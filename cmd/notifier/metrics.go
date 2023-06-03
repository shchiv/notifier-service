package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type notifierMetrics struct {
	postedEventsTotal         *prometheus.CounterVec
	sentEventsTotal           *prometheus.CounterVec
	failedEventsTotal         *prometheus.CounterVec
	retriedEventsTotal        *prometheus.CounterVec
	postedEventsByThreadTotal *prometheus.CounterVec
	failedEventsByThreadTotal *prometheus.CounterVec
	requestDurationSeconds    *prometheus.HistogramVec
	notificationLag           *prometheus.HistogramVec
	deliveryLag               *prometheus.HistogramVec
}

func newMetrics() *notifierMetrics {
	return &notifierMetrics{
		postedEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "notifier",
			Name:      "posted_events_total",
			Help:      "Total number of events posted by notifier.",
		}, []string{"customer_id", "mode"}),

		sentEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "notifier",
			Name:      "sent_events_total",
			Help:      "Total number of events sent by notifier.",
		}, []string{"customer_id", "mode"}),

		retriedEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "notifier",
			Name:      "retried_events_total",
			Help:      "Total number of events which were not sent.",
		}, []string{"customer_id", "mode"}),

		failedEventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "notifier",
			Name:      "failed_events_total",
			Help:      "Total number of events which were not sent.",
		}, []string{"customer_id", "status"}),

		postedEventsByThreadTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "notifier",
			Name:      "thread_posted_events_total",
			Help:      "Total number of events which were not sent.",
		}, []string{"customer_id", "thread_id"}),

		failedEventsByThreadTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "notifier",
			Name:      "thread_failed_events_total",
			Help:      "Total number of events which were not sent.",
		}, []string{"customer_id", "thread_id"}),

		requestDurationSeconds: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: "notifier",
			Name:      "request_duration_seconds",
			Help:      "Duration of the request.",
			Buckets:   []float64{.005, .01, .02, 0.04, .06, 0.08, .1, 0.15, .25, 0.4, .6, .8, 1, 1.5, 2, 3, 5},
		}, []string{"customer_id"}),

		notificationLag: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: "notifier",
			Name:      "notification_lag",
			Help:      "Event notification lag (ms) represents the requestDurationSeconds from the event occurred till when the notifier reads the event from DB",
			Buckets:   []float64{.005, .01, .02, 0.04, .06, 0.08, .1, 0.15, .25, 0.4, .6, .8, 1, 1.5, 2, 3, 5},
		}, []string{"customer_id", "thread_id"}),

		deliveryLag: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: "notifier",
			Name:      "delivery_lag",
			Help:      "Event notification lag (ms) represents the requestDurationSeconds from the event occurred till when the notifier received response from customer (catcher)",
			Buckets:   []float64{.005, .01, .02, 0.04, .06, 0.08, .1, 0.15, .25, 0.4, .6, .8, 1, 1.5, 2, 3, 5},
		}, []string{"customer_id", "thread_id", "mode"}),
	}
}
