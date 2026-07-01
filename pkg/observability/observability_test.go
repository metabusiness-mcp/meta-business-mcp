package observability

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsHandler(t *testing.T) {
	// Increment labeled metrics to ensure they are initialized in Prometheus registry
	WebhookEventsReceived.WithLabelValues("messages", "processing").Inc()
	MessagesDispatched.WithLabelValues("sent", "whatsapp", "service").Inc()
	ComplianceChecks.WithLabelValues("true", "ALLOWED").Inc()
	RateLimitHits.WithLabelValues("mock-phone-id").Inc()

	handler := MetricsHandler()
	if handler == nil {
		t.Fatalf("MetricsHandler returned nil")
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	metricsStr := buf.String()

	// Verify our metric names exist in the prometheus output
	expectedMetrics := []string{
		"meta_mcp_webhook_events_received_total",
		"meta_mcp_messages_dispatched_total",
		"meta_mcp_compliance_checks_total",
		"meta_mcp_compliance_check_duration_seconds",
		"meta_mcp_rate_limit_hits_total",
	}

	for _, m := range expectedMetrics {
		if !bytes.Contains(buf.Bytes(), []byte(m)) {
			t.Errorf("Expected metric '%s' to be registered, but it was not found in: %s", m, metricsStr)
		}
	}
}
