// Package metrics defines the Prometheus metrics for the MCP server and the
// adapters that feed client-go's request metrics into the default registry.
// Metrics are served on a separate, unauthenticated port (see main); labels are
// deliberately low-cardinality (never namespace/object/user).
package metrics

import (
	"context"
	"net/url"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	clientmetrics "k8s.io/client-go/tools/metrics"
)

var (
	toolCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kmcp_tool_calls_total",
		Help: "MCP tool calls by tool, target cluster and result (ok|error|forbidden|blocked).",
	}, []string{"tool", "cluster", "result"})

	toolDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kmcp_tool_call_duration_seconds",
		Help:    "MCP tool call latency by tool and cluster.",
		Buckets: prometheus.DefBuckets,
	}, []string{"tool", "cluster"})

	authRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kmcp_auth_requests_total",
		Help: "Agent authentication attempts by method (static|oidc|none) and result (allow|deny).",
	}, []string{"method", "result"})

	clusterUp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kmcp_cluster_up",
		Help: "Cluster apiserver reachability (1 = reachable, 0 = not), by cluster.",
	}, []string{"cluster"})

	writesBlocked = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kmcp_writes_blocked_total",
		Help: "Mutating tool calls blocked by the read-only guard, by cluster and reason.",
	}, []string{"cluster", "reason"})

	buildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kmcp_build_info",
		Help: "Build information; constant 1.",
	}, []string{"version", "goversion"})

	restRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rest_client_requests_total",
		Help: "Kubernetes API requests by host, status code and method (client-go).",
	}, []string{"host", "code", "method"})

	restLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "rest_client_request_duration_seconds",
		Help:    "Kubernetes API request latency by host and verb (client-go).",
		Buckets: prometheus.DefBuckets,
	}, []string{"host", "verb"})
)

// RecordTool records a tool call outcome and latency.
func RecordTool(tool, cluster, result string, d time.Duration) {
	if cluster == "" {
		cluster = "-"
	}
	toolCalls.WithLabelValues(tool, cluster, result).Inc()
	toolDuration.WithLabelValues(tool, cluster).Observe(d.Seconds())
}

// RecordAuth records an agent-authentication outcome.
func RecordAuth(method, result string) { authRequests.WithLabelValues(method, result).Inc() }

// RecordWriteBlocked records a mutation blocked by the read-only guard.
func RecordWriteBlocked(cluster, reason string) { writesBlocked.WithLabelValues(cluster, reason).Inc() }

// SetClusterUp sets the reachability gauge for a cluster.
func SetClusterUp(cluster string, up bool) {
	v := 0.0
	if up {
		v = 1
	}
	clusterUp.WithLabelValues(cluster).Set(v)
}

// SetBuildInfo publishes the build-info gauge.
func SetBuildInfo(version string) {
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)
}

// --- client-go request metrics adapters ---

type resultAdapter struct{}

func (resultAdapter) Increment(_ context.Context, code, method, host string) {
	restRequests.WithLabelValues(host, code, method).Inc()
}

type latencyAdapter struct{}

func (latencyAdapter) Observe(_ context.Context, verb string, u url.URL, latency time.Duration) {
	restLatency.WithLabelValues(u.Host, verb).Observe(latency.Seconds())
}

// RegisterClientGo plugs the adapters into client-go. Call once at startup.
func RegisterClientGo() {
	clientmetrics.Register(clientmetrics.RegisterOpts{
		RequestResult:  resultAdapter{},
		RequestLatency: latencyAdapter{},
	})
}
