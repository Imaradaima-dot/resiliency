// Package transform converts raw API documents into flattened, enriched
// records suitable for the processed zone, and aggregates them for the
// serving zone.  It also implements quality checks from the STTM.
package transform

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/db"
	"github.com/resiliency/global-service/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// kelvinToCelsius converts a temperature in Kelvin to Celsius.
// Resolves FR-07 from the requirements specification.
func kelvinToCelsius(k float64) float64 {
	return math.Round((k-273.15)*100) / 100
}

// =============================================================================
// GitHub Events transformation
// =============================================================================

// FlattenGitHubEvent converts a raw GitHub event into a processed zone record.
// It implements:
//   - Field flattening (FR-05): actor.login → actor_login, repo.name → repo_name
//   - Timestamp enrichment (FR-06): hour-of-day and day-of-week
//   - Bot detection: actor login ending with "[bot]"
//   - Quality flagging: GAP-001 (missing org) — events are retained with empty org_login
func FlattenGitHubEvent(raw models.RawGitHubEvent) models.GitHubEventFlat {
	qFlag := "ok"
	if raw.OrgLogin() == "" {
		qFlag = "missing_org"
	}

	return models.GitHubEventFlat{
		ID:          primitive.NewObjectID(),
		EventID:     raw.EventID,
		Type:        raw.Type,
		ActorLogin:  raw.Actor.Login,
		RepoName:    raw.Repo.Name,
		OrgLogin:    raw.OrgLogin(),
		IsBot:       raw.IsBot(),
		Hour:        raw.CreatedAt.UTC().Hour(),
		DayOfWeek:   raw.CreatedAt.UTC().Weekday().String(),
		CreatedAt:   raw.CreatedAt,
		ProcessedAt: time.Now().UTC(),
		QualityFlag: qFlag,
	}
}

// FlattenGitHubEvents transforms a slice of raw events in one call.
func FlattenGitHubEvents(raws []models.RawGitHubEvent) []models.GitHubEventFlat {
	out := make([]models.GitHubEventFlat, 0, len(raws))
	for _, r := range raws {
		out = append(out, FlattenGitHubEvent(r))
	}
	return out
}

// =============================================================================
// Weather transformation
// =============================================================================

// FlattenWeather converts a raw OWM record into a processed zone record.
// It implements:
//   - Kelvin → Celsius conversion (FR-07)
//   - Region tagging (FR-08)
//   - Quality flagging (GAP-003: zero wind_speed, GAP-004: capped visibility)
func FlattenWeather(raw models.RawWeather, cfg *config.Config) models.WeatherFlat {
	region := cfg.RegionForCity(raw.Name)

	qFlag := "ok"
	if raw.Wind.Speed == 0 {
		qFlag = "zero_wind"
	}
	if raw.Visibility >= 10000 {
		if qFlag == "ok" {
			qFlag = "capped_visibility"
		} else {
			qFlag += ",capped_visibility"
		}
	}

	return models.WeatherFlat{
		ID:           primitive.NewObjectID(),
		CityName:     raw.Name,
		Country:      raw.Sys.Country,
		Region:       region,
		Lat:          raw.Coord.Lat,
		Lon:          raw.Coord.Lon,
		TemperatureC: kelvinToCelsius(raw.Main.Temp),
		FeelsLikeC:   kelvinToCelsius(raw.Main.FeelsLike),
		Humidity:     raw.Main.Humidity,
		WindSpeed:    raw.Wind.Speed,
		Condition:    raw.PrimaryCondition(),
		Visibility:   raw.Visibility,
		CloudCover:   raw.Clouds.All,
		Timestamp:    time.Unix(raw.Dt, 0).UTC(),
		ProcessedAt:  time.Now().UTC(),
		QualityFlag:  qFlag,
	}
}

// FlattenWeatherRecords transforms a slice of raw weather records.
func FlattenWeatherRecords(raws []models.RawWeather, cfg *config.Config) []models.WeatherFlat {
	out := make([]models.WeatherFlat, 0, len(raws))
	for _, r := range raws {
		out = append(out, FlattenWeather(r, cfg))
	}
	return out
}

// =============================================================================
// Serving zone aggregations
// =============================================================================

// BuildEventTypeCounts aggregates event type distribution from processed records
// and upserts into the serving zone (RPT-01).
func BuildEventTypeCounts(ctx context.Context, dbClient *db.Client, windowStart, windowEnd time.Time) error {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{
			{Key: "processed_at", Value: bson.D{
				{Key: "$gte", Value: windowStart},
				{Key: "$lte", Value: windowEnd},
			}},
		}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$type"},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
	}

	cursor, err := dbClient.GitHubEventsFlat().Aggregate(ctx, pipeline)
	if err != nil {
		return fmt.Errorf("transform: event type aggregate: %w", err)
	}
	defer cursor.Close(ctx)

	type aggResult struct {
		Type  string `bson:"_id"`
		Count int    `bson:"count"`
	}

	var results []aggResult
	if err := cursor.All(ctx, &results); err != nil {
		return fmt.Errorf("transform: decode aggregate: %w", err)
	}

	// Compute total for percentage calculation.
	total := 0
	for _, r := range results {
		total += r.Count
	}
	fmt.Printf("[transform] event_type_counts: matched %d events across %d types in window %s → %s\n",
		total, len(results), windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339))
	if total == 0 {
		fmt.Printf("[transform] event_type_counts: 0 events matched — check processed_at field in github_events_flat\n")
		return nil
	}

	col := dbClient.EventTypeCounts()
	now := time.Now().UTC()

	for _, r := range results {
		doc := models.EventTypeCount{
			EventType:   r.Type,
			Count:       r.Count,
			Percentage:  math.Round(float64(r.Count)/float64(total)*1000) / 10, // 1 dp
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
		}
		filter := bson.D{
			{Key: "event_type", Value: r.Type},
			{Key: "window_start", Value: windowStart},
		}
		update := bson.D{{Key: "$set", Value: doc}, {Key: "$setOnInsert", Value: bson.D{{Key: "created_at", Value: now}}}}
		_, err := col.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
		if err != nil {
			return fmt.Errorf("transform: upsert event_type_counts: %w", err)
		}
	}

	fmt.Printf("[transform] event_type_counts: upserted %d types for window %s → %s\n",
		len(results), windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339))
	return nil
}

// BuildRegionalWeatherAgg aggregates regional weather means and upserts into
// the serving zone (RPT-03).
func BuildRegionalWeatherAgg(ctx context.Context, dbClient *db.Client) error {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{
			// Exclude zero-wind records (GAP-003 mitigation).
			{Key: "quality_flag", Value: bson.D{{Key: "$ne", Value: "zero_wind"}}},
		}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$region"},
			{Key: "avg_temp_c", Value: bson.D{{Key: "$avg", Value: "$temperature_c"}}},
			{Key: "avg_humidity", Value: bson.D{{Key: "$avg", Value: "$humidity"}}},
			{Key: "avg_wind_ms", Value: bson.D{{Key: "$avg", Value: "$wind_speed"}}},
			{Key: "city_count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
	}

	cursor, err := dbClient.WeatherFlat().Aggregate(ctx, pipeline)
	if err != nil {
		return fmt.Errorf("transform: regional weather aggregate: %w", err)
	}
	defer cursor.Close(ctx)

	type aggResult struct {
		Region      string  `bson:"_id"`
		AvgTempC    float64 `bson:"avg_temp_c"`
		AvgHumidity float64 `bson:"avg_humidity"`
		AvgWindMS   float64 `bson:"avg_wind_ms"`
		CityCount   int     `bson:"city_count"`
	}

	var results []aggResult
	if err := cursor.All(ctx, &results); err != nil {
		return fmt.Errorf("transform: decode regional agg: %w", err)
	}

	col := dbClient.RegionalWeatherAgg()
	now := time.Now().UTC()

	for _, r := range results {
		doc := models.RegionalWeatherAgg{
			Region:      r.Region,
			AvgTempC:    math.Round(r.AvgTempC*100) / 100,
			AvgHumidity: math.Round(r.AvgHumidity*100) / 100,
			AvgWindMS:   math.Round(r.AvgWindMS*100) / 100,
			CityCount:   r.CityCount,
			Timestamp:   now,
		}
		filter := bson.D{{Key: "region", Value: r.Region}}
		update := bson.D{{Key: "$set", Value: doc}}
		_, err := col.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
		if err != nil {
			return fmt.Errorf("transform: upsert regional_weather_agg: %w", err)
		}
	}

	fmt.Printf("[transform] regional_weather_agg: upserted %d regions\n", len(results))
	return nil
}
