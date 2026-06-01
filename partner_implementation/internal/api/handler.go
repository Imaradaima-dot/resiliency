// Package api exposes the serving zone collections as REST endpoints
// consumed by Grafana's Infinity datasource.
//
// Routes (all registered on the healthcheck HTTP server):
//
//	GET /api/event-types    → event_type_counts    (RPT-01)
//	GET /api/weather        → regional_weather_agg (RPT-03)
//	GET /api/region-health  → region_health        (RPT-04)
//	GET /api/summary        → KPI snapshot across all three
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/resiliency/global-service/internal/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Handler holds a DB client and registers all /api/* routes onto mux.
type Handler struct {
	db *db.Client
}

// New returns a ready Handler.
func New(dbClient *db.Client) *Handler {
	return &Handler{db: dbClient}
}

// Register wires all API routes onto the provided mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/event-types",   h.eventTypes)
	mux.HandleFunc("/api/weather",       h.weather)
	mux.HandleFunc("/api/region-health", h.regionHealth)
	mux.HandleFunc("/api/summary",       h.summary)
}

// ── /api/event-types ─────────────────────────────────────────────────────────
// Returns event_type_counts sorted by count descending (RPT-01).
// Grafana Infinity datasource reads this as a JSON array.
//
// Example response:
//
//	[
//	  {"event_type":"PushEvent","count":81,"percentage":27.0},
//	  {"event_type":"PullRequestEvent","count":63,"percentage":21.0},
//	  ...
//	]
func (h *Handler) eventTypes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := h.db.EventTypeCounts().Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "count", Value: -1}}).SetLimit(20),
	)
	if err != nil {
		jsonError(w, err)
		return
	}
	defer cursor.Close(ctx)

	type row struct {
		EventType   string  `json:"event_type"  bson:"event_type"`
		Count       int     `json:"count"       bson:"count"`
		Percentage  float64 `json:"percentage"  bson:"percentage"`
		WindowStart time.Time `json:"window_start" bson:"window_start"`
		WindowEnd   time.Time `json:"window_end"   bson:"window_end"`
	}

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		jsonError(w, err)
		return
	}
	if rows == nil {
		rows = []row{}
	}
	jsonOK(w, rows)
}

// ── /api/weather ─────────────────────────────────────────────────────────────
// Returns regional_weather_agg sorted by avg_temp_c descending (RPT-03).
//
// Example response:
//
//	[
//	  {"region":"us-east","avg_temp_c":26.25,"avg_humidity":45,"avg_wind_ms":6.43,"city_count":5},
//	  ...
//	]
func (h *Handler) weather(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := h.db.RegionalWeatherAgg().Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "avg_temp_c", Value: -1}}),
	)
	if err != nil {
		jsonError(w, err)
		return
	}
	defer cursor.Close(ctx)

	type row struct {
		Region      string    `json:"region"       bson:"region"`
		AvgTempC    float64   `json:"avg_temp_c"   bson:"avg_temp_c"`
		AvgHumidity float64   `json:"avg_humidity" bson:"avg_humidity"`
		AvgWindMS   float64   `json:"avg_wind_ms"  bson:"avg_wind_ms"`
		CityCount   int       `json:"city_count"   bson:"city_count"`
		Timestamp   time.Time `json:"timestamp"    bson:"timestamp"`
	}

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		jsonError(w, err)
		return
	}
	if rows == nil {
		rows = []row{}
	}
	jsonOK(w, rows)
}

// ── /api/region-health ────────────────────────────────────────────────────────
// Returns region_health — one doc per region, upserted on every health check.
// Grafana uses this for the traffic-light Region Health Monitor (RPT-04).
//
// Example response:
//
//	[
//	  {"region":"us-east","status":"healthy","latency_ms":2.1,"last_check":"..."},
//	  {"region":"us-west","status":"degraded","latency_ms":210.4,"last_check":"..."},
//	  ...
//	]
func (h *Handler) regionHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := h.db.RegionHealth().Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "region", Value: 1}}),
	)
	if err != nil {
		jsonError(w, err)
		return
	}
	defer cursor.Close(ctx)

	type row struct {
		Region       string    `json:"region"        bson:"region"`
		Status       string    `json:"status"        bson:"status"`
		LatencyMS    float64   `json:"latency_ms"    bson:"latency_ms"`
		WriteConcern string    `json:"write_concern" bson:"write_concern"`
		LastCheck    time.Time `json:"last_check"    bson:"last_check"`
	}

	var rows []row
	if err := cursor.All(ctx, &rows); err != nil {
		jsonError(w, err)
		return
	}
	if rows == nil {
		rows = []row{}
	}
	jsonOK(w, rows)
}

// ── /api/summary ─────────────────────────────────────────────────────────────
// Aggregates KPIs across all three serving zone collections.
// Used by the Grafana overview dashboard header stat panels.
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Total GitHub events ingested
	totalEvents, _ := h.db.EventTypeCounts().EstimatedDocumentCount(ctx)

	// Count of healthy regions
	healthyCursor, err := h.db.RegionHealth().Find(ctx, bson.D{{Key: "status", Value: "healthy"}})
	healthyRegions := 0
	if err == nil {
		var docs []bson.M
		healthyCursor.All(ctx, &docs)
		healthyCursor.Close(ctx)
		healthyRegions = len(docs)
	}

	// Total regions tracked
	totalRegions, _ := h.db.RegionHealth().CountDocuments(ctx, bson.D{})

	// Global average temperature from regional_weather_agg
	pipeline := []bson.D{
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: nil},
			{Key: "avg_temp", Value: bson.D{{Key: "$avg", Value: "$avg_temp_c"}}},
			{Key: "avg_humidity", Value: bson.D{{Key: "$avg", Value: "$avg_humidity"}}},
		}}},
	}
	cursor, err := h.db.RegionalWeatherAgg().Aggregate(ctx, pipeline)
	avgTempC := 0.0
	avgHumidity := 0.0
	if err == nil {
		var agg []struct {
			AvgTemp     float64 `bson:"avg_temp"`
			AvgHumidity float64 `bson:"avg_humidity"`
		}
		cursor.All(ctx, &agg)
		cursor.Close(ctx)
		if len(agg) > 0 {
			avgTempC = agg[0].AvgTemp
			avgHumidity = agg[0].AvgHumidity
		}
	}

	jsonOK(w, map[string]interface{}{
		"total_event_types":  totalEvents,
		"healthy_regions":    healthyRegions,
		"total_regions":      totalRegions,
		"global_avg_temp_c":  avgTempC,
		"global_avg_humidity": avgHumidity,
		"generated_at":       time.Now().UTC(),
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*") // allow Grafana to call from browser
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
