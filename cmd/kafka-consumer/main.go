package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	_ "github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shchiv/notifier-service/pkg/goid"
	ku "github.com/shchiv/notifier-service/pkg/kafka"
	prommetrics "github.com/shchiv/notifier-service/pkg/metrics"
	"go.uber.org/zap/zapcore"
	"os"
	"time"

	"github.com/shchiv/notifier-service/pkg/cli"
	"github.com/shchiv/notifier-service/pkg/log"
	"golang.org/x/exp/slices"
	"strings"
)

var cliOptions struct {
	cli.KafkaArgs
	cli.DBArgs
}

var metrics = newMetrics()

func main() {
	cli.Parse(&cliOptions)

	brokersList := cliOptions.GetBrokerList()

	s := prommetrics.NewHTTPServerWithMetricsEndpoint()
	defer s.Stop()

	s.AddGoRoutine(func() error {
		bootstrapServers := strings.Join(brokersList[:], ",")
		return consumeAndIngest(s.Ctx, bootstrapServers)
	})

	s.StartServer(prommetrics.AddrForKafka(brokersList))
}

// consumeAndIngest Ingests messages consumed from Kafka topic to DB
func consumeAndIngest(ctx context.Context, boostrapServers string) error {
	logger := log.Logger().Named(fmt.Sprintf("Cons#%d", goid.GetGID()))

	for {
		db := cliOptions.CreatePGXConnection(ctx)
		defer db.Close(ctx)

		hostname, err := os.Hostname()
		if err != nil {
			panic(fmt.Sprintf("Error getting hostname: %+v", err))
		}

		consumer, err := ku.Consumer(boostrapServers, "event-consumers", hostname)
		if err != nil {
			logger.Errorf("Error creating kafka consumer: %+v", err)
			goto reconnectLbl
		}
		defer consumer.Close()

		err = consumer.SubscribeTopics([]string{cliOptions.KafkaTopic}, nil)
		if err != nil {
			logger.Errorf("Error subscribing to kafka topic '%s': %+v", cliOptions.KafkaTopic, err)
			goto reconnectLbl
		}

		// Message loop
		{
			const batchCapacity = 100
			const batchMaxWait = 500 * time.Millisecond
			msgBatch := make([]*kafka.Message, batchCapacity)
			idsBatch := make([]*uuid.UUID, batchCapacity)
			payloadBatch := newPayloadBatch(batchCapacity)

			for {
				// -- Collect batch
				payloadBatch.Reset()
				batchDeadlineAt := time.Now().Add(batchMaxWait)
				batchSize := 0
				for batchSize < batchCapacity {
					maxWait := time.Until(batchDeadlineAt)
					if maxWait < 0 {
						// overdue
						break
					}

					ev := consumer.Poll(int(maxWait.Milliseconds()))
					if ev == nil {
						continue
					}

					switch e := ev.(type) {
					case *kafka.Message:
						msgBatch[batchSize] = e
						batchSize++

					case kafka.Error:
						// TODO: add proper error handling
						//  to be sure PostgreSQL store all data from batch and possibly merge values
						panic(fmt.Sprintf("Got en error from kafka: %+v", e))

					case *kafka.Stats:
						// Stats events are emitted as JSON (as string).
						// Either directly forward the JSON to your
						// statistics collector, or convert it to a
						// map to extract fields of interest.
						// The definition of the statistics JSON
						// object can be found here:
						// https://github.com/confluentinc/librdkafka/blob/master/STATISTICS.md
						// https://github.com/seglo/kafka-lag-exporter/blob/master/src/main/scala/com/lightbend/kafkalagexporter/Metrics.scala
						// https://gist.github.com/fl64/a86b3d375deb947fa11099dd374660da
						var stats map[string]interface{}
						err := json.Unmarshal([]byte(e.String()), &stats)
						if err != nil {
							panic(fmt.Sprintf("Error parsing stats: %+v", err))
						}

						fmt.Printf("Stats: %v messages (%v bytes) messages consumed\n",
							stats["rxmsgs"], stats["rxmsg_bytes"])

					default:
						logger.Warnf("Unhandled event will be ignored: %v\n", e)
					}
				}

				if batchSize <= 0 {
					// Nothing collected yet
					continue
				}

				// -- Populate id & payload batches
				for i := 0; i < batchSize; i++ {
					msg := msgBatch[i]
					id := ku.GetEventIDFromKafkaMessage(msg)
					idsBatch[i] = id
					payloadBatch.Add(msg.Value)
				}

				tx, err := db.Begin(ctx)
				if err != nil {
					panic(fmt.Sprintf("Error starting transaction: %+v", err))
				}

				succeeded, err := tx.CopyFrom(ctx, pgx.Identifier{"events"}, []string{"payload"}, payloadBatch)
				if err != nil {
					if succeeded > 0 {
						// TODO: Add granular handling: in case unrecoverable error: send unprocessed messages to DLQ
						affectedIDs := idsBatch[succeeded:]
						logger.Warnf("Error ingesting the entire batch, %d events were faield to ingest, will be re-tried:  %s. Error: %v",
							payloadBatch.Size()-int(succeeded), affectedIDs, err,
						)
					} else {
						logger.Warnf("Error ingesting the entire batch: %v", err)
					}
				}

				if succeeded <= 0 {
					_ = tx.Rollback(ctx)
					continue
				}

				// -- Commit ingested messages
				err = tx.Commit(ctx)
				if err != nil {
					if err2 := tx.Rollback(ctx); err2 != nil {
						logger.Errorf("An error occurred (1) while rolling back database changes "+
							"after DB commit failure (2):\n"+
							"\n(1) %+v\n"+
							"\n(2) %+v",
							err, err2,
						)
					} else {
						logger.Errorf("Error commiting DB changes, all changes were rolled back: %+v", err)
					}

					// Do not commit offset to Kafka since nothing is ingested.
					// It is expected that the consumer will re-process these messages (the entire batch) one more time.
					// That implies a TODO: mitigate a poison message, and ensure that the same message will not cause re-processing infinitely.
					continue
				}

				succeededMsgs := msgBatch[:succeeded]
				incTotalIngestedEvents(succeededMsgs)

				partitionsWithOffsets := calculateOffsetDiffPerPartition(succeededMsgs)
				committedOffsets, err := consumer.CommitOffsets(partitionsWithOffsets)
				if err != nil {
					logger.Errorf("Error commiting kafka offsets:\n   %s", dumpTopicPartitionOffsets(partitionsWithOffsets, "\n   "))
					// It is expected that the consumer will re-process these messages (the entire batch) one more time.
					// That implies a TODO: mitigate DB insertion failure (duplicated records) during re-processing of these uncommitted kafka messages
				} else {
					if logger.Level() <= zapcore.DebugLevel {
						logger.Debugf("[Ingested]: %d of %d events, kafka offsets:\n   %s",
							succeeded, batchSize,
							dumpTopicPartitionOffsets(committedOffsets, "\n   "),
						)
					}
				}
			}
		}

	reconnectLbl:
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			// Re-try attempt after timeout
		}
	}
}

func incTotalIngestedEvents(messages []*kafka.Message) {
	totalIngestedEvents := make(map[uuid.UUID]prometheus.Counter, len(messages))
	var counter prometheus.Counter
	for _, msg := range messages {
		customerID := ku.GetCustomerIDFromKafkaMessage(msg)
		if cntr, ok := totalIngestedEvents[*customerID]; ok {
			counter = cntr
		} else {
			counter = metrics.ingestedEventsTotal.WithLabelValues(customerID.String())
			totalIngestedEvents[*customerID] = counter
		}

		counter.Inc()
	}
}

// --
func dumpTopicPartitionOffsets(partitions []kafka.TopicPartition, delim string) string {
	slices.SortFunc(partitions, func(a, b kafka.TopicPartition) bool {
		return a.Partition > b.Partition
	})

	sbld := strings.Builder{}

	for _, p := range partitions {
		if sbld.Len() > 0 {
			sbld.WriteString(delim)
		}
		sbld.WriteString(fmt.Sprintf("%s#%d: %d", *p.Topic, p.Partition, p.Offset))
	}

	return sbld.String()
}

// calculateMessagesPerPartition Assume that all the messages come from the same topic
func calculateOffsetDiffPerPartition(msgs []*kafka.Message) []kafka.TopicPartition {
	var partitions []kafka.TopicPartition
	for _, msg := range msgs {
		idx := slices.IndexFunc(partitions, func(p kafka.TopicPartition) bool {
			return msg.TopicPartition.Partition == p.Partition
		})

		if idx == -1 {
			pPartition := kafka.TopicPartition{
				Topic:     msg.TopicPartition.Topic,
				Partition: msg.TopicPartition.Partition,
				Offset:    msg.TopicPartition.Offset + 1,
			}

			//originalPartitions = append(originalPartitions, msg.TopicPartition)
			partitions = append(partitions, pPartition)
		} else {
			pPartition := &partitions[idx]
			if pPartition.Offset != msg.TopicPartition.Offset {
				// Consider to dump the entire batch
				panic("Message dsoes not have monotonically increased offset")
			}

			pPartition.Offset++
			//log.Logger().Debugf("%d", pPartition.Offset)
		}
	}

	return partitions
}
