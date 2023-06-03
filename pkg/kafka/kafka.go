package kafka

import (
	"fmt"
	libkafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/segmentio/kafka-go"

	kb "github.com/philipjkim/kafka-brokers-go"
	"github.com/pkg/errors"

	"github.com/shchiv/notifier-service/pkg/log"
)

// Consumer https://docs.confluent.io/kafka-clients/go/current/overview.html#go-example-code
func Consumer(bootstrapServers, groupID, hostname string) (*libkafka.Consumer, error) {
	return libkafka.NewConsumer(&libkafka.ConfigMap{
		"bootstrap.servers":  bootstrapServers,
		"group.id":           groupID,
		"enable.auto.commit": "false",
		"auto.offset.reset":  "earliest",

		"client.id": hostname,

		//  Statistics output from the client may be enabled
		// by setting statistics.interval.ms and
		// handling kafka.Stats events (see below).
		"statistics.interval.ms": 1000},
	)
}

// Producer https://github.com/confluentinc/confluent-kafka-go/blob/master/examples/idempotent_producer_example/idempotent_producer_example.go
func Producer(bootstrapServers, hostname string) (*libkafka.Producer, error) {
	return libkafka.NewProducer(&libkafka.ConfigMap{
		"bootstrap.servers": bootstrapServers,
		"client.id":         hostname,

		"partitioner": "fnv1a",

		/**
		 * The following configuration properties are adjusted automatically (if not modified by the user)
		 * when idempotence is enabled: max.in.flight.requests.per.connection=5 (must be less than or equal to 5),
		 * retries=INT32_MAX (must be greater than 0), acks=all, queuing.strategy=fifo.
		 */
		"enable.idempotence": true,
	})
}

func GetBrokersList(bootstrapServer string, zooKeeperServers ...string) ([]string, error) {
	var brokerList []string
	if len(zooKeeperServers) > 0 {
		// Retrieve a list of active kafka brokers from zookeeper
		c, err := kb.NewConn(zooKeeperServers)
		if err != nil {
			log.Logger().Fatalf("Failed to connect to zookeeper servers '%s': %+v", zooKeeperServers, err)
		}

		brokerList, _, err = c.GetW()
		c.Close()
		if err != nil {
			return nil, errors.Wrapf(err,
				"Failed to retrieve a list of active kafka brokers from zookeeper '%s'",
				zooKeeperServers,
			)
		}

		log.Logger().Debugf("Successfully retrieved list of active Kafka brokers: %s", brokerList)
	} else if len(bootstrapServer) > 0 {
		// Make a list of active kafka brokers from controller
		conn, err := kafka.Dial("tcp", bootstrapServer)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to bootsrap kafka server '%s'", bootstrapServer)
		}
		defer conn.Close()

		brokers, err := conn.Brokers()
		if err != nil {
			return nil, errors.Wrapf(err,
				"Failed to retrieve a list of active kafka brokers from bootstrap server '%s'",
				zooKeeperServers,
			)
		}

		for _, b := range brokers {
			brokerList = append(brokerList, fmt.Sprintf("%s:%d", b.Host, b.Port))
		}
	} else {
		return nil, errors.Errorf("Either one of 'bootstrapServer' or 'zooKeeperServers' arguments must be provided")
	}

	return brokerList, nil
}
