package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"goyard/internal/supervisor"
)

type Metrics struct {
	StartTime time.Time

	ConnectedSessions atomic.Int64
	MonitoredUsers    atomic.Int64
	GatewayConnected  atomic.Int64

	PresenceUpdatesTotal      atomic.Uint64
	MessagesInboundTotal      atomic.Uint64
	MessagesOutboundTotal     atomic.Uint64
	SlowConsumersDroppedTotal atomic.Uint64

	HTTPResponses2xx atomic.Uint64
	HTTPResponses3xx atomic.Uint64
	HTTPResponses4xx atomic.Uint64
	HTTPResponses5xx atomic.Uint64

	// Additional counters with labels aggregated by key
	GatewayReconnects    atomic.Uint64
	KVValidationFailures atomic.Uint64
	SocketClosesTotal    atomic.Uint64
}

var DefaultMetrics = &Metrics{
	StartTime: time.Now(),
}

func (m *Metrics) RecordHTTPResponse(code int) {
	switch {
	case code >= 200 && code < 300:
		m.HTTPResponses2xx.Add(1)
	case code >= 300 && code < 400:
		m.HTTPResponses3xx.Add(1)
	case code >= 400 && code < 500:
		m.HTTPResponses4xx.Add(1)
	case code >= 500:
		m.HTTPResponses5xx.Add(1)
	}
}

func (m *Metrics) PrometheusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var sb strings.Builder

		fmt.Fprintf(&sb, "# TYPE goyard_connected_sessions gauge\n")
		fmt.Fprintf(&sb, "goyard_connected_sessions %d\n", m.ConnectedSessions.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_monitored_users gauge\n")
		fmt.Fprintf(&sb, "goyard_monitored_users %d\n", m.MonitoredUsers.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_gateway_connected gauge\n")
		fmt.Fprintf(&sb, "goyard_gateway_connected %d\n", m.GatewayConnected.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_presence_updates_total counter\n")
		fmt.Fprintf(&sb, "goyard_presence_updates_total %d\n", m.PresenceUpdatesTotal.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_messages_inbound_total counter\n")
		fmt.Fprintf(&sb, "goyard_messages_inbound_total %d\n", m.MessagesInboundTotal.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_messages_outbound_total counter\n")
		fmt.Fprintf(&sb, "goyard_messages_outbound_total %d\n", m.MessagesOutboundTotal.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_slow_consumers_dropped_total counter\n")
		fmt.Fprintf(&sb, "goyard_slow_consumers_dropped_total %d\n", m.SlowConsumersDroppedTotal.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_http_responses_total counter\n")
		fmt.Fprintf(&sb, "goyard_http_responses_total{class=\"2xx\"} %d\n", m.HTTPResponses2xx.Load())
		fmt.Fprintf(&sb, "goyard_http_responses_total{class=\"3xx\"} %d\n", m.HTTPResponses3xx.Load())
		fmt.Fprintf(&sb, "goyard_http_responses_total{class=\"4xx\"} %d\n", m.HTTPResponses4xx.Load())
		fmt.Fprintf(&sb, "goyard_http_responses_total{class=\"5xx\"} %d\n", m.HTTPResponses5xx.Load())

		fmt.Fprintf(&sb, "# TYPE goyard_panics_total counter\n")
		fmt.Fprintf(&sb, "goyard_panics_total %d\n", supervisor.PanicsTotal())

		w.Write([]byte(sb.String()))
	}
}
