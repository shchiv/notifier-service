package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type producerMetrics struct {
	enqueuedEventsTotal *prometheus.CounterVec
}

func newMetrics() *producerMetrics {
	return &producerMetrics{enqueuedEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "producer",
		Name:      "enqueued_events_total",
		Help:      "Total number of events enqueued by customer.",
	},
		[]string{"customer_id"},
	)}
}
