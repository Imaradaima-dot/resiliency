// Package health implements the health-check service core logic.
// It pings MongoDB Atlas, measures round-trip latency, and writes a
// region_health document for this deployment's region on every cycle.
package health

import (
	"context"
	"fmt"
	"time"

	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// Checker measures MongoDB connectivity and writes region_health.
type Checker struct {
	client *db.Client
	region string
	log    *zap.Logger
}

// New creates a Checker.
func New(client *db.Client, region string, log *zap.Logger) *Checker {
	return &Checker{client: client, region: region, log: log}
}

// Run performs one health-check cycle for this region and writes to region_health.
// Returns the measured latency in milliseconds and the status string.
func (c *Checker) Run(ctx context.Context) (int64, string, error) {
	start := time.Now()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := c.client.Ping(pingCtx)
	latencyMs := time.Since(start).Milliseconds()

	status := models.StatusHealthy
	if err != nil {
		status = models.StatusDown
		latencyMs = 9999
		c.log.Warn("health-check: mongo ping failed",
			zap.String("region", c.region),
			zap.Error(err),
		)
	} else if latencyMs > 500 {
		status = models.StatusDegraded
	}

	// Replication lag is estimated from ping latency as a conservative proxy.
	// True replication lag requires Atlas Metrics API or optime comparison
	// (acknowledged caveat — requires M10+ Atlas cluster).
	replicationLagMs := latencyMs * 2

	now := time.Now().UTC()
	doc := &models.RegionHealth{
		Region:           c.region,
		Status:           status,
		LatencyMs:        latencyMs,
		ReplicationLagMs: replicationLagMs,
		WriteConcern:     "w:1 health heartbeat; majority used for raw/processed writes",
		ReadPreference:   "nearest",
		LastCheck:        now,
		CheckedAt:        now,
	}

	filter := bson.M{"region": c.region}
	update := bson.M{"$set": doc}
	_, writeErr := c.client.RegionHealth().UpdateOne(
		ctx, filter, update, options.Update().SetUpsert(true),
	)
	if writeErr != nil {
		return latencyMs, status, fmt.Errorf("health-check write: %w", writeErr)
	}

	c.log.Info("health-check: cycle complete",
		zap.String("region", c.region),
		zap.String("status", status),
		zap.Int64("latency_ms", latencyMs),
		zap.Int64("replication_lag_ms", replicationLagMs),
	)
	return latencyMs, status, nil
}
