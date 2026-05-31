// cmd/transformer is the transformation service.
// It reads from the raw zone and writes to the processed zone on a schedule.
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
	"github.com/resiliency/global/internal/transform"
	"go.uber.org/zap"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg := config.MustLoad()
	log.Info("transformer starting",
		zap.String("region", cfg.Region),
		zap.Int("interval_s", cfg.TransformerInterval),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbClient, err := db.Connect(ctx, cfg.MongoURI)
	if err != nil {
		log.Fatal("mongo connect failed", zap.Error(err))
	}
	defer dbClient.Disconnect(context.Background())

	t := transform.New(dbClient, cfg.Region, log)

	if err := transform.EnsureIndexes(ctx, dbClient); err != nil {
		log.Warn("index creation warning", zap.Error(err))
	}

	go serveHealth(cfg.TransformerPort, dbClient, log)

	runTransform(ctx, t, log)

	ticker := time.NewTicker(time.Duration(cfg.TransformerInterval) * time.Second)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			runTransform(ctx, t, log)
		case sig := <-quit:
			log.Info("shutdown", zap.String("signal", sig.String()))
			return
		}
	}
}

func runTransform(ctx context.Context, t *transform.Transformer, log *zap.Logger) {
	start := time.Now()

	ghWritten, err := t.RunGitHub(ctx)
	if err != nil {
		log.Error("github transform error", zap.Error(err))
	} else {
		log.Info("github transform done", zap.Int("written", ghWritten))
	}

	owmWritten, err := t.RunWeather(ctx)
	if err != nil {
		log.Error("weather transform error", zap.Error(err))
	} else {
		log.Info("weather transform done", zap.Int("written", owmWritten))
	}

	log.Info("transform cycle complete", zap.Duration("elapsed", time.Since(start)))
}

func serveHealth(port int, dbClient *db.Client, log *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := dbClient.Ping(ctx); err != nil {
			http.Error(w, `{"status":"degraded"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy","service":"transformer"}`)
	})
	addr := fmt.Sprintf(":%d", port)
	log.Info("health endpoint", zap.String("addr", addr))
	http.ListenAndServe(addr, mux)
}
