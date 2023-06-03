package notifier

import (
	"database/sql"
	"github.com/gofrs/uuid"
	"time"
)

type Event struct {
	ID          uuid.UUID    `json:"id"`
	CustomerID  uuid.UUID    `json:"customer_id"`
	OccurredAt  time.Time    `json:"occurred_at"`
	ProcessedAt sql.NullTime `json:"processed_at"`
	LastErrorAt sql.NullTime `json:"last_error_at"`
	Payload     []byte       `json:"payload"`
}
