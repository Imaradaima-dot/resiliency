// cmd/mcp/main.go
//
// MCP (Model Context Protocol) server for the Global Service Resiliency project.
// Exposes MongoDB serving zone data as tools that Claude / ChatGPT can call.
//
// Tools exposed:
//   get_region_health   — live status + latency per deployment region
//   get_weather         — regional weather aggregates (temp, humidity, wind)
//   get_event_types     — GitHub event type breakdown with percentages
//   get_summary         — KPI snapshot across all three data zones
//   get_raw_events      — recent raw GitHub events (filterable by type)
//   get_weather_detail  — per-city weather from the processed zone
//
// Usage:
//   export MONGO_URI="mongodb://..."
//   go run ./cmd/mcp
//
// Claude Desktop — add to claude_desktop_config.json:
//   {
//     "mcpServers": {
//       "resiliency": {
//         "command": "/path/to/resiliency-mcp",
//         "env": { "MONGO_URI": "mongodb://..." }
//       }
//     }
//   }

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// ── DB constants (mirrors internal/db/client.go) ────────────────────────────

const (
	dbRaw       = "resiliency_raw"
	dbProcessed = "resiliency_processed"
	dbServing   = "resiliency_serving"

	colRawGitHubEvents  = "raw_github_events"
	colGitHubEventsFlat = "github_events_flat"
	colWeatherFlat      = "weather_flat"
	colEventTypeCounts  = "event_type_counts"
	colRegionalWeather  = "regional_weather_agg"
	colRegionHealth     = "region_health"
)

// ── DB client ────────────────────────────────────────────────────────────────

type dbClient struct {
	mc *mongo.Client
}

func connectDB(ctx context.Context) (*dbClient, error) {
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		return nil, fmt.Errorf("MONGO_URI env var is required")
	}
	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	mc, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := mc.Ping(ctx, readpref.Nearest()); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return &dbClient{mc: mc}, nil
}

func (d *dbClient) col(database, collection string) *mongo.Collection {
	return d.mc.Database(database).Collection(collection)
}

// ── Tool helpers ─────────────────────────────────────────────────────────────

func toJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(b)
}

func errResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultText(fmt.Sprintf("error: %s", err.Error()))
}

func ctx5s() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// ── Tool handlers ─────────────────────────────────────────────────────────────

// getRegionHealth returns the live health status of each deployment region.
func (d *dbClient) getRegionHealth(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := ctx5s()
	defer cancel()

	type row struct {
		Region       string    `bson:"region"        json:"region"`
		Status       string    `bson:"status"        json:"status"`
		LatencyMS    float64   `bson:"latency_ms"    json:"latency_ms"`
		WriteConcern string    `bson:"write_concern" json:"write_concern"`
		LastCheck    time.Time `bson:"last_check"    json:"last_check"`
	}

	cursor, err := d.col(dbServing, colRegionHealth).Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "region", Value: 1}}),
	)
	if err != nil {
		return errResult(err), nil
	}
	defer cursor.Close(ctx)

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		return errResult(err), nil
	}
	if len(rows) == 0 {
		return mcp.NewToolResultText("No region health data found. Is the healthcheck service running?"), nil
	}
	return mcp.NewToolResultText(toJSON(rows)), nil
}

// getWeather returns regional weather aggregates.
func (d *dbClient) getWeather(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := ctx5s()
	defer cancel()

	type row struct {
		Region      string    `bson:"region"       json:"region"`
		AvgTempC    float64   `bson:"avg_temp_c"   json:"avg_temp_c"`
		AvgHumidity float64   `bson:"avg_humidity" json:"avg_humidity"`
		AvgWindMS   float64   `bson:"avg_wind_ms"  json:"avg_wind_ms"`
		CityCount   int       `bson:"city_count"   json:"city_count"`
		Timestamp   time.Time `bson:"timestamp"    json:"timestamp"`
	}

	cursor, err := d.col(dbServing, colRegionalWeather).Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "avg_temp_c", Value: -1}}),
	)
	if err != nil {
		return errResult(err), nil
	}
	defer cursor.Close(ctx)

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		return errResult(err), nil
	}
	return mcp.NewToolResultText(toJSON(rows)), nil
}

// getEventTypes returns the GitHub event type breakdown.
func (d *dbClient) getEventTypes(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := ctx5s()
	defer cancel()

	type row struct {
		EventType   string    `bson:"event_type"   json:"event_type"`
		Count       int       `bson:"count"        json:"count"`
		Percentage  float64   `bson:"percentage"   json:"percentage"`
		WindowStart time.Time `bson:"window_start" json:"window_start"`
		WindowEnd   time.Time `bson:"window_end"   json:"window_end"`
	}

	cursor, err := d.col(dbServing, colEventTypeCounts).Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "count", Value: -1}}).SetLimit(20),
	)
	if err != nil {
		return errResult(err), nil
	}
	defer cursor.Close(ctx)

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		return errResult(err), nil
	}
	return mcp.NewToolResultText(toJSON(rows)), nil
}

// getSummary returns a KPI snapshot across all three data zones.
func (d *dbClient) getSummary(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := ctx5s()
	defer cancel()

	totalEvents, _ := d.col(dbServing, colEventTypeCounts).EstimatedDocumentCount(ctx)
	totalRegions, _ := d.col(dbServing, colRegionHealth).CountDocuments(ctx, bson.D{})
	healthyRegions, _ := d.col(dbServing, colRegionHealth).CountDocuments(ctx,
		bson.D{{Key: "status", Value: "healthy"}},
	)

	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: nil},
			{Key: "avg_temp",     Value: bson.D{{Key: "$avg", Value: "$avg_temp_c"}}},
			{Key: "avg_humidity", Value: bson.D{{Key: "$avg", Value: "$avg_humidity"}}},
		}}},
	}
	cur, _ := d.col(dbServing, colRegionalWeather).Aggregate(ctx, pipeline)
	var agg []struct {
		AvgTemp     float64 `bson:"avg_temp"`
		AvgHumidity float64 `bson:"avg_humidity"`
	}
	if cur != nil {
		cur.All(ctx, &agg)
		cur.Close(ctx)
	}

	avgTemp, avgHum := 0.0, 0.0
	if len(agg) > 0 {
		avgTemp = agg[0].AvgTemp
		avgHum = agg[0].AvgHumidity
	}

	summary := map[string]any{
		"total_event_types":   totalEvents,
		"healthy_regions":     healthyRegions,
		"total_regions":       totalRegions,
		"global_avg_temp_c":   fmt.Sprintf("%.1f", avgTemp),
		"global_avg_humidity": fmt.Sprintf("%.1f%%", avgHum),
		"generated_at":        time.Now().UTC().Format(time.RFC3339),
	}
	return mcp.NewToolResultText(toJSON(summary)), nil
}

// getRawEvents returns recent raw GitHub events, optionally filtered by type.
func (d *dbClient) getRawEvents(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := ctx5s()
	defer cancel()

	filter := bson.D{}
	if t := req.GetString("event_type", ""); t != "" {
		filter = bson.D{{Key: "type", Value: t}}
	}

	limit := int64(10)
	if l := req.GetFloat("limit", 10); l > 0 && l <= 50 {
		limit = int64(l)
	}

	type row struct {
		EventID   string    `bson:"event_id"   json:"event_id"`
		Type      string    `bson:"type"       json:"type"`
		ActorLogin string   `bson:"actor_login" json:"actor"`
		RepoName  string    `bson:"repo_name"  json:"repo"`
		OrgLogin  string    `bson:"org_login"  json:"org,omitempty"`
		IsBot     bool      `bson:"is_bot"     json:"is_bot"`
		CreatedAt time.Time `bson:"created_at" json:"created_at"`
	}

	cursor, err := d.col(dbProcessed, colGitHubEventsFlat).Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetLimit(limit),
	)
	if err != nil {
		return errResult(err), nil
	}
	defer cursor.Close(ctx)

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		return errResult(err), nil
	}
	return mcp.NewToolResultText(toJSON(rows)), nil
}

// getWeatherDetail returns per-city weather from the processed zone.
func (d *dbClient) getWeatherDetail(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := ctx5s()
	defer cancel()

	filter := bson.D{}
	if r := req.GetString("region", ""); r != "" {
		filter = bson.D{{Key: "region", Value: r}}
	}

	type row struct {
		CityName     string    `bson:"city_name"     json:"city"`
		Country      string    `bson:"country"       json:"country"`
		Region       string    `bson:"region"        json:"region"`
		TemperatureC float64   `bson:"temperature_c" json:"temp_c"`
		FeelsLikeC   float64   `bson:"feels_like_c"  json:"feels_like_c"`
		Humidity     int       `bson:"humidity"      json:"humidity"`
		WindSpeed    float64   `bson:"wind_speed"    json:"wind_ms"`
		Condition    string    `bson:"condition"     json:"condition"`
		Timestamp    time.Time `bson:"timestamp"     json:"timestamp"`
	}

	cursor, err := d.col(dbProcessed, colWeatherFlat).Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "timestamp", Value: -1}}).
			SetLimit(20),
	)
	if err != nil {
		return errResult(err), nil
	}
	defer cursor.Close(ctx)

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		return errResult(err), nil
	}
	return mcp.NewToolResultText(toJSON(rows)), nil
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	ctx := context.Background()

	db, err := connectDB(ctx)
	if err != nil {
		log.Fatalf("DB connection failed: %v", err)
	}
	log.Println("Connected to MongoDB")

	s := server.NewMCPServer(
		"resiliency-mcp",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	// ── Tool: get_region_health ──────────────────────────────────────────────
	s.AddTool(
		mcp.NewTool("get_region_health",
			mcp.WithDescription("Get live health status, latency, and write concern for each deployment region (us-east, us-west, europe)."),
		),
		db.getRegionHealth,
	)

	// ── Tool: get_weather ────────────────────────────────────────────────────
	s.AddTool(
		mcp.NewTool("get_weather",
			mcp.WithDescription("Get aggregated weather data per region: average temperature (°C), humidity, wind speed, and city count."),
		),
		db.getWeather,
	)

	// ── Tool: get_event_types ────────────────────────────────────────────────
	s.AddTool(
		mcp.NewTool("get_event_types",
			mcp.WithDescription("Get GitHub event type breakdown with counts and percentages (e.g. PushEvent, PullRequestEvent)."),
		),
		db.getEventTypes,
	)

	// ── Tool: get_summary ────────────────────────────────────────────────────
	s.AddTool(
		mcp.NewTool("get_summary",
			mcp.WithDescription("Get a KPI snapshot: total event types, healthy vs total regions, global average temperature and humidity."),
		),
		db.getSummary,
	)

	// ── Tool: get_raw_events ─────────────────────────────────────────────────
	s.AddTool(
		mcp.NewTool("get_raw_events",
			mcp.WithDescription("Get recent GitHub events from the processed zone. Optionally filter by event type (e.g. PushEvent) and set a limit (max 50)."),
			mcp.WithString("event_type",
				mcp.Description("Filter by event type: PushEvent, PullRequestEvent, IssuesEvent, WatchEvent, ForkEvent, CreateEvent, etc. Leave empty for all types."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Number of events to return (1-50, default 10)."),
			),
		),
		db.getRawEvents,
	)

	// ── Tool: get_weather_detail ─────────────────────────────────────────────
	s.AddTool(
		mcp.NewTool("get_weather_detail",
			mcp.WithDescription("Get per-city weather detail from the processed zone: temperature, humidity, wind, condition. Optionally filter by region."),
			mcp.WithString("region",
				mcp.Description("Filter by region: us-east, us-west, europe. Leave empty for all regions."),
			),
		),
		db.getWeatherDetail,
	)

	// MCP_TRANSPORT=http  → StreamableHTTP on MCP_PORT (default 8090) for K8s
	// MCP_TRANSPORT=stdio → stdin/stdout for Claude Desktop (default)
	transport := os.Getenv("MCP_TRANSPORT")
	if transport == "http" {
		port := os.Getenv("MCP_PORT")
		if port == "" {
			port = "8090"
		}
		addr := ":" + port
		log.Printf("MCP server starting on HTTP %s ...", addr)

		// Mount the MCP handler at /mcp and add /health for K8s probes.
		mux := http.NewServeMux()
		mcpHandler := server.NewStreamableHTTPServer(s)
		mux.Handle("/mcp", mcpHandler)
		mux.Handle("/mcp/", mcpHandler)
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"status":"ok"}`)
		})

		httpServer := &http.Server{Addr: addr, Handler: mux}
		log.Printf("Listening on %s  (MCP: /mcp  Health: /health)", addr)
		if err := httpServer.ListenAndServe(); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	} else {
		log.Println("MCP server starting on stdio...")
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("MCP server error: %v", err)
		}
	}
}
