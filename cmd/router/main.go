// cmd/router is the traffic routing service.
// It reads region_health on a schedule, computes preferred + fallback routing
// decisions, and writes them to routing_decisions. Exposes /route for the
// current decision and /refresh to trigger an immediate re-evaluation.
package main

import (
	"context"
	"encoding/json"
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
	"github.com/resiliency/global/internal/router"
	"go.uber.org/zap"
)

var (
	routerDecisions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_decisions_total",
		Help: "Total routing decisions made.",
	})
	routerPreferredLatency = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "router_preferred_latency_ms",
		Help: "Latency of the current preferred region.",
	})
	routerFallbackLatency = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "router_fallback_latency_ms",
		Help: "Latency of the current fallback region.",
	})
	routerHealthyCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "router_healthy_region_count",
		Help: "Number of healthy regions at last decision.",
	})
	routerDownCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "router_down_region_count",
		Help: "Number of down regions at last decision.",
	})
	routerErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_errors_total",
		Help: "Total routing errors.",
	})
)

func init() {
	prometheus.MustRegister(
		routerDecisions, routerPreferredLatency, routerFallbackLatency,
		routerHealthyCount, routerDownCount, routerErrors,
	)
}

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg := config.MustLoad()
	log.Info("router starting",
		zap.String("region", cfg.Region),
		zap.Int("interval_s", cfg.HealthCheckInterval),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbClient, err := db.Connect(ctx, cfg.MongoURI)
	if err != nil {
		log.Fatal("router: mongo connect failed", zap.Error(err))
	}
	defer dbClient.Disconnect(context.Background())
	log.Info("router: connected to MongoDB Atlas")

	r := router.New(dbClient, 3600, log)

	go serveHTTP(cfg.RouterPort, dbClient, r, ctx, log)

	runRouting(ctx, r, log)

	ticker := time.NewTicker(time.Duration(cfg.HealthCheckInterval) * time.Second)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			runRouting(ctx, r, log)
		case sig := <-quit:
			log.Info("router: shutdown", zap.String("signal", sig.String()))
			return
		}
	}
}

func runRouting(ctx context.Context, r *router.Router, log *zap.Logger) {
	decision, err := r.Run(ctx)
	if err != nil {
		routerErrors.Inc()
		log.Error("router: decision error", zap.Error(err))
		return
	}
	routerDecisions.Inc()
	routerPreferredLatency.Set(float64(decision.PreferredLatencyMs))
	routerFallbackLatency.Set(float64(decision.FallbackLatencyMs))
	routerHealthyCount.Set(float64(decision.HealthyCount))
	routerDownCount.Set(float64(decision.DownCount))
}

func serveHTTP(port int, dbClient *db.Client, r *router.Router, ctx context.Context, log *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy","service":"router"}`)
	})

	// /route returns the current routing decision without triggering a refresh
	mux.HandleFunc("/route", func(w http.ResponseWriter, req *http.Request) {
		decision, err := r.Current(req.Context())
		if err != nil {
			http.Error(w, `{"error":"no routing decision available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(decision)
	})

	// /refresh triggers an immediate re-evaluation and returns the new decision
	mux.HandleFunc("/refresh", func(w http.ResponseWriter, req *http.Request) {
		decision, err := r.Run(req.Context())
		if err != nil {
			routerErrors.Inc()
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		routerDecisions.Inc()
		routerPreferredLatency.Set(float64(decision.PreferredLatencyMs))
		routerFallbackLatency.Set(float64(decision.FallbackLatencyMs))
		routerHealthyCount.Set(float64(decision.HealthyCount))
		routerDownCount.Set(float64(decision.DownCount))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(decision)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Info("router: HTTP listening", zap.String("addr", addr))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("router: HTTP error", zap.Error(err))
	}
}
