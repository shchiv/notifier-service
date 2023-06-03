package main

import (
	"context"
	"fmt"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/pkg/errors"
	"github.com/shchiv/notifier-service/pkg/cli"
	"github.com/shchiv/notifier-service/pkg/goid"
	prommetrics "github.com/shchiv/notifier-service/pkg/metrics"
	"math/rand"
	"os"
	"strings"
	"time"

	"go.uber.org/ratelimit"

	"github.com/gofrs/uuid"
	ku "github.com/shchiv/notifier-service/pkg/kafka"
	"github.com/shchiv/notifier-service/pkg/log"
)

var cliOptions struct {
	cli.KafkaArgs

	NumberOfCustomers uint16 `short:"c" long:"customers-count" description:"Number of customers, default is '1'" default:"1"`
	NumberOfEvents    uint64 `short:"e" long:"events-count" description:"Number of events produced per customer, default is '0' (unlimited)" default:"0"`
	OrdersByGenerator uint64 `short:"o" long:"orders-generator" description:"Maximal number of orders per generator, default is '100'" default:"100"`
	RateLimit         int    `short:"r" long:"rate-limit" description:"Number of events produced(enqueued) per second among all the customers, default '1'" default:"1"`
}

var errEllEventsSent = errors.New("Requested number of events enqueued")

var metrics = newMetrics()

func main() {
	cli.Parse(&cliOptions)

	brokersList := cliOptions.GetBrokerList()

	customerIDs := make([]uuid.UUID, 0, cliOptions.NumberOfCustomers)
	for i := 0; i < cap(customerIDs); i++ {
		customerIDs = append(customerIDs, uuid.Must(uuid.NewV6()))
	}

	eventChan := make(chan *ku.OrderStateChangedEvent, 100)
	defer close(eventChan)

	rateLimiter := ratelimit.New(cliOptions.RateLimit)

	s := prommetrics.NewHTTPServerWithMetricsEndpoint()
	defer s.Stop()

	s.AddGoRoutine(func() error {
		return sender(s.Ctx, brokersList, rateLimiter, eventChan)
	})

	// -- Customer event generators
	for _, cid := range customerIDs {
		cid := cid
		s.AddGoRoutine(func() error {
			return generator(s.Ctx, cid, eventChan)
		})
	}

	s.StartServer(prommetrics.AddrForKafka(brokersList))
}

func generator(ctx context.Context, customer uuid.UUID, outEvents chan<- *ku.OrderStateChangedEvent) error {
	logger := log.Logger().Named(fmt.Sprintf("Gen#%d", goid.GetGID()))
	i := uint64(0)

	logger.Infof("Customer event stream generator started")

	orders := preGenOrders(cliOptions.OrdersByGenerator)
	for {
		for range orders {
			randIndex := uint64(rand.Intn(len(orders)))
			order := orders[randIndex]

			event := ku.OrderStateChangedEvent{
				ID:             uuid.Must(uuid.NewV6()),
				CustomerID:     customer,
				OrderID:        order.ID,
				State:          order.CurrentState,
				OccurredAt:     time.Now(),
				SequenceNumber: order.SequenceNumber,
			}

			// if we have reached the last number in the sequence
			// this means that all order events have already been sent,
			// and we can pull a new one order
			if order.SequenceNumber == len(ku.OrderStateTypes)-1 {
				// replace the old one with a newly generated one
				orders[randIndex] = newOrder()
			} else {
				// get next state, increment seq number, replace and go ahead to the next order
				order.CurrentState = randOrderState()
				order.SequenceNumber++
				orders[randIndex] = order
			}

			for {
				select {
				case <-ctx.Done():
					goto exitLbl
				case outEvents <- &event:
					//logger.Debugf("[Generated] #%s Customer: %s, Order: %s, SeqNum: %d, Status: %s", event.ID, event.CustomerID, event.ID, event.SequenceNumber, event.State)
					logger.Debugf("[Generated] %s", event.ID)
					goto continueLbl
				case <-time.After(10 * time.Millisecond):
				}
			}

		continueLbl:
		}

		i++
		// if we have reached requested number of events - producer will stop
		if i == cliOptions.NumberOfEvents {
			break
		}
	}

exitLbl:
	logger.Infof("Customer event stream generator completed")
	return nil
}

type Order struct {
	ID             uuid.UUID
	CurrentState   ku.OrderStateType
	OccurredAt     time.Time
	SequenceNumber int
}

func preGenOrders(num uint64) []Order {
	m := make([]Order, num)
	for i := range m {
		m[i] = newOrder()
	}
	return m
}

func newOrder() Order {
	return Order{
		ID:             uuid.Must(uuid.NewV6()),
		OccurredAt:     time.Now(),
		SequenceNumber: 0,
		CurrentState:   randOrderState(),
	}
}

func randOrderState() ku.OrderStateType {
	index := rand.Intn(len(ku.OrderStateTypes))
	return ku.OrderStateTypes[index]
}

func sender(ctx context.Context, brokerList []string, rateLimiter ratelimit.Limiter, inEvents <-chan *ku.OrderStateChangedEvent) error {
	logger := log.Logger().Named(fmt.Sprintf("Sen#%d", goid.GetGID()))
	logger.Infof("Kafka sender started")

	bootstrapServers := strings.Join(brokerList[:], ",")

	hostname, err := os.Hostname()
	if err != nil {
		panic(fmt.Sprintf("Error getting hostname: %+v", err))
	}

	// TODO: re-work producer to be more durable, as it was done for consumer
	producer, err := ku.Producer(bootstrapServers, hostname)
	if err != nil {
		panic(fmt.Sprintf("Error creating kafka producer: %+v", err))
	}
	defer producer.Close()

	var event *ku.OrderStateChangedEvent
	var ok bool

	sentEventCounter := int64(0)
	for ; cliOptions.NumberOfEvents == 0 || sentEventCounter < int64(cliOptions.NumberOfEvents); sentEventCounter++ {
		select {
		case <-ctx.Done():
			goto exitLbl

		case event, ok = <-inEvents:
			if !ok {
				//// TODO: proper error handling
				goto exitLbl
			}

		case <-time.After(10 * time.Millisecond):
			continue
		}

		// Consider incorporating rate limiting logic into select
		rateLimiter.Take()

		var msg *kafka.Message
		if msg, err = event.ToKafkaMessage(&cliOptions.KafkaTopic); err != nil {
			err = errors.Wrapf(err, "Error enqueuing message: %+v", err)
			goto errExitLbl
		} else {
			// TODO: for the sake of consistency it is either required to do a synchronous produce or change logic accordingly to async (metrics, rate limiter)
			if err = producer.Produce(msg, nil); err != nil {
				err = errors.Wrapf(err, "Error enqueuing message: %+v", err)
				goto errExitLbl
			}
		}

		metrics.enqueuedEventsTotal.WithLabelValues(event.CustomerID.String()).Inc()
		logger.Debugf("[ Enqueued ] %s", event.ID.String())
	}

exitLbl:
	logger.Debugf("Kafka sender: flushing events...")
	producer.Flush(10 * 1000)
	logger.Infof("Kafka sender completed: %d events enqueued", sentEventCounter)
	return errEllEventsSent

errExitLbl:
	logger.Errorf("Kafka sender completed with error: %+v", err)
	return err
}
