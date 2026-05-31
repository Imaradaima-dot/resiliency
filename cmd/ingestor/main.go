// cmd/ingestor is the ingestion service. It runs the GitHub and OpenWeatherMap
// collectors on a configurable schedule and writes results to the raw zone.
// In production this binary runs as a Kubernetes Deployment (always-on) or
// CronJob (scheduled). Locally it loops with a ticker.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/resiliency/global/internal/config"
	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/github"
	"github.com/resiliency/global/internal/weather"
	"go.uber.org/zap"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg := config.MustLoad()

	log.Info("ingestor starting",
		zap.String("region", cfg.Region),
		zap.Int("interval_s", cfg.IngestorInterval),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to MongoDB Atlas
	dbClient, err := db.Connect(ctx, cfg.MongoURI)
	if err != nil {
		log.Fatal("failed to connect to MongoDB", zap.Error(err))
	}
	defer dbClient.Disconnect(context.Background())
	log.Info("connected to MongoDB Atlas")

	// Ensure indexes exist (idempotent)
	if err := github.EnsureIndexes(ctx, dbClient); err != nil {
		log.Warn("github index creation warning", zap.Error(err))
	}
	if err := weather.EnsureIndexes(ctx, dbClient); err != nil {
		log.Warn("weather index creation warning", zap.Error(err))
	}

	// Build collectors
	ghCollector := github.NewCollector(dbClient, cfg.GitHubToken, cfg.Region, log)
	owmCollector := weather.NewCollector(dbClient, cfg.OWMAPIKey, cfg.Region, log)

	// Health endpoint so k8s readiness probes work
	go serveHealth(cfg.IngestorPort, dbClient, log)

	// Run immediately on startup, then on the ticker
	runCollectors(ctx, ghCollector, owmCollector, log)

	ticker := time.NewTicker(time.Duration(cfg.IngestorInterval) * time.Second)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			runCollectors(ctx, ghCollector, owmCollector, log)
		case sig := <-quit:
			log.Info("shutdown signal received", zap.String("signal", sig.String()))
			cancel()
			return
		}
	}
}

func runCollectors(ctx context.Context, gh *github.Collector, owm *weather.Collector, log *zap.Logger) {
	start := time.Now()

	ghWritten, ghErr := gh.Run(ctx)
	if ghErr != nil {
		log.Error("github collector error", zap.Error(ghErr))
	} else {
		log.Info("github collection done", zap.Int("new_events", ghWritten))
	}

	owmWritten, owmErr := owm.Run(ctx)
	if owmErr != nil {
		log.Error("weather collector error", zap.Error(owmErr))
	} else {
		log.Info("weather collection done", zap.Int("new_records", owmWritten))
	}

	log.Info("collection cycle complete",
		zap.Duration("elapsed", time.Since(start)),
	)
}

func serveHealth(port int, dbClient *db.Client, log *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := dbClient.Ping(ctx); err != nil {
			log.Warn("health check: mongo ping failed", zap.Error(err))
			http.Error(w, `{"status":"degraded"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy","service":"ingestor"}`)
	})
	addr := fmt.Sprintf(":%d", port)
	log.Info("health endpoint listening", zap.String("addr", addr))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("health server error", zap.Error(err))
	}
}
