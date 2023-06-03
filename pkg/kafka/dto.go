package kafka

import (
	"database/sql/driver"
	"encoding/json"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/pkg/errors"
	"github.com/shchiv/notifier-service/pkg/log"
	"time"

	"github.com/gofrs/uuid"
)

type OrderStateType string

const (
	State1 OrderStateType = "state1"
	State2 OrderStateType = "state2"
	State3 OrderStateType = "state3"
	State4 OrderStateType = "state4"
	State5 OrderStateType = "state5"
	State6 OrderStateType = "state6"
	State7 OrderStateType = "state7"
	State8 OrderStateType = "state8"
)

var OrderStateTypes = []OrderStateType{
	State1, State2, State3, State4, State5,
	State6, State7, State8,
}

type OrderStateChangedEvent struct {
	ID         uuid.UUID      `json:"id"`
	CustomerID uuid.UUID      `json:"customer_id"`
	OrderID    uuid.UUID      `json:"order_id"`
	State      OrderStateType `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	// SequenceNumber artificial number value of event in order sequence
	SequenceNumber int `json:"sequence_number"`
}

func (e *OrderStateChangedEvent) Value() (driver.Value, error) {
	return json.Marshal(e)
}

func (e *OrderStateChangedEvent) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(b, &e)
}

func (e *OrderStateChangedEvent) ToKafkaMessage(topic *string) (*kafka.Message, error) {
	var payload []byte
	payload, err := json.Marshal(e)
	if err != nil {
		// TODO: proper error handling
		log.Logger().Fatalf("Can't marshal: %+v", err)
	}

	var bytesOrderID []byte
	if bytesOrderID, err = e.OrderID.MarshalBinary(); err != nil {
		return nil, errors.Wrapf(err, "Error marshaling event.orderID")
	}

	return &kafka.Message{
		Key:   bytesOrderID,
		Value: payload,
		Headers: []kafka.Header{
			{Key: "id", Value: e.ID.Bytes()},
			{Key: "customer_id", Value: e.CustomerID.Bytes()},
		},
		TopicPartition: kafka.TopicPartition{Topic: topic, Partition: kafka.PartitionAny},
	}, nil
}

func GetEventIDFromKafkaMessage(msg *kafka.Message) *uuid.UUID {
	if len(msg.Headers) < 1 || msg.Headers[0].Key != "id" {
		panic("Event ID is not provided within kafka message headers")
	}

	retVal := uuid.Must(uuid.FromBytes(msg.Headers[0].Value))
	return &retVal
}

func GetCustomerIDFromKafkaMessage(msg *kafka.Message) *uuid.UUID {
	if len(msg.Headers) < 2 || msg.Headers[1].Key != "customer_id" {
		panic("CustomerID is not provided within kafka message headers")
	}

	retVal := uuid.Must(uuid.FromBytes(msg.Headers[1].Value))
	return &retVal
}
