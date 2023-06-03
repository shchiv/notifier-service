package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shchiv/notifier-service/pkg/cli"
	"github.com/shchiv/notifier-service/pkg/goid"
	"github.com/shchiv/notifier-service/pkg/log"
	prommetrics "github.com/shchiv/notifier-service/pkg/metrics"
	"github.com/shchiv/notifier-service/pkg/notifier"
	"go.uber.org/zap"
	"net/http"
	"net/http/httputil"
	"strconv"
	"time"
	"unsafe"
)

const (
	lockAndGetStmt = "lockAndGetEventForProcessing"
	updateStmt     = "updateEventDeliveryStatus"
)

var cliOptions struct {
	cli.DBArgs

	Number uint   `short:"t" long:"threads" description:"Number of threads to process events" default:"5"`
	Sleep  uint64 `short:"s" long:"sleep" description:"Sleep amount (in seconds)" default:"5"`
	Chaos  bool   `long:"chaos" description:"Simulate actual notification handlers"`
}

var metrics = newMetrics()

func main() {
	cli.Parse(&cliOptions)

	poolCfg, err := pgxpool.ParseConfig(cliOptions.DBConnectionString)
	if err != nil {
		log.Logger().Fatalf("Error parsing connection string: %+v", err)
	}

	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	poolCfg.MinConns = int32(cliOptions.Number)
	// as we are using pool, we can add prepared statements only on afterConnect
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err = conn.Prepare(ctx, lockAndGetStmt, `SELECT (pe.event).id, (pe.event).customer_id, (pe.event).occurred_at,
					(pe.event).processed_at, (pe.event).last_error_at, (pe.event).payload,
					pe.webhook
				FROM  lockAndGetEventForProcessing() pe`); err != nil {
			return err
		}
		if _, err = conn.Prepare(ctx, updateStmt, "CALL updateEventDeliveryStatus($1, $2)"); err != nil {
			return err
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		log.Logger().Fatalf("Error creating db connection poll: %+v", err)
	}
	defer pool.Close()

	s := prommetrics.NewHTTPServerWithMetricsEndpoint()
	defer s.Stop()

	for i := uint(0); i < cliOptions.Number; i++ {

		s.AddGoRoutine(func() error {
			conn, err := pool.Acquire(s.Ctx)
			if err != nil {
				return errors.Wrapf(err, "Error acquiring db connection")
			}
			defer conn.Release()

			notificationLoop(s.Ctx, conn)
			return nil
		})
	}

	s.StartServer(":1234")
}

func notificationLoop(ctx context.Context, conn *pgxpool.Conn) {
	strGid := fmt.Sprint(goid.GetGID())
	threadID := fmt.Sprintf("Ntf#%s-%d", strGid, conn.Conn().PgConn().PID())
	logger := log.Logger().Named(threadID)
	logger.Debugf("notificationLoop started")

	var event notifier.Event
	var webhook sql.NullString
	var deliveryError pgtype.Text
	waitingEventsBanner := true
	var strCustomerID string
	noEventsForProcessing := false
	for {
		now := time.Now()

		tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, DeferrableMode: pgx.NotDeferrable})
		if err != nil {
			logger.Fatalf("Error starting tx: %+v", err)
		}

		// -- Select event and lock on it to prevent parallel processing
		err = tx.QueryRow(ctx, lockAndGetStmt).Scan(&event.ID, &event.CustomerID, &event.OccurredAt,
			&event.ProcessedAt, &event.LastErrorAt, &event.Payload, &webhook)
		if err != nil {
			var pqErr *pgconn.PgError
			if errors.As(err, &pqErr) {
				if pqErr.Code == "40001" {
					//  ERROR: could not serialize access due to concurrent update (SQLSTATE 40001)
					logger.Debugf("Got serialize access error, will re-try")
					goto lblRollback
				}
			}

			if noEventsForProcessing = errors.Is(err, pgx.ErrNoRows); !noEventsForProcessing {
				logger.Fatalf("Error getting event from DB: %+v", err)
			}
			goto lblRollback
		}
		waitingEventsBanner = true
		noEventsForProcessing = false

		strCustomerID = event.CustomerID.String()
		logger.Debugf("Processing: %s", event.ID.String())

		if cliOptions.Chaos {
			err = doChaosEventDelivery(&event, webhook.String, logger)
		} else {
			err = doEventDelivery(&event, webhook.String, threadID, logger)
		}

		deliveryError.Valid = err != nil
		if err != nil {
			deliveryError.String = err.Error()
			logger.Errorf("Failed to deliver notification %s to customer %s(%s): %v", event.ID.String(), strCustomerID, webhook.String, err)
		} else {
			logger.Debugf("Event %s delivered to %s", event.ID.String(), webhook.String)
		}

		if _, err = tx.Exec(ctx, updateStmt, event.ID, deliveryError); err != nil {
			var pqErr *pgconn.PgError
			if errors.As(err, &pqErr) {
				switch pqErr.Code {
				case "ICERR":
					panic(fmt.Sprintf("DETECTED: Out of order event processing: %s", pqErr.Message))
				}
			}
			err = errors.Wrapf(err, "Error updating delivery status for event event %s", event.ID.String())

			goto lblError
		}

		if err = tx.Commit(ctx); err != nil {
			err = errors.Wrapf(err, "Error commiting delivery status update for event %s", event.ID.String())

			goto lblError
		}
		logger.Debugf("Processed(commited): %s", event.ID.String())

		metrics.postedEventsByThreadTotal.With(prometheus.Labels{"customer_id": strCustomerID, "thread_id": threadID}).Inc()
		metrics.requestDurationSeconds.With(prometheus.Labels{"customer_id": strCustomerID}).Observe(time.Since(now).Seconds())

		continue

	lblError:
		panic(fmt.Sprintf("Unexpected error occurred, transaction will be rolled back: %+v", err))

	lblRollback:
		if err = tx.Rollback(ctx); err != nil {
			logger.Errorf("Failed rollback tx: %+v", err)
		}

		if noEventsForProcessing {
			if waitingEventsBanner {
				logger.Debug("Waiting events to deliver...")
				waitingEventsBanner = false
			}

			select {
			case <-ctx.Done():
				goto lblExit

			case <-time.After(time.Duration(cliOptions.Sleep) * time.Second):
			}
		}
	}

lblExit:
	logger.Debugf("notificationLoop exited")
}

func doEventDelivery(event *notifier.Event, webhook, threadID string, _ *zap.SugaredLogger) error {
	strCustomerID := event.CustomerID.String()

	// TODO: Lag must be calculated only for events that are about to deliver for the 1st time (last_error_at IS NULL)
	metrics.notificationLag.With(
		prometheus.Labels{"customer_id": strCustomerID, "thread_id": threadID},
	).Observe(float64(time.Since(event.OccurredAt).Milliseconds()))

	if len(webhook) == 0 {
		return errors.Errorf("Customer '%s' does not have registered handler,  delivery will be postponed", strCustomerID)
	}

	// -- Deliver notification
	payloadReader := bytes.NewReader(event.Payload)
	resp, err := http.Post(webhook, "application/json", payloadReader)
	// FOR DEBUG
	if resp != nil && resp.StatusCode == http.StatusConflict {
		log.Logger().Debugf("Duplication Error Happened")
		metrics.failedEventsTotal.With(prometheus.Labels{"customer_id": strCustomerID, "status": strconv.Itoa(http.StatusConflict)}).Inc()
		panic("Panic Happened!")
	}

	mode := "default"
	if resp != nil {
		mode = resp.Header.Get("mode")
		metrics.deliveryLag.With(
			prometheus.Labels{"customer_id": strCustomerID, "thread_id": threadID, "mode": mode},
		).Observe(float64(time.Since(event.OccurredAt).Milliseconds()))
	}

	if event.LastErrorAt.Valid && !event.LastErrorAt.Time.IsZero() {
		metrics.retriedEventsTotal.With(prometheus.Labels{"customer_id": strCustomerID, "mode": mode}).Inc()
	}

	metrics.sentEventsTotal.With(prometheus.Labels{"customer_id": strCustomerID, "mode": mode}).Inc()

	//  -- Update event delivery status
	delivered := err == nil && resp.StatusCode == http.StatusOK
	if !delivered {
		var deliveryError string
		var statusCode int
		if resp != nil {
			statusCode = resp.StatusCode
			defer resp.Body.Close()
			if body, err := httputil.DumpResponse(resp, true); err != nil {
				deliveryError = unsafe.String(unsafe.SliceData(body), len(body))
			} else {
				deliveryError = fmt.Sprintf("HTTP %d", statusCode)
			}
			metrics.failedEventsTotal.With(prometheus.Labels{"customer_id": strCustomerID, "status": strconv.Itoa(statusCode)}).Inc()
			metrics.failedEventsByThreadTotal.With(prometheus.Labels{"customer_id": strCustomerID, "thread_id": threadID}).Inc()
		} else {
			deliveryError = fmt.Sprintf("%+v", err)
		}

		return errors.New(deliveryError)
	}

	metrics.postedEventsTotal.With(prometheus.Labels{"customer_id": strCustomerID, "mode": mode}).Inc()
	return nil
}
