package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shchiv/notifier-service/pkg/cli"
	"github.com/shchiv/notifier-service/pkg/kafka"
	"github.com/shchiv/notifier-service/pkg/log"
	prommetrics "github.com/shchiv/notifier-service/pkg/metrics"
	"net/http"
	"os"
	"time"
)

type Server struct {
	ctx  context.Context
	pool *pgxpool.Pool
}

func NewServer(ctx context.Context, pool *pgxpool.Pool) *Server {
	return &Server{ctx: ctx, pool: pool}
}

var cliOptions struct {
	cli.DBArgs

	Port             string `short:"p" long:"port" description:"Application port" required:"true"`
	ProcessingWindow int    `short:"w" long:"proc-window" description:"Time in seconds that indicates that the catcher is not active at least for this time, default '60'" default:"60"`
}

var metrics = newMetrics()

func main() {
	cli.Parse(&cliOptions)

	pool, err := pgxpool.New(context.Background(), cliOptions.DBConnectionString)
	if err != nil {
		log.Logger().Fatalf("Error creating db connection poll: %+v", err)
	}
	defer pool.Close()

	s := prommetrics.NewHTTPServerWithMetricsEndpoint()
	defer s.Stop()

	s.AddGoRoutine(func() error {
		webhook := webhookURL()
		registerCatcher(s.Ctx, pool, webhook)
		log.Logger().Debugf("Registered catcher: %s", webhook)
		return nil
	})

	s.AddGoRoutine(func() error {
		srv := NewServer(s.Ctx, pool)
		http.HandleFunc("/catch", chaosMonkey(srv.catch))
		return nil
	})

	s.StartServer(fmt.Sprintf(":%s", cliOptions.Port))
}

func (s *Server) catch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST is allowed", http.StatusMethodNotAllowed)
		return
	}
	e := new(kafka.OrderStateChangedEvent)
	err := json.NewDecoder(r.Body).Decode(&e)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	metrics.receivedEventsTotal.WithLabelValues(e.CustomerID.String()).Inc()

	if _, err = s.pool.Exec(s.ctx, "CALL updateOrderProjection($1, $2)", e.OrderID, e.SequenceNumber); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "ICERR":
				log.Logger().Infof("Out of order issue happened: %s", pgErr.Message)
			case "ERROR":
				log.Logger().Infof("Error happened: %s", pgErr.Message)
			default:
				log.Logger().Infof("Unexpected error has arrived: %s error: %+v", e.ID.String(), *pgErr)
			}
			metrics.unprocEventsTotal.With(prometheus.Labels{"customer_id": e.CustomerID.String(), "code": pgErr.Code}).Inc()
			w.WriteHeader(http.StatusConflict)
			w.Header().Set("Content-Type", "application/json")
			if _, err = w.Write([]byte(fmt.Sprintf(`"error":%s`, err.Error()))); err != nil {
				log.Logger().Infof("Can't send error response: %v", err)
				return
			}
			return
		} else {
			log.Logger().Errorf("Can't insert projection ID %s error: %+v", e.ID.String(), err)
		}
		return
	}
}

func webhookURL() string {
	hn, err := os.Hostname()
	if err != nil {
		log.Logger().Fatalf("Error getting hostname: %+v", err)
	}
	return fmt.Sprintf("http://%s:%s/catch", hn, cliOptions.Port)
}

func registerCatcher(ctx context.Context, pool *pgxpool.Pool, addr string) {
	var customerID sql.NullString
	for {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			log.Logger().Fatalf("Failed to start tx: %v", err)
		}

		err = tx.QueryRow(ctx, "SELECT * FROM registerCustomerMock($1, $2)", addr, &cliOptions.ProcessingWindow).Scan(&customerID)
		if err == nil && customerID.Valid {
			if err = tx.Commit(ctx); err != nil {
				err = errors.Wrapf(err, "Error committing tx after successful registration")
			} else {
				goto exitLbl
			}
		}

		if err != nil {
			log.Logger().Errorf("Error performing registration: %v", err)
		} else {
			log.Logger().Infof("Failed to register, all customers are serving, nothing to serve, registration will be re-attempted...")
		}

		if err = tx.Rollback(ctx); err != nil {
			log.Logger().Fatalf("Error rolling back tx after unsuccessful registration: %v", err)
		}

		time.Sleep(5 * time.Second)
	}
exitLbl:
	log.Logger().Infof("Registration succeeded:  registered to serve %s", customerID.String)
}
