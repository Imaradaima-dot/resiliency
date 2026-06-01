// Package health implements the Health-Check service.
//
// It pings every MongoDB region node independently on each tick, so the
// serving zone always has a fresh health record per region (RPT-04).
// The /readyz endpoint returns 503 only if ALL regions are down.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	StatusHealthy  = "healthy"
	StatusDegraded = "degraded" // latency > degradedThresholdMS
	StatusDown     = "down"     // ping failed

	degradedThresholdMS = 200.0 // ms — lower threshold for local docker network
)

// regionState holds the latest health result for one region.
type regionState struct {
	Status    string
	LatencyMS float64
	LastCheck time.Time
}

// Checker runs periodic per-region health checks and serves HTTP probes.
type Checker struct {
	dbClient *db.Client
	cfg      *config.Config
	mu       sync.RWMutex
	states   map[string]*regionState // region → latest state
}

// NewChecker returns a ready-to-use Checker.
func NewChecker(dbClient *db.Client, cfg *config.Config) *Checker {
	return &Checker{
		dbClient: dbClient,
		cfg:      cfg,
		states:   make(map[string]*regionState),
	}
}

// Run starts the background health-check loop (blocking until ctx cancelled).
func (c *Checker) Run(ctx context.Context) {
	fmt.Printf("[health] starting  interval=%s  regions=%d\n",
		c.cfg.HealthInterval, len(c.cfg.RegionNodes))

	c.runAllChecks(ctx)

	ticker := time.NewTicker(c.cfg.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[health] checker stopped")
			return
		case <-ticker.C:
			c.runAllChecks(ctx)
		}
	}
}

// runAllChecks pings every region node concurrently using one goroutine each.
func (c *Checker) runAllChecks(ctx context.Context) {
	var wg sync.WaitGroup
	for _, node := range c.cfg.RegionNodes {
		wg.Add(1)
		go func(n config.RegionNode) {
			defer wg.Done()
			c.checkRegion(ctx, n)
		}(node)
	}
	wg.Wait()
}

// checkRegion pings one region node and upserts its health record.
func (c *Checker) checkRegion(ctx context.Context, node config.RegionNode) {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Open a direct connection to this specific node.
	nodeClient, err := db.ConnectDirect(pingCtx, c.cfg, node)
	if err != nil {
		c.recordState(ctx, node.Region, StatusDown, 0, err)
		return
	}
	defer nodeClient.Disconnect(context.Background())

	start := time.Now()
	latencyMS, pingErr := nodeClient.Ping(pingCtx)
	_ = start

	status := StatusHealthy
	if pingErr != nil {
		status = StatusDown
		latencyMS = 0
	} else if latencyMS > degradedThresholdMS {
		status = StatusDegraded
	}

	fmt.Printf("[health] %-10s  %-10s  %.1fms\n", node.Region, status, latencyMS)
	c.recordState(ctx, node.Region, status, latencyMS, pingErr)
}

// recordState updates in-memory state and upserts the SINGLE health doc
// per region into the serving zone.
//
// Answer to Q2: the collection holds exactly ONE document per region,
// keyed on "region". Every check does a $set upsert:
//
//	filter  → { region: "us-east" }           — find the existing doc
//	$set    → { status, latency_ms, ... }      — overwrite all fields
//	$setOnInsert → { created_at }              — only written on first insert
//	upsert:true  → insert if missing, update if found
//
// Result: the collection always has 4 docs (one per region), never grows.
// Dashboards always read the latest snapshot with a simple Find().
func (c *Checker) recordState(ctx context.Context, region, status string, latencyMS float64, _ error) {
	now := time.Now().UTC()

	c.mu.Lock()
	c.states[region] = &regionState{
		Status:    status,
		LatencyMS: latencyMS,
		LastCheck: now,
	}
	c.mu.Unlock()

	upsertCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// filter: the one document for this region
	filter := bson.D{{Key: "region", Value: region}}

	// $set: fields that change on every ping
	setFields := bson.D{
		{Key: "status", Value: status},
		{Key: "latency_ms", Value: latencyMS},
		{Key: "write_concern", Value: c.cfg.WriteConcern},
		{Key: "last_check", Value: now},
	}

	// $setOnInsert: only written when the document is created for the first time
	setOnInsert := bson.D{
		{Key: "region", Value: region},
		{Key: "created_at", Value: now},
	}

	update := bson.D{
		{Key: "$set", Value: setFields},
		{Key: "$setOnInsert", Value: setOnInsert},
	}

	res, err := c.dbClient.RegionHealth().UpdateOne(
		upsertCtx, filter, update, options.Update().SetUpsert(true),
	)
	if err != nil {
		fmt.Printf("[health] WARNING: upsert failed for %s: %v\n", region, err)
		return
	}

	if res.UpsertedCount > 0 {
		fmt.Printf("[health] created new region_health doc for %s\n", region)
	} else {
		fmt.Printf("[health] updated region_health for %-10s  %-10s  %.1fms\n", region, status, latencyMS)
	}
}

// =============================================================================
// HTTP handlers
// =============================================================================

// HealthResponse is the JSON body returned by health endpoints.
type HealthResponse struct {
	Status    string                   `json:"status"`
	Timestamp time.Time                `json:"timestamp"`
	Regions   map[string]*RegionDetail `json:"regions,omitempty"`
}

// RegionDetail carries per-region health detail for /status.
type RegionDetail struct {
	Status    string    `json:"status"`
	LatencyMS float64   `json:"latency_ms"`
	LastCheck time.Time `json:"last_check"`
}

// LivezHandler — always 200 while process is running (K8s liveness probe).
func (c *Checker) LivezHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    "alive",
		Timestamp: time.Now().UTC(),
	})
}

// ReadyzHandler — 200 if at least one region is healthy, 503 if all are down.
func (c *Checker) ReadyzHandler(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	states := c.states
	c.mu.RUnlock()

	if len(states) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(HealthResponse{Status: "initializing", Timestamp: time.Now().UTC()})
		return
	}

	// Overall status: healthy > degraded > down
	// If at least one region is up, we're ready (active-active: other regions absorb traffic).
	overall := StatusDown
	regions := make(map[string]*RegionDetail, len(states))
	for region, s := range states {
		regions[region] = &RegionDetail{
			Status:    s.Status,
			LatencyMS: s.LatencyMS,
			LastCheck: s.LastCheck,
		}
		if s.Status == StatusHealthy {
			overall = StatusHealthy
		} else if s.Status == StatusDegraded && overall == StatusDown {
			overall = StatusDegraded
		}
	}

	code := http.StatusOK
	if overall == StatusDown {
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    overall,
		Timestamp: time.Now().UTC(),
		Regions:   regions,
	})
}

// StatusHandler — returns full per-region health detail for dashboards.
func (c *Checker) StatusHandler(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	states := c.states
	c.mu.RUnlock()

	regions := make(map[string]*RegionDetail, len(states))
	for region, s := range states {
		regions[region] = &RegionDetail{
			Status:    s.Status,
			LatencyMS: s.LatencyMS,
			LastCheck: s.LastCheck,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC(),
		Regions:   regions,
	})
}

// APIRegistrar is satisfied by api.Handler — avoids an import cycle.
type APIRegistrar interface {
	Register(mux *http.ServeMux)
}

// ServeHTTP starts the HTTP server for health probes and API routes.
// Pass the api.Handler as apiReg so Grafana endpoints are served on the
// same port without needing a second service.
func (c *Checker) ServeHTTP(ctx context.Context, apiReg APIRegistrar) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", c.LivezHandler)
	mux.HandleFunc("/readyz", c.ReadyzHandler)
	mux.HandleFunc("/status", c.StatusHandler)
	apiReg.Register(mux) // mounts /api/event-types, /api/weather, /api/region-health, /api/summary

	srv := &http.Server{
		Addr:    ":" + c.cfg.HealthPort,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	fmt.Printf("[health] HTTP server :%s  GET /livez /readyz /status\n", c.cfg.HealthPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
