package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jessevdk/go-flags"
	ku "github.com/shchiv/notifier-service/pkg/kafka"
	"github.com/shchiv/notifier-service/pkg/log"
	"os"
)

type KafkaArgs struct {
	ZooKeeperServers     []string `short:"z" long:"zookeeper" description:"ZooKeeper servers to retrieve active kafka brokers" required:"false"`
	BootstrapKafkaServer string   `short:"k" long:"bootstrap-server" description:"Kafka bootstrap server to retrieve active kafka brokers" required:"false"`
	KafkaTopic           string   `short:"t" long:"topic" description:"Kafka topic" required:"true"`
}

func (args *KafkaArgs) GetBrokerList() []string {
	var brokerList []string
	var err error

	if len(args.ZooKeeperServers) > 0 {
		brokerList, err = ku.GetBrokersList(args.BootstrapKafkaServer, args.ZooKeeperServers...)
	} else {
		brokerList, err = ku.GetBrokersList(args.BootstrapKafkaServer)
	}

	if err != nil {
		log.Logger().Fatalf("Failed to retrive a list of active kafka brokers: %+v", err)
	}

	return brokerList
}

type DBArgs struct {
	DBConnectionString string `short:"c" long:"db-connection" description:"Database connection string, the only PostgreSQL is supported" required:"true" `
}

func (args *DBArgs) CreateDBConnection() *sql.DB {
	db, err := sql.Open("postgres", args.DBConnectionString)
	if err != nil {
		log.Logger().Fatalf("Error creating connection to DB: %+v", err)
	}

	if err = db.Ping(); err != nil {
		log.Logger().Fatalf("Error establishing connection to DB: %+v", err)
	}

	return db
}

func (args *DBArgs) CreatePGXConnection(ctx context.Context) *pgx.Conn {
	conn, err := pgx.Connect(ctx, args.DBConnectionString)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	if err = conn.Ping(ctx); err != nil {
		log.Logger().Fatalf("Error establishing connection to DB: %+v", err)
	}

	return conn
}

func Parse(cliOptions interface{}) {
	parser := flags.NewParser(cliOptions, flags.Default)
	_, err := parser.Parse()
	if err != nil {
		var flagsErr flags.ErrorType
		if errors.As(err, &flagsErr) {
			if flagsErr == flags.ErrHelp {
				os.Exit(0)
			}
		}
		log.Logger().Fatalf("Can't parse CLI options: %+v", err)
	}
}
