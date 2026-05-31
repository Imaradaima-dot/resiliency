// cmd/healthcheck is the health-check service.
// It pings MongoDB Atlas every HEALTHCHECK_INTERVAL_SECONDS seconds,
// records latency and status in region_health, and exposes /health + /metrics.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/resiliency/global/internal/config"
	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/health"
	"go.uber.org/zap"
)

var (
	hcLatency = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "healthcheck_region_latency_ms",
		Help: "Observed latency to MongoDB Atlas per region.",
	}, []string{"region"})
	hcStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "healthcheck_region_status",
		Help: "Region health status: 1=healthy, 0.5=degraded, 0=down.",
	}, []string{"region"})
	hcCycles = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "healthcheck_cycles_total",
		Help: "Total health-check cycles completed.",
	})
	hcErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "healthcheck_errors_total",
		Help: "Total health-check write errors.",
	})
	hcRepLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "healthcheck_replication_lag_ms",
		Help: "Estimated replication lag in ms per region.",
	}, []string{"region"})
)

func init() {
	prometheus.MustRegister(hcLatency, hcStatus, hcCycles, hcErrors, hcRepLag)
}

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg := config.MustLoad()
	log.Info("healthcheck starting",
		zap.String("region", cfg.Region),
		zap.Int("interval_s", cfg.HealthCheckInterval),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbClient, err := db.Connect(ctx, cfg.MongoURI)
	if err != nil {
		log.Fatal("healthcheck: mongo connect failed", zap.Error(err))
	}
	defer dbClient.Disconnect(context.Background())
	log.Info("healthcheck: connected to MongoDB Atlas")

	checker := health.New(dbClient, cfg.Region, log)

	go serveHTTP(cfg.HealthCheckPort, log)

	runCheck(ctx, checker, cfg.Region, log)

	ticker := time.NewTicker(time.Duration(cfg.HealthCheckInterval) * time.Second)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			runCheck(ctx, checker, cfg.Region, log)
		case sig := <-quit:
			log.Info("healthcheck: shutdown", zap.String("signal", sig.String()))
			return
		}
	}
}

func runCheck(ctx context.Context, checker *health.Checker, region string, log *zap.Logger) {
	latencyMs, status, err := checker.Run(ctx)

	hcCycles.Inc()
	hcLatency.WithLabelValues(region).Set(float64(latencyMs))
	hcRepLag.WithLabelValues(region).Set(float64(latencyMs * 2))

	statusVal := 0.0
	switch status {
	case "healthy":
		statusVal = 1.0
	case "degraded":
		statusVal = 0.5
	}
	hcStatus.WithLabelValues(region).Set(statusVal)

	if err != nil {
		hcErrors.Inc()
		log.Error("healthcheck: cycle error", zap.Error(err))
	}
}

func serveHTTP(port int, log *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy","service":"healthcheck"}`)
	})
	addr := fmt.Sprintf(":%d", port)
	log.Info("healthcheck: HTTP listening", zap.String("addr", addr))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("healthcheck: HTTP error", zap.Error(err))
	}
}
