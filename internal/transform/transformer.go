// Package transform implements the raw → processed pipeline.
// It reads unprocessed records from the raw zone, applies cleaning and
// enrichment, and writes to the processed zone with idempotent upserts.
package transform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

const batchSize = 500

// Transformer reads raw zone records and writes to the processed zone.
type Transformer struct {
	client *db.Client
	region string
	log    *zap.Logger
}

// New creates a Transformer.
func New(client *db.Client, region string, log *zap.Logger) *Transformer {
	return &Transformer{client: client, region: region, log: log}
}

// RunGitHub transforms all unprocessed GitHub events.
// "Unprocessed" means: no matching event_id in github_events_flat.
func (t *Transformer) RunGitHub(ctx context.Context) (int, error) {
	processedAt := time.Now().UTC()
	batchID := fmt.Sprintf("tf-gh-%d", processedAt.UnixMilli())

	// Find raw events not yet in the processed zone.
	// We use $lookup-style logic: query raw, filter out those whose event_id
	// already exists in processed. For simplicity we use a cursor over raw
	// and let the processed-side upsert handle deduplication.
	rawColl := t.client.RawGitHubEvents()

	cursor, err := rawColl.Find(ctx, bson.M{}, options.Find().SetBatchSize(int32(batchSize)))
	if err != nil {
		return 0, fmt.Errorf("transform github find: %w", err)
	}
	defer cursor.Close(ctx)

	var written int
	for cursor.Next(ctx) {
		var raw models.RawGitHubEvent
		if err := cursor.Decode(&raw); err != nil {
			t.log.Warn("decode raw github event", zap.Error(err))
			continue
		}

		flat := transformGitHub(&raw, processedAt, batchID)
		inserted, err := t.upsertFlat(ctx, flat)
		if err != nil {
			t.log.Warn("upsert flat github event",
				zap.String("event_id", flat.EventID),
				zap.Error(err),
			)
			continue
		}
		if inserted {
			written++
		}
	}

	if err := cursor.Err(); err != nil {
		return written, fmt.Errorf("github cursor: %w", err)
	}

	t.log.Info("github transform complete",
		zap.Int("written", written),
		zap.String("batch_id", batchID),
	)
	return written, nil
}

// RunWeather transforms all unprocessed weather records.
func (t *Transformer) RunWeather(ctx context.Context) (int, error) {
	processedAt := time.Now().UTC()
	batchID := fmt.Sprintf("tf-owm-%d", processedAt.UnixMilli())

	rawColl := t.client.RawWeather()
	cursor, err := rawColl.Find(ctx, bson.M{}, options.Find().SetBatchSize(int32(batchSize)))
	if err != nil {
		return 0, fmt.Errorf("transform weather find: %w", err)
	}
	defer cursor.Close(ctx)

	var written int
	for cursor.Next(ctx) {
		var raw models.RawWeather
		if err := cursor.Decode(&raw); err != nil {
			t.log.Warn("decode raw weather", zap.Error(err))
			continue
		}

		flat := transformWeather(&raw, processedAt, batchID)
		inserted, err := t.upsertWeatherFlat(ctx, flat)
		if err != nil {
			t.log.Warn("upsert flat weather",
				zap.String("city", flat.CityName),
				zap.Error(err),
			)
			continue
		}
		if inserted {
			written++
		}
	}

	t.log.Info("weather transform complete",
		zap.Int("written", written),
		zap.String("batch_id", batchID),
	)
	return written, nil
}

// ---------------------------------------------------------------------------
// Transformation logic
// ---------------------------------------------------------------------------

func transformGitHub(raw *models.RawGitHubEvent, processedAt time.Time, batchID string) *models.GitHubEventFlat {
	flat := &models.GitHubEventFlat{
		EventID:     raw.EventID,
		Type:        raw.Type,
		ActorLogin:  raw.Actor.Login,
		ActorID:     raw.Actor.ID,
		IsBot:       isBot(raw.Actor.Login),
		RepoName:    raw.Repo.Name,
		RepoID:      raw.Repo.ID,
		Public:      raw.Public,
		CreatedAt:   raw.CreatedAt,
		Hour:        raw.CreatedAt.UTC().Hour(),
		DayOfWeek:   int(raw.CreatedAt.UTC().Weekday()),
		ProcessedAt: processedAt,
		Region:      raw.Region,
		BatchID:     batchID,
	}

	// Quality flag: missing org_login is the primary gap identified in EDA
	if raw.Org != nil && raw.Org.Login != "" {
		orgLogin := raw.Org.Login
		flat.OrgLogin = &orgLogin
		flat.QualityFlag = models.QualityOK
	} else {
		flat.QualityFlag = models.QualityMissingOrg
	}

	return flat
}

func transformWeather(raw *models.RawWeather, processedAt time.Time, batchID string) *models.WeatherFlat {
	// Kelvin → Celsius conversion
	tempC := raw.Main.Temp - 273.15
	feelsLikeC := raw.Main.FeelsLike - 273.15

	// Determine quality flag
	quality := models.QualityOK
	if raw.Wind.Speed == 0 {
		quality = models.QualityZeroSensorValue
	}
	if raw.Visibility == 10000 {
		quality = models.QualityCappedVisibility
	}

	// Primary weather condition
	condition := "Unknown"
	if len(raw.Weather) > 0 {
		condition = raw.Weather[0].Main
	}

	obsTime := time.Unix(raw.Dt, 0).UTC()

	return &models.WeatherFlat{
		CityName:     raw.CityName,
		Country:      raw.Country,
		Region:       raw.Region,
		Latitude:     raw.Coord.Lat,
		Longitude:    raw.Coord.Lon,
		TemperatureC: roundTo2(tempC),
		FeelsLikeC:   roundTo2(feelsLikeC),
		Humidity:     raw.Main.Humidity,
		WindSpeed:    raw.Wind.Speed,
		Condition:    condition,
		Visibility:   raw.Visibility,
		ObservedAt:   obsTime,
		Hour:         obsTime.Hour(),
		QualityFlag:  quality,
		ProcessedAt:  processedAt,
		BatchID:      batchID,
	}
}

// isBot returns true when the login looks like an automated actor.
// Mirrors the EDA finding that ~17% of events are bot-driven.
func isBot(login string) bool {
	lower := strings.ToLower(login)
	return strings.HasSuffix(lower, "[bot]") ||
		strings.HasSuffix(lower, "-bot") ||
		lower == "copilot" ||
		lower == "github-actions"
}

func roundTo2(f float64) float64 {
	return float64(int(f*100)) / 100
}

// ---------------------------------------------------------------------------
// Upsert helpers
// ---------------------------------------------------------------------------

func (t *Transformer) upsertFlat(ctx context.Context, flat *models.GitHubEventFlat) (bool, error) {
	coll := t.client.GitHubEventsFlat()
	filter := bson.M{"event_id": flat.EventID}
	update := bson.M{"$setOnInsert": flat}
	res, err := coll.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0, nil
}

func (t *Transformer) upsertWeatherFlat(ctx context.Context, flat *models.WeatherFlat) (bool, error) {
	coll := t.client.WeatherFlat()
	filter := bson.M{
		"city_name":   flat.CityName,
		"observed_at": flat.ObservedAt,
	}
	update := bson.M{"$setOnInsert": flat}
	res, err := coll.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0, nil
}

// EnsureIndexes creates indexes on processed collections.
func EnsureIndexes(ctx context.Context, client *db.Client) error {
	ghIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "event_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("event_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "type", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("type_created"),
		},
		{
			Keys:    bson.D{{Key: "is_bot", Value: 1}},
			Options: options.Index().SetName("is_bot"),
		},
		{
			Keys:    bson.D{{Key: "quality_flag", Value: 1}},
			Options: options.Index().SetName("quality_flag"),
		},
	}
	if _, err := client.GitHubEventsFlat().Indexes().CreateMany(ctx, ghIndexes); err != nil {
		return err
	}

	owmIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "city_name", Value: 1}, {Key: "observed_at", Value: -1}},
			Options: options.Index().SetUnique(true).SetName("city_observed_unique"),
		},
		{
			Keys:    bson.D{{Key: "region", Value: 1}, {Key: "observed_at", Value: -1}},
			Options: options.Index().SetName("region_observed"),
		},
		{
			Keys:    bson.D{{Key: "quality_flag", Value: 1}},
			Options: options.Index().SetName("quality_flag"),
		},
	}
	_, err := client.WeatherFlat().Indexes().CreateMany(ctx, owmIndexes)
	return err
}

// Silence unused import warning for primitive (used in models)
var _ = primitive.NilObjectID
