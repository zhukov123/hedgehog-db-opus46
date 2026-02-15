// Package metrics defines and registers all Prometheus metrics for HedgehogDB.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Latency histogram buckets (seconds).
var defBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// ---- HTTP metrics ----

// HTTPRequestsTotal counts all HTTP requests.
var HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hedgehog_http_requests_total",
	Help: "Total HTTP requests.",
}, []string{"method", "operation", "status", "table"})

// HTTPRequestDuration records HTTP request latency.
var HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "hedgehog_http_request_duration_seconds",
	Help:    "HTTP request latency in seconds.",
	Buckets: defBuckets,
}, []string{"method", "operation", "status", "table"})

// ---- Replication metrics ----

// ReplicationSendTotal counts outgoing replication attempts.
var ReplicationSendTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hedgehog_replication_send_total",
	Help: "Outgoing replicate attempts by op, target node, and result.",
}, []string{"op", "target_node", "result"})

// ReplicationHintedHandoffPending is the current pending hints per target node.
var ReplicationHintedHandoffPending = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "hedgehog_replication_hinted_handoff_pending",
	Help: "Current pending hinted-handoff count per target node.",
}, []string{"target_node"})

// ReplicationHintReplayTotal counts hint replay runs.
var ReplicationHintReplayTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hedgehog_replication_hint_replay_total",
	Help: "Hint replay runs by target node and result.",
}, []string{"target_node", "result"})

// ReplicationHintReplayOpsTotal counts individual hint operations replayed.
var ReplicationHintReplayOpsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hedgehog_replication_hint_replay_ops_total",
	Help: "Individual hint ops replayed by target node and op.",
}, []string{"target_node", "op"})

// ReplicationReceivedTotal counts incoming replicate requests handled.
var ReplicationReceivedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hedgehog_replication_received_total",
	Help: "Incoming replicate requests by op and result.",
}, []string{"op", "result"})

// ReplicationReceivedDuration records incoming replicate request latency.
var ReplicationReceivedDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "hedgehog_replication_received_duration_seconds",
	Help:    "Latency of incoming replicate requests.",
	Buckets: defBuckets,
}, []string{"op", "result"})

// registry is the custom prometheus registry (avoids default Go collector noise).
var registry = prometheus.NewRegistry()

func init() {
	// HTTP
	registry.MustRegister(HTTPRequestsTotal)
	registry.MustRegister(HTTPRequestDuration)
	// Replication
	registry.MustRegister(ReplicationSendTotal)
	registry.MustRegister(ReplicationHintedHandoffPending)
	registry.MustRegister(ReplicationHintReplayTotal)
	registry.MustRegister(ReplicationHintReplayOpsTotal)
	registry.MustRegister(ReplicationReceivedTotal)
	registry.MustRegister(ReplicationReceivedDuration)
}

// Handler returns an http.Handler that serves the /metrics endpoint.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// UpdateHintedHandoffGauges sets the pending-hints gauge from a map[nodeID]count.
// Caller passes handoff.PendingCounts() so this package has no cluster dependency.
func UpdateHintedHandoffGauges(counts map[string]int) {
	for nodeID, count := range counts {
		ReplicationHintedHandoffPending.WithLabelValues(nodeID).Set(float64(count))
	}
}
