// Package db manages all MongoDB connectivity for the Global Service Resiliency system.
//
// It exposes a single Client wrapper that:
//   - Connects to MongoDB Atlas with configurable write concern and read preference
//   - Returns typed collection handles for each of the 7 collections across 3 zones
//   - Implements ping-based health checking used by the Health-Check service
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/resiliency/global-service/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// Collection names across all three zones.
const (
	// Raw zone
	ColRawGitHubEvents = "raw_github_events"
	ColRawWeather      = "raw_weather"

	// Processed zone
	ColGitHubEventsFlat = "github_events_flat"
	ColWeatherFlat      = "weather_flat"

	// Serving zone
	ColEventTypeCounts     = "event_type_counts"
	ColRegionalWeatherAgg  = "regional_weather_agg"
	ColRegionHealth        = "region_health"
)

// Client wraps a *mongo.Client and provides typed accessors for all collections.
type Client struct {
	mc  *mongo.Client
	cfg *config.Config
}

// Connect establishes a connection to MongoDB using the URI and consistency
// settings from cfg. Call Disconnect when the application exits.
func Connect(ctx context.Context, cfg *config.Config) (*Client, error) {
	wc, err := parseWriteConcern(cfg.WriteConcern)
	if err != nil {
		return nil, fmt.Errorf("db: invalid write concern %q: %w", cfg.WriteConcern, err)
	}

	rp, err := parseReadPref(cfg.ReadPref)
	if err != nil {
		return nil, fmt.Errorf("db: invalid read preference %q: %w", cfg.ReadPref, err)
	}

	opts := options.Client().
		ApplyURI(cfg.MongoURI).
		SetWriteConcern(wc).
		SetReadPreference(rp).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	mc, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	// Verify the connection is live before returning.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := mc.Ping(pingCtx, rp); err != nil {
		return nil, fmt.Errorf("db: ping failed: %w", err)
	}

	fmt.Printf("[db] Connected to MongoDB  write_concern=%s  read_pref=%s\n",
		cfg.WriteConcern, cfg.ReadPref)

	return &Client{mc: mc, cfg: cfg}, nil
}

// Disconnect closes all connections and should be deferred after Connect.
func (c *Client) Disconnect(ctx context.Context) error {
	return c.mc.Disconnect(ctx)
}

// ConnectDirect opens a lightweight direct connection to a single named region
// node. Used by the health checker to ping each region independently.
func ConnectDirect(ctx context.Context, cfg *config.Config, node config.RegionNode) (*Client, error) {
	uri := cfg.NodeURI(node)
	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(5 * time.Second).
		SetServerSelectionTimeout(5 * time.Second).
		SetDirect(true)
	mc, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("db: direct connect to %s (%s): %w", node.Region, uri, err)
	}
	return &Client{mc: mc, cfg: cfg}, nil
}

// Ping measures round-trip latency to MongoDB and returns it in milliseconds.
// It is called by the Health-Check service every HealthInterval.
func (c *Client) Ping(ctx context.Context) (float64, error) {
	start := time.Now()
	if err := c.mc.Ping(ctx, readpref.Nearest()); err != nil {
		return 0, err
	}
	return float64(time.Since(start).Milliseconds()), nil
}

// ServerStatus returns a summary of the MongoDB server status document.
// Useful for surfacing replication lag in dashboards.
func (c *Client) ServerStatus(ctx context.Context) (bson.M, error) {
	result := c.mc.Database("admin").RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}})
	var doc bson.M
	if err := result.Decode(&doc); err != nil {
		return nil, fmt.Errorf("db: serverStatus: %w", err)
	}
	return doc, nil
}

// =============================================================================
// Collection accessors
// =============================================================================

// RawGitHubEvents returns the raw zone collection for GitHub events.
func (c *Client) RawGitHubEvents() *mongo.Collection {
	return c.mc.Database(c.cfg.RawDB()).Collection(ColRawGitHubEvents)
}

// RawWeather returns the raw zone collection for weather records.
func (c *Client) RawWeather() *mongo.Collection {
	return c.mc.Database(c.cfg.RawDB()).Collection(ColRawWeather)
}

// GitHubEventsFlat returns the processed zone collection for flattened events.
func (c *Client) GitHubEventsFlat() *mongo.Collection {
	return c.mc.Database(c.cfg.ProcessedDB()).Collection(ColGitHubEventsFlat)
}

// WeatherFlat returns the processed zone collection for cleaned weather data.
func (c *Client) WeatherFlat() *mongo.Collection {
	return c.mc.Database(c.cfg.ProcessedDB()).Collection(ColWeatherFlat)
}

// EventTypeCounts returns the serving zone collection for RPT-01 aggregations.
func (c *Client) EventTypeCounts() *mongo.Collection {
	return c.mc.Database(c.cfg.ServingDB()).Collection(ColEventTypeCounts)
}

// RegionalWeatherAgg returns the serving zone collection for RPT-03 aggregations.
func (c *Client) RegionalWeatherAgg() *mongo.Collection {
	return c.mc.Database(c.cfg.ServingDB()).Collection(ColRegionalWeatherAgg)
}

// RegionHealth returns the serving zone collection for RPT-04 health records.
func (c *Client) RegionHealth() *mongo.Collection {
	return c.mc.Database(c.cfg.ServingDB()).Collection(ColRegionHealth)
}

// =============================================================================
// Index creation
// =============================================================================

// EnsureIndexes creates all required indexes across the 3 zones.
// It is idempotent and safe to call on every startup.
func (c *Client) EnsureIndexes(ctx context.Context) error {
	jobs := []struct {
		col     *mongo.Collection
		indexes []mongo.IndexModel
	}{
		{
			col: c.RawGitHubEvents(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "event_id", Value: 1}}, Options: options.Index().SetUnique(true)},
				{Keys: bson.D{{Key: "type", Value: 1}}},
				{Keys: bson.D{{Key: "created_at", Value: -1}}},
			},
		},
		{
			col: c.RawWeather(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "city_id", Value: 1}, {Key: "dt", Value: -1}}},
			},
		},
		{
			col: c.GitHubEventsFlat(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "event_id", Value: 1}}, Options: options.Index().SetUnique(true)},
				{Keys: bson.D{{Key: "type", Value: 1}}},
				{Keys: bson.D{{Key: "created_at", Value: -1}}},
				{Keys: bson.D{{Key: "city", Value: 1}, {Key: "timestamp", Value: -1}}},
			},
		},
		{
			col: c.WeatherFlat(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "city_name", Value: 1}, {Key: "timestamp", Value: -1}}},
				{Keys: bson.D{{Key: "region", Value: 1}}},
			},
		},
		{
			col: c.RegionHealth(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "region", Value: 1}, {Key: "last_check", Value: -1}}},
			},
		},
	}

	for _, job := range jobs {
		if _, err := job.col.Indexes().CreateMany(ctx, job.indexes); err != nil {
			return fmt.Errorf("db: ensureIndexes on %s: %w", job.col.Name(), err)
		}
	}
	fmt.Println("[db] Indexes ensured across all collections")
	return nil
}

// =============================================================================
// CAP Theorem helpers
// =============================================================================

// parseWriteConcern maps the config string to a mongo.WriteConcern.
//
//	"majority"  → w:majority  (safest, higher latency — cross-region durability)
//	"1"         → w:1         (fastest, data may not reach secondaries)
//	"2"         → w:2         (intermediate)
func parseWriteConcern(s string) (*writeconcern.WriteConcern, error) {
	switch s {
	case "majority":
		return writeconcern.Majority(), nil
	case "1":
		return writeconcern.W1(), nil
	case "2":
		return &writeconcern.WriteConcern{W: 2}, nil
	default:
		return nil, fmt.Errorf("unsupported write concern: %q (use majority|1|2)", s)
	}
}

// parseReadPref maps the config string to a mongo.ReadPref.
//
//	"nearest"              → route to geographically closest node (lowest latency)
//	"primary"             → always read from primary (strongest consistency)
//	"secondaryPreferred"  → read from secondaries when available (higher throughput)
func parseReadPref(s string) (*readpref.ReadPref, error) {
	switch s {
	case "nearest":
		return readpref.Nearest(), nil
	case "primary":
		return readpref.Primary(), nil
	case "primaryPreferred":
		return readpref.PrimaryPreferred(), nil
	case "secondaryPreferred":
		return readpref.SecondaryPreferred(), nil
	case "secondary":
		return readpref.Secondary(), nil
	default:
		return nil, fmt.Errorf("unsupported read preference: %q", s)
	}
}
