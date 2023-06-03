package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type catcherMetrics struct {
	receivedEventsTotal *prometheus.CounterVec
	unprocEventsTotal   *prometheus.CounterVec
}

func newMetrics() *catcherMetrics {
	return &catcherMetrics{
		receivedEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "catcher",
			Name:      "received_events_total",
			Help:      "Total number events catcher received by customer.",
		}, []string{"customer_id"}),
		unprocEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "catcher",
			Name:      "unprocessed_events_total",
			Help:      "Total number of unprocessed events catcher received by customer.",
		}, []string{"customer_id", "code"}),
	}
}
