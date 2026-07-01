package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	WebhookEventsReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meta_mcp_webhook_events_received_total",
			Help: "Total number of webhook events received from Meta.",
		},
		[]string{"field", "status"},
	)

	MessagesDispatched = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meta_mcp_messages_dispatched_total",
			Help: "Total number of messages dispatched via NATS queue.",
		},
		[]string{"status", "channel", "message_type"},
	)

	ComplianceChecks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meta_mcp_compliance_checks_total",
			Help: "Total number of message compliance evaluations.",
		},
		[]string{"allowed", "reason_code"},
	)

	ComplianceLatency = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "meta_mcp_compliance_check_duration_seconds",
			Help:    "Latency of check_compliance operations in seconds.",
			Buckets: []float64{0.001, 0.005, 0.010, 0.025, 0.050, 0.100, 0.250},
		},
	)

	RateLimitHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "meta_mcp_rate_limit_hits_total",
			Help: "Total number of rate limit check failures.",
		},
		[]string{"phone_number_id"},
	)
)

func init() {
	// Register metrics with Prometheus default registry
	prometheus.MustRegister(WebhookEventsReceived)
	prometheus.MustRegister(MessagesDispatched)
	prometheus.MustRegister(ComplianceChecks)
	prometheus.MustRegister(ComplianceLatency)
	prometheus.MustRegister(RateLimitHits)
}

func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
