// cmd/healthcheck/main.go — Health-Check + REST API Service
//
// HTTP routes:
//   /livez               Kubernetes liveness probe
//   /readyz              Kubernetes readiness probe (503 if all regions down)
//   /status              Per-region health JSON (used by Grafana)
//   /api/event-types     Serving zone RPT-01 → Grafana
//   /api/weather         Serving zone RPT-03 → Grafana
//   /api/region-health   Serving zone RPT-04 → Grafana
//   /api/summary         KPI snapshot        → Grafana overview panel
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/api"
	"github.com/resiliency/global-service/internal/db"
	"github.com/resiliency/global-service/internal/health"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbClient, err := db.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer dbClient.Disconnect(context.Background())

	// Ensure indexes exist (idempotent — safe to call on every startup).
	if err := dbClient.EnsureIndexes(ctx); err != nil {
		log.Printf("WARNING: ensureIndexes: %v", err)
	}

	checker := health.NewChecker(dbClient, cfg)
	apiHandler := api.New(dbClient)

	// Background health-check loop — pings every region node every HealthInterval.
	go checker.Run(ctx)

	// HTTP server — registers both health probes and API routes.
	fmt.Println("[healthcheck] service starting")
	if err := checker.ServeHTTP(ctx, apiHandler); err != nil {
		log.Fatalf("http: %v", err)
	}
}
