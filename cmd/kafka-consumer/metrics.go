package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type consumerMetrics struct {
	ingestedEventsTotal *prometheus.CounterVec
}

func newMetrics() *consumerMetrics {
	return &consumerMetrics{ingestedEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "consumer",
		Name:      "ingested_events_total",
		Help:      "Total number of events ingested by customer.",
	},
		[]string{"customer_id"},
	)}
}
