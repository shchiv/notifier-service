package metrics

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shchiv/notifier-service/pkg/log"
	"golang.org/x/sync/errgroup"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"
)

type HTTPServerWithMetricsEndpoint struct {
	errGroup *errgroup.Group
	Ctx      context.Context
	Stop     context.CancelFunc
}

func NewHTTPServerWithMetricsEndpoint() *HTTPServerWithMetricsEndpoint {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	g, ctxGroup := errgroup.WithContext(ctx)
	return &HTTPServerWithMetricsEndpoint{
		errGroup: g,
		Ctx:      ctxGroup,
		Stop:     stop,
	}
}

func (g *HTTPServerWithMetricsEndpoint) AddGoRoutine(f func() error) {
	g.errGroup.Go(func() error {
		return f()
	})
}

func AddrForKafka(brokersList []string) string {
	/**
	 * It's better to avoid starting Metrics endpoint on 0:0:0:0, to avoid listening on all bounded networks.
	 * In case several networks, Prometheus due to lack of options can't filter SD results by proper network,
	 * as a result there are will be N (whereas 'n' - is number of bounded networks) targets that will be processed
	 * and generate duplicated data
	 */
	return fmt.Sprintf("%s:1234", MustFigureProperIP(brokersList).String())
}

func MustFigureProperIP(brokersList []string) net.IP {
	host, _, err := net.SplitHostPort(brokersList[0])
	if err != nil {
		log.Logger().Fatalf("Error splitting host and port")
	}

	kafkaBrokerIPs, err := net.LookupHost(host)
	if err != nil {
		panic(err)
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Logger().Fatalf("Error obtaining list of network addresses: %+v", err)
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok /*&& !ipnet.IP.IsLoopback()*/ {
			for _, brokerIPStr := range kafkaBrokerIPs {
				kafkaBrokerIP := net.ParseIP(brokerIPStr)
				if ipnet.Contains(kafkaBrokerIP) {
					return ipnet.IP.To4()
				}
			}
		}
	}

	panic("Can't determine proper IP address")
}

func (g *HTTPServerWithMetricsEndpoint) StartServer(addr string) {
	server := new(http.Server)
	g.errGroup.Go(func() error {
		server.Addr = addr
		http.Handle("/metrics", promhttp.Handler())
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Logger().Fatalf("Fatal http server error: %+v", err)
		}
		return nil
	})

	<-g.Ctx.Done()

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownRelease()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Logger().Fatalf("HTTP shutdown error: %+v", err)
	}

	if err := g.errGroup.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Logger().Fatalf("Some error occurred: %+v", err)
	}
}
