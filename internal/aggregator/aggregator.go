// Package aggregator implements the processed → serving aggregation pipeline.
// It reads github_events_flat and weather_flat from the processed zone and
// writes pre-aggregated documents to the serving zone for dashboard use.
package aggregator

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

// Aggregator computes serving-zone aggregations from the processed zone.
type Aggregator struct {
	client *db.Client
	region string
	log    *zap.Logger
}

// New creates an Aggregator.
func New(client *db.Client, region string, log *zap.Logger) *Aggregator {
	return &Aggregator{client: client, region: region, log: log}
}

// RunEventTypes aggregates github_events_flat → event_type_counts.
// Groups all events by type, counts them, computes percentage, and upserts
// one document per event type with a shared window covering the full dataset.
func (a *Aggregator) RunEventTypes(ctx context.Context) (int, error) {
	computedAt := time.Now().UTC()
	coll := a.client.GitHubEventsFlat()

	pipeline := bson.A{
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$type"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "window_start", Value: bson.D{{Key: "$min", Value: "$created_at"}}},
			{Key: "window_end", Value: bson.D{{Key: "$max", Value: "$created_at"}}},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("aggregator event types: %w", err)
	}
	defer cursor.Close(ctx)

	type aggResult struct {
		ID          string    `bson:"_id"`
		Count       int64     `bson:"count"`
		WindowStart time.Time `bson:"window_start"`
		WindowEnd   time.Time `bson:"window_end"`
	}

	var rows []aggResult
	if err := cursor.All(ctx, &rows); err != nil {
		return 0, fmt.Errorf("aggregator event types decode: %w", err)
	}

	// Compute total for percentage
	var total int64
	for _, r := range rows {
		total += r.Count
	}
	if total == 0 {
		a.log.Info("aggregator: no github events found, skipping event type aggregation")
		return 0, nil
	}

	servingColl := a.client.EventTypeCounts()
	var written int

	for _, r := range rows {
		pct := float64(r.Count) / float64(total) * 100
		doc := &models.EventTypeCount{
			EventType:   r.ID,
			Count:       r.Count,
			Percentage:  roundTo2(pct),
			WindowStart: r.WindowStart,
			WindowEnd:   r.WindowEnd,
			ComputedAt:  computedAt,
			Region:      a.region,
		}
		filter := bson.M{"event_type": doc.EventType}
		update := bson.M{"$set": doc}
		_, err := servingColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
		if err != nil {
			a.log.Warn("aggregator: upsert event type count",
				zap.String("type", doc.EventType), zap.Error(err))
			continue
		}
		written++
	}

	a.log.Info("aggregator: event type counts written",
		zap.Int("types", written), zap.Int64("total_events", total))
	return written, nil
}

// RunWeatherAgg aggregates weather_flat → regional_weather_agg.
// Excludes records flagged ZERO_SENSOR_VALUE from numeric averages.
func (a *Aggregator) RunWeatherAgg(ctx context.Context) (int, error) {
	computedAt := time.Now().UTC()
	coll := a.client.WeatherFlat()

	pipeline := bson.A{
		// Exclude records with zero sensor values from aggregation
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "quality_flag", Value: bson.D{{Key: "$ne", Value: models.QualityZeroSensorValue}}},
		}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$region"},
			{Key: "city_count", Value: bson.D{{Key: "$addToSet", Value: "$city_name"}}},
			{Key: "avg_temp_c", Value: bson.D{{Key: "$avg", Value: "$temperature_c"}}},
			{Key: "avg_humidity", Value: bson.D{{Key: "$avg", Value: "$humidity"}}},
			{Key: "avg_wind_speed", Value: bson.D{{Key: "$avg", Value: "$wind_speed"}}},
			{Key: "observation_ts", Value: bson.D{{Key: "$max", Value: "$observed_at"}}},
		}}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, fmt.Errorf("aggregator weather: %w", err)
	}
	defer cursor.Close(ctx)

	type weatherAgg struct {
		Region        string    `bson:"_id"`
		CityCount     []string  `bson:"city_count"`
		AvgTempC      float64   `bson:"avg_temp_c"`
		AvgHumidity   float64   `bson:"avg_humidity"`
		AvgWindSpeed  float64   `bson:"avg_wind_speed"`
		ObservationTs time.Time `bson:"observation_ts"`
	}

	var rows []weatherAgg
	if err := cursor.All(ctx, &rows); err != nil {
		return 0, fmt.Errorf("aggregator weather decode: %w", err)
	}

	if len(rows) == 0 {
		a.log.Info("aggregator: no weather data found, skipping weather aggregation")
		return 0, nil
	}

	servingColl := a.client.RegionalWeatherAgg()
	var written int

	for _, r := range rows {
		doc := &models.RegionalWeatherAgg{
			Region:        r.Region,
			CityCount:     len(r.CityCount),
			AvgTempC:      roundTo2(r.AvgTempC),
			AvgHumidity:   roundTo2(r.AvgHumidity),
			AvgWindSpeed:  roundTo2(r.AvgWindSpeed),
			ObservationTs: r.ObservationTs,
			ComputedAt:    computedAt,
		}
		filter := bson.M{"region": doc.Region}
		update := bson.M{"$set": doc}
		_, err := servingColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
		if err != nil {
			a.log.Warn("aggregator: upsert weather agg",
				zap.String("region", doc.Region), zap.Error(err))
			continue
		}
		written++
	}

	a.log.Info("aggregator: regional weather agg written", zap.Int("regions", written))
	return written, nil
}

func roundTo2(f float64) float64 {
	return float64(int(f*100)) / 100
}
