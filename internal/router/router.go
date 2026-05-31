// Package router implements the traffic routing decision logic.
// It reads region_health documents, selects the lowest-latency healthy region
// as preferred and the second-lowest as fallback, and writes the decision
// to the routing_decisions collection.
package router

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

const routingDecisionID = "current"

// RoutingDecision is written to the routing_decisions collection.
type RoutingDecision struct {
	DecisionID         string    `bson:"decision_id" json:"decision_id"`
	PreferredRegion    string    `bson:"preferred_region" json:"preferred_region"`
	PreferredStatus    string    `bson:"preferred_status" json:"preferred_status"`
	PreferredLatencyMs int64     `bson:"preferred_latency_ms" json:"preferred_latency_ms"`
	FallbackRegion     string    `bson:"fallback_region" json:"fallback_region"`
	FallbackStatus     string    `bson:"fallback_status" json:"fallback_status"`
	FallbackLatencyMs  int64     `bson:"fallback_latency_ms" json:"fallback_latency_ms"`
	Reason             string    `bson:"reason" json:"reason"`
	CandidateCount     int       `bson:"candidate_count" json:"candidate_count"`
	HealthyCount       int       `bson:"healthy_count" json:"healthy_count"`
	DegradedCount      int       `bson:"degraded_count" json:"degraded_count"`
	DownCount          int       `bson:"down_count" json:"down_count"`
	StaleCount         int       `bson:"stale_count" json:"stale_count"`
	MaxAgeSeconds      int       `bson:"max_age_seconds" json:"max_age_seconds"`
	DecidedAt          time.Time `bson:"decided_at" json:"decided_at"`
}

// Router reads region_health and computes routing decisions.
type Router struct {
	client        *db.Client
	maxAgeSeconds int
	log           *zap.Logger
}

// New creates a Router. maxAgeSeconds is how old a region_health doc can be
// before the region is treated as stale (default 3600 = 1 hour).
func New(client *db.Client, maxAgeSeconds int, log *zap.Logger) *Router {
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = 3600
	}
	return &Router{client: client, maxAgeSeconds: maxAgeSeconds, log: log}
}

// Run reads region_health, selects preferred + fallback, and upserts to routing_decisions.
func (r *Router) Run(ctx context.Context) (*RoutingDecision, error) {
	now := time.Now().UTC()
	staleThreshold := now.Add(-time.Duration(r.maxAgeSeconds) * time.Second)

	// Read all region_health docs
	cursor, err := r.client.RegionHealth().Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("router: read region_health: %w", err)
	}
	defer cursor.Close(ctx)

	var regions []models.RegionHealth
	if err := cursor.All(ctx, &regions); err != nil {
		return nil, fmt.Errorf("router: decode region_health: %w", err)
	}

	// Count by status, separate healthy candidates
	var (
		healthy  []models.RegionHealth
		degraded int
		down     int
		stale    int
	)

	for _, reg := range regions {
		if reg.CheckedAt.Before(staleThreshold) {
			stale++
			continue
		}
		switch reg.Status {
		case models.StatusHealthy:
			healthy = append(healthy, reg)
		case models.StatusDegraded:
			degraded++
		case models.StatusDown:
			down++
		}
	}

	// Sort healthy regions by latency ascending
	sort.Slice(healthy, func(i, j int) bool {
		return healthy[i].LatencyMs < healthy[j].LatencyMs
	})

	decision := &RoutingDecision{
		DecisionID:     routingDecisionID,
		CandidateCount: len(regions),
		HealthyCount:   len(healthy),
		DegradedCount:  degraded,
		DownCount:      down,
		StaleCount:     stale,
		MaxAgeSeconds:  r.maxAgeSeconds,
		DecidedAt:      now,
	}

	switch {
	case len(healthy) >= 2:
		decision.PreferredRegion = healthy[0].Region
		decision.PreferredStatus = healthy[0].Status
		decision.PreferredLatencyMs = healthy[0].LatencyMs
		decision.FallbackRegion = healthy[1].Region
		decision.FallbackStatus = healthy[1].Status
		decision.FallbackLatencyMs = healthy[1].LatencyMs
		decision.Reason = "selected lowest-latency healthy region with healthy fallback"

	case len(healthy) == 1:
		decision.PreferredRegion = healthy[0].Region
		decision.PreferredStatus = healthy[0].Status
		decision.PreferredLatencyMs = healthy[0].LatencyMs
		decision.FallbackRegion = "none"
		decision.FallbackStatus = "unavailable"
		decision.FallbackLatencyMs = 0
		decision.Reason = "only one healthy region available; no fallback"

	default:
		decision.PreferredRegion = "none"
		decision.PreferredStatus = "unavailable"
		decision.FallbackRegion = "none"
		decision.FallbackStatus = "unavailable"
		decision.Reason = "no healthy regions available"
	}

	// Upsert decision keyed on decision_id = "current"
	// Use routing_decisions collection
	routingColl := r.client.ServingCollection("routing_decisions")
	filter := bson.M{"decision_id": routingDecisionID}
	update := bson.M{"$set": decision}
	_, err = routingColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return decision, fmt.Errorf("router: write decision: %w", err)
	}

	r.log.Info("router: decision written",
		zap.String("preferred", decision.PreferredRegion),
		zap.String("fallback", decision.FallbackRegion),
		zap.Int64("preferred_latency_ms", decision.PreferredLatencyMs),
		zap.String("reason", decision.Reason),
	)
	return decision, nil
}

// Current reads the latest routing decision from routing_decisions.
func (r *Router) Current(ctx context.Context) (*RoutingDecision, error) {
	routingColl := r.client.ServingCollection("routing_decisions")
	result := routingColl.FindOne(ctx, bson.M{"decision_id": routingDecisionID})
	if result.Err() != nil {
		return nil, fmt.Errorf("router: read current: %w", result.Err())
	}
	var d RoutingDecision
	if err := result.Decode(&d); err != nil {
		return nil, fmt.Errorf("router: decode current: %w", err)
	}
	return &d, nil
}
