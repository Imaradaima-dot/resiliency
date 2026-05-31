// cmd/aggregator is the aggregation service.
// It reads from the processed zone and writes serving-zone aggregations
// on a configurable schedule. Exposes /health and /metrics.
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
	"github.com/resiliency/global/internal/aggregator"
	"github.com/resiliency/global/internal/config"
	"github.com/resiliency/global/internal/db"
	"go.uber.org/zap"
)

var (
	aggCycles = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "aggregator_cycles_total",
		Help: "Total aggregation cycles completed.",
	}, []string{"type"})
	aggWritten = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "aggregator_records_written_total",
		Help: "Total records written to the serving zone.",
	}, []string{"collection"})
	aggDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "aggregator_cycle_duration_seconds",
		Help: "Duration of the last aggregation cycle.",
	}, []string{"type"})
	aggErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "aggregator_errors_total",
		Help: "Total aggregation errors.",
	}, []string{"type"})
)

func init() {
	prometheus.MustRegister(aggCycles, aggWritten, aggDuration, aggErrors)
}

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg := config.MustLoad()
	log.Info("aggregator starting",
		zap.String("region", cfg.Region),
		zap.Int("interval_s", cfg.AggregatorInterval),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbClient, err := db.Connect(ctx, cfg.MongoURI)
	if err != nil {
		log.Fatal("mongo connect failed", zap.Error(err))
	}
	defer dbClient.Disconnect(context.Background())
	log.Info("aggregator: connected to MongoDB Atlas")

	agg := aggregator.New(dbClient, cfg.Region, log)

	go serveHTTP(8085, log)

	runAggregation(ctx, agg, log)

	ticker := time.NewTicker(time.Duration(cfg.AggregatorInterval) * time.Second)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			runAggregation(ctx, agg, log)
		case sig := <-quit:
			log.Info("aggregator: shutdown", zap.String("signal", sig.String()))
			return
		}
	}
}

func runAggregation(ctx context.Context, agg *aggregator.Aggregator, log *zap.Logger) {
	// Event type counts
	start := time.Now()
	written, err := agg.RunEventTypes(ctx)
	elapsed := time.Since(start).Seconds()
	aggDuration.WithLabelValues("event_types").Set(elapsed)
	if err != nil {
		aggErrors.WithLabelValues("event_types").Inc()
		log.Error("aggregator: event types error", zap.Error(err))
	} else {
		aggCycles.WithLabelValues("event_types").Inc()
		aggWritten.WithLabelValues("event_type_counts").Add(float64(written))
		log.Info("aggregator: event types done", zap.Int("written", written))
	}

	// Regional weather aggregation
	start = time.Now()
	written, err = agg.RunWeatherAgg(ctx)
	elapsed = time.Since(start).Seconds()
	aggDuration.WithLabelValues("weather_agg").Set(elapsed)
	if err != nil {
		aggErrors.WithLabelValues("weather_agg").Inc()
		log.Error("aggregator: weather agg error", zap.Error(err))
	} else {
		aggCycles.WithLabelValues("weather_agg").Inc()
		aggWritten.WithLabelValues("regional_weather_agg").Add(float64(written))
		log.Info("aggregator: weather agg done", zap.Int("written", written))
	}
}

func serveHTTP(port int, log *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy","service":"aggregator"}`)
	})
	addr := fmt.Sprintf(":%d", port)
	log.Info("aggregator: HTTP listening", zap.String("addr", addr))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("aggregator: HTTP error", zap.Error(err))
	}
}
