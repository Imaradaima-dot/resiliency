// Package observability centralizes Prometheus metrics used by the Phase 3 services.
package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CyclesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resiliency_service_cycles_total",
			Help: "Total service cycles by service and outcome.",
		},
		[]string{"service", "status"},
	)

	CycleDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "resiliency_service_cycle_duration_seconds",
			Help:    "Duration of service cycles in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service"},
	)

	RecordsProcessedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resiliency_records_processed_total",
			Help: "Records inserted, transformed, aggregated, or otherwise processed by service and record type.",
		},
		[]string{"service", "record_type"},
	)

	ErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resiliency_errors_total",
			Help: "Errors observed by service and error type.",
		},
		[]string{"service", "error_type"},
	)

	RegionLatencyMs = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "resiliency_region_latency_ms",
			Help: "Current measured or estimated region latency in milliseconds.",
		},
		[]string{"region"},
	)

	RegionReplicationLagMs = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "resiliency_region_replication_lag_ms",
			Help: "Current estimated replication lag in milliseconds.",
		},
		[]string{"region"},
	)

	RegionHealthStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "resiliency_region_health_status",
			Help: "Region health encoded as healthy=1, degraded=0.5, down=0.",
		},
		[]string{"region", "status"},
	)

	RoutingDecisionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resiliency_routing_decisions_total",
			Help: "Routing decisions produced by preferred and fallback region.",
		},
		[]string{"preferred_region", "fallback_region", "reason"},
	)

	RoutingPreferredLatencyMs = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "resiliency_routing_preferred_latency_ms",
			Help: "Latency of the current preferred region in milliseconds.",
		},
		[]string{"region"},
	)

	RoutingFallbackLatencyMs = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "resiliency_routing_fallback_latency_ms",
			Help: "Latency of the current fallback region in milliseconds.",
		},
		[]string{"region"},
	)

	RoutingRegionCounts = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "resiliency_routing_region_count",
			Help: "Number of candidate regions by routing health state.",
		},
		[]string{"state"},
	)

	APIRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resiliency_api_requests_total",
			Help: "HTTP requests served by service, path, method, and status code.",
		},
		[]string{"service", "path", "method", "status_code"},
	)

	APIRequestDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "resiliency_api_request_duration_seconds",
			Help:    "HTTP request duration in seconds by service, path, method, and status code.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "path", "method", "status_code"},
	)
)

func ObserveCycle(service string, started time.Time, success bool) {
	status := "success"
	if !success {
		status = "error"
	}
	CyclesTotal.WithLabelValues(service, status).Inc()
	CycleDurationSeconds.WithLabelValues(service).Observe(time.Since(started).Seconds())
}

func StatusValue(status string) float64 {
	switch status {
	case "healthy":
		return 1
	case "degraded":
		return 0.5
	default:
		return 0
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func HTTPMiddleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			status := strconv.Itoa(rec.status)
			path := r.URL.Path
			APIRequestsTotal.WithLabelValues(service, path, r.Method, status).Inc()
			APIRequestDurationSeconds.WithLabelValues(service, path, r.Method, status).Observe(time.Since(started).Seconds())
		})
	}
}
