// Package db provides a MongoDB client wrapper and typed collection accessors.
// All connection management, write-concern tuning, and read-preference
// selection live here so individual services never touch driver internals.
package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// Collection names — defined once, referenced everywhere.
const (
	// Raw zone (resiliency_raw)
	CollRawGitHubEvents = "raw_github_events"
	CollRawWeather      = "raw_weather"

	// Processed zone (resiliency_processed)
	CollGitHubEventsFlat = "github_events_flat"
	CollWeatherFlat      = "weather_flat"

	// Serving zone (resiliency_serving)
	CollEventTypeCounts    = "event_type_counts"
	CollRegionalWeatherAgg = "regional_weather_agg"
	CollRegionHealth       = "region_health"
)

// Database names — one per zone.
const (
	DBRaw       = "resiliency_raw"
	DBProcessed = "resiliency_processed"
	DBServing   = "resiliency_serving"
)

// Client wraps the MongoDB driver client with convenience methods.
type Client struct {
	inner  *mongo.Client
	dbName string // legacy single-DB name, kept for backward compat
}

// Connect creates a new Client, pings Atlas to confirm connectivity, and
// returns. Call Disconnect when the process shuts down.
func Connect(ctx context.Context, uri string) (*Client, error) {
	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	inner, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("db.Connect: %w", err)
	}

	// Confirm the cluster is reachable before returning.
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := inner.Ping(pingCtx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("db.Connect ping: %w", err)
	}

	return &Client{inner: inner}, nil
}

// Disconnect cleanly closes the connection. Safe to defer from main().
func (c *Client) Disconnect(ctx context.Context) error {
	return c.inner.Disconnect(ctx)
}

// Ping checks whether the cluster is reachable from the nearest node.
// Used by the Health-Check service.
func (c *Client) Ping(ctx context.Context) error {
	return c.inner.Ping(ctx, readpref.Nearest())
}

// ---------------------------------------------------------------------------
// Collection accessors — one method per workload type.
// Each uses the write concern and read preference appropriate for that work.
// ---------------------------------------------------------------------------

// RawCollection returns a collection in the raw zone with majority writes and
// primary reads. Raw zone is the source of truth — we never lose a write here.
func (c *Client) RawCollection(name string) *mongo.Collection {
	opts := options.Collection().
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary())
	return c.inner.Database(DBRaw).Collection(name, opts)
}

// ProcessedCollection returns a collection in the processed zone with majority
// writes. Reads use the nearest node for analytics queries.
func (c *Client) ProcessedCollection(name string) *mongo.Collection {
	nearest, _ := readpref.New(readpref.NearestMode)
	opts := options.Collection().
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(nearest)
	return c.inner.Database(DBProcessed).Collection(name, opts)
}

// ServingCollection returns a collection in the serving zone with w:1 writes
// (aggregations are reproducible — speed matters more than durability here)
// and nearest reads for lowest dashboard latency.
func (c *Client) ServingCollection(name string) *mongo.Collection {
	nearest, _ := readpref.New(readpref.NearestMode)
	opts := options.Collection().
		SetWriteConcern(&writeconcern.WriteConcern{W: 1}).
		SetReadPreference(nearest)
	return c.inner.Database(DBServing).Collection(name, opts)
}

// HealthCollection returns the region_health collection with w:1 writes.
// Health-check heartbeats are high-frequency; losing one is acceptable.
func (c *Client) HealthCollection() *mongo.Collection {
	opts := options.Collection().
		SetWriteConcern(&writeconcern.WriteConcern{W: 1}).
		SetReadPreference(readpref.Primary())
	return c.inner.Database(DBServing).Collection(CollRegionHealth, opts)
}

// Raw zone shortcuts
func (c *Client) RawGitHubEvents() *mongo.Collection {
	return c.RawCollection(CollRawGitHubEvents)
}
func (c *Client) RawWeather() *mongo.Collection {
	return c.RawCollection(CollRawWeather)
}

// Processed zone shortcuts
func (c *Client) GitHubEventsFlat() *mongo.Collection {
	return c.ProcessedCollection(CollGitHubEventsFlat)
}
func (c *Client) WeatherFlat() *mongo.Collection {
	return c.ProcessedCollection(CollWeatherFlat)
}

// Serving zone shortcuts
func (c *Client) EventTypeCounts() *mongo.Collection {
	return c.ServingCollection(CollEventTypeCounts)
}
func (c *Client) RegionalWeatherAgg() *mongo.Collection {
	return c.ServingCollection(CollRegionalWeatherAgg)
}
func (c *Client) RegionHealth() *mongo.Collection {
	return c.HealthCollection()
}
