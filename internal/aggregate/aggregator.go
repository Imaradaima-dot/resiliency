// Package aggregate implements the processed → serving pipeline.
// It reads cleaned records from resiliency_processed and refreshes
// dashboard-ready collections in resiliency_serving.
package aggregate

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// Aggregator refreshes serving-zone collections from processed-zone data.
type Aggregator struct {
	client *db.Client
	region string
	log    *zap.Logger
}

// Result summarizes one aggregation cycle.
type Result struct {
	EventTypeRows       int
	RegionalWeatherRows int
}

// New creates an Aggregator.
func New(client *db.Client, region string, log *zap.Logger) *Aggregator {
	return &Aggregator{client: client, region: region, log: log}
}

// Run refreshes all serving-zone aggregations.
func (a *Aggregator) Run(ctx context.Context) (*Result, error) {
	start := time.Now().UTC()

	eventRows, err := a.RefreshEventTypeCounts(ctx, start)
	if err != nil {
		return nil, fmt.Errorf("refresh event type counts: %w", err)
	}

	weatherRows, err := a.RefreshRegionalWeatherAgg(ctx, start)
	if err != nil {
		return nil, fmt.Errorf("refresh regional weather agg: %w", err)
	}

	result := &Result{
		EventTypeRows:       eventRows,
		RegionalWeatherRows: weatherRows,
	}

	a.log.Info("aggregation cycle complete",
		zap.Int("event_type_rows", eventRows),
		zap.Int("regional_weather_rows", weatherRows),
		zap.Duration("elapsed", time.Since(start)),
	)

	return result, nil
}

// RefreshEventTypeCounts computes the GitHub event distribution from
// github_events_flat and refreshes resiliency_serving.event_type_counts.
func (a *Aggregator) RefreshEventTypeCounts(ctx context.Context, computedAt time.Time) (int, error) {
	source := a.client.GitHubEventsFlat()
	target := a.client.EventTypeCounts()

	total, err := source.CountDocuments(ctx, bson.M{})
	if err != nil {
		return 0, fmt.Errorf("count github_events_flat: %w", err)
	}
	if total == 0 {
		a.log.Warn("no github_events_flat records available for aggregation")
		return 0, nil
	}

	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$type"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "window_start", Value: bson.D{{Key: "$min", Value: "$created_at"}}},
			{Key: "window_end", Value: bson.D{{Key: "$max", Value: "$created_at"}}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}, {Key: "_id", Value: 1}}}},
	}

	cursor, err := source.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("aggregate event types: %w", err)
	}
	defer cursor.Close(ctx)

	type eventTypeRow struct {
		EventType   string    `bson:"_id"`
		Count       int64     `bson:"count"`
		WindowStart time.Time `bson:"window_start"`
		WindowEnd   time.Time `bson:"window_end"`
	}

	var docs []interface{}
	for cursor.Next(ctx) {
		var row eventTypeRow
		if err := cursor.Decode(&row); err != nil {
			return 0, fmt.Errorf("decode event type row: %w", err)
		}

		docs = append(docs, models.EventTypeCount{
			EventType:   row.EventType,
			Count:       row.Count,
			Percentage:  round2(float64(row.Count) / float64(total) * 100),
			WindowStart: row.WindowStart,
			WindowEnd:   row.WindowEnd,
			ComputedAt:  computedAt,
			Region:      a.region,
		})
	}
	if err := cursor.Err(); err != nil {
		return 0, fmt.Errorf("event type cursor: %w", err)
	}

	if _, err := target.DeleteMany(ctx, bson.M{"region": a.region}); err != nil {
		return 0, fmt.Errorf("delete stale event_type_counts: %w", err)
	}
	if len(docs) == 0 {
		return 0, nil
	}
	res, err := target.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil {
		return 0, fmt.Errorf("insert event_type_counts: %w", err)
	}

	return len(res.InsertedIDs), nil
}

// RefreshRegionalWeatherAgg computes regional averages from weather_flat and
// refreshes resiliency_serving.regional_weather_agg.
func (a *Aggregator) RefreshRegionalWeatherAgg(ctx context.Context, computedAt time.Time) (int, error) {
	source := a.client.WeatherFlat()
	target := a.client.RegionalWeatherAgg()

	total, err := source.CountDocuments(ctx, bson.M{})
	if err != nil {
		return 0, fmt.Errorf("count weather_flat: %w", err)
	}
	if total == 0 {
		a.log.Warn("no weather_flat records available for aggregation")
		return 0, nil
	}

	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$region"},
			{Key: "cities", Value: bson.D{{Key: "$addToSet", Value: "$city_name"}}},
			{Key: "avg_temp_c", Value: bson.D{{Key: "$avg", Value: "$temperature_c"}}},
			{Key: "avg_humidity", Value: bson.D{{Key: "$avg", Value: "$humidity"}}},
			{Key: "avg_wind_speed", Value: bson.D{{Key: "$avg", Value: "$wind_speed"}}},
			{Key: "observation_ts", Value: bson.D{{Key: "$max", Value: "$observed_at"}}},
		}}},
		{{Key: "$project", Value: bson.D{
			{Key: "region", Value: "$_id"},
			{Key: "city_count", Value: bson.D{{Key: "$size", Value: "$cities"}}},
			{Key: "avg_temp_c", Value: 1},
			{Key: "avg_humidity", Value: 1},
			{Key: "avg_wind_speed", Value: 1},
			{Key: "observation_ts", Value: 1},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "region", Value: 1}}}},
	}

	cursor, err := source.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("aggregate regional weather: %w", err)
	}
	defer cursor.Close(ctx)

	type regionalWeatherRow struct {
		Region        string    `bson:"region"`
		CityCount     int       `bson:"city_count"`
		AvgTempC      float64   `bson:"avg_temp_c"`
		AvgHumidity   float64   `bson:"avg_humidity"`
		AvgWindSpeed  float64   `bson:"avg_wind_speed"`
		ObservationTs time.Time `bson:"observation_ts"`
	}

	var docs []interface{}
	for cursor.Next(ctx) {
		var row regionalWeatherRow
		if err := cursor.Decode(&row); err != nil {
			return 0, fmt.Errorf("decode regional weather row: %w", err)
		}

		docs = append(docs, models.RegionalWeatherAgg{
			Region:        row.Region,
			CityCount:     row.CityCount,
			AvgTempC:      round2(row.AvgTempC),
			AvgHumidity:   round2(row.AvgHumidity),
			AvgWindSpeed:  round2(row.AvgWindSpeed),
			ObservationTs: row.ObservationTs,
			ComputedAt:    computedAt,
		})
	}
	if err := cursor.Err(); err != nil {
		return 0, fmt.Errorf("regional weather cursor: %w", err)
	}

	// Regional weather rows are a latest snapshot keyed by data region. In local
	// Phase 3 development, one aggregator owns this serving collection.
	if _, err := target.DeleteMany(ctx, bson.M{}); err != nil {
		return 0, fmt.Errorf("delete stale regional_weather_agg: %w", err)
	}
	if len(docs) == 0 {
		return 0, nil
	}
	res, err := target.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil {
		return 0, fmt.Errorf("insert regional_weather_agg: %w", err)
	}

	return len(res.InsertedIDs), nil
}

// EnsureIndexes creates indexes on serving collections.
func EnsureIndexes(ctx context.Context, client *db.Client) error {
	eventIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "event_type", Value: 1}, {Key: "region", Value: 1}},
			Options: options.Index().SetName("event_type_region"),
		},
		{
			Keys:    bson.D{{Key: "count", Value: -1}},
			Options: options.Index().SetName("count_desc"),
		},
	}
	if _, err := client.EventTypeCounts().Indexes().CreateMany(ctx, eventIndexes); err != nil {
		return fmt.Errorf("event_type_counts indexes: %w", err)
	}

	weatherIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "region", Value: 1}},
			Options: options.Index().SetName("region"),
		},
		{
			Keys:    bson.D{{Key: "avg_temp_c", Value: -1}},
			Options: options.Index().SetName("avg_temp_desc"),
		},
	}
	if _, err := client.RegionalWeatherAgg().Indexes().CreateMany(ctx, weatherIndexes); err != nil {
		return fmt.Errorf("regional_weather_agg indexes: %w", err)
	}

	return nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
