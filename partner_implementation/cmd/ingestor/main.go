// cmd/ingestor/main.go — Event Ingestion Service
//
// Runs the full ETL pipeline on a configurable interval:
//  1. Extract: fetch GitHub events + weather concurrently
//  2. Load (raw): insert into resiliency_raw with majority write concern
//  3. Transform: flatten and enrich into resiliency_processed
//  4. Aggregate: upsert serving zone summaries into resiliency_serving
//
// The service is designed to run as a Kubernetes Deployment (continuous)
// or CronJob (scheduled). Configure via environment variables — see .env.example.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/db"
	"github.com/resiliency/global-service/internal/ingestion"
	"github.com/resiliency/global-service/internal/transform"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Connect to MongoDB ────────────────────────────────────────────────────
	dbClient, err := db.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer dbClient.Disconnect(context.Background())

	if err := dbClient.EnsureIndexes(ctx); err != nil {
		log.Fatalf("ensureIndexes: %v", err)
	}

	// ── Collectors ───────────────────────────────────────────────────────────
	githubCol := ingestion.NewGitHubCollector(cfg)
	weatherCol := ingestion.NewWeatherCollector(cfg)

	// ── Run one cycle immediately, then on every tick ────────────────────────
	fmt.Printf("[ingestor] starting  interval=%s  offline=%v\n",
		cfg.IngestInterval, cfg.OfflineMode)

	runCycle(ctx, cfg, dbClient, githubCol, weatherCol)

	ticker := time.NewTicker(cfg.IngestInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[ingestor] shutting down")
			return
		case <-ticker.C:
			runCycle(ctx, cfg, dbClient, githubCol, weatherCol)
		}
	}
}

// runCycle executes one complete Extract → Load → Transform → Aggregate cycle.
func runCycle(
	ctx context.Context,
	cfg *config.Config,
	dbClient *db.Client,
	githubCol *ingestion.GitHubCollector,
	weatherCol *ingestion.WeatherCollector,
) {
	start := time.Now()
	fmt.Printf("\n[ingestor] ── cycle start %s ──\n", start.Format(time.RFC3339))

	// ── 1. EXTRACT (concurrent) ───────────────────────────────────────────────
	// Both collectors run concurrently via channels.
	type githubResult struct {
		events []interface{}
		err    error
	}
	type weatherResult struct {
		records []interface{}
		err     error
	}

	ghCh := make(chan githubResult, 1)
	wtCh := make(chan weatherResult, 1)

	go func() {
		events, err := githubCol.Collect(ctx)
		if err != nil {
			ghCh <- githubResult{err: err}
			return
		}
		docs := make([]interface{}, len(events))
		for i, e := range events {
			docs[i] = e
		}
		ghCh <- githubResult{events: docs}
	}()

	go func() {
		records, err := weatherCol.Collect(ctx)
		if err != nil {
			wtCh <- weatherResult{err: err}
			return
		}
		docs := make([]interface{}, len(records))
		for i, r := range records {
			docs[i] = r
		}
		wtCh <- weatherResult{records: docs}
	}()

	ghRes := <-ghCh
	wtRes := <-wtCh

	// ── 2. LOAD RAW ───────────────────────────────────────────────────────────
	if ghRes.err != nil {
		fmt.Printf("[ingestor] github collect error: %v\n", ghRes.err)
	} else if len(ghRes.events) > 0 {
		inserted, skipped := bulkInsert(ctx, dbClient.RawGitHubEvents(), ghRes.events)
		fmt.Printf("[ingestor] raw_github_events  inserted=%d  duplicates_skipped=%d\n", inserted, skipped)
	}

	if wtRes.err != nil {
		fmt.Printf("[ingestor] weather collect error: %v\n", wtRes.err)
	} else if len(wtRes.records) > 0 {
		inserted, skipped := bulkInsert(ctx, dbClient.RawWeather(), wtRes.records)
		fmt.Printf("[ingestor] raw_weather  inserted=%d  duplicates_skipped=%d\n", inserted, skipped)
	}

	// ── 3. TRANSFORM → PROCESSED ZONE ────────────────────────────────────────
	if ghRes.err == nil && len(ghRes.events) > 0 {
		// Re-fetch from raw zone and transform (ensures transform uses DB data).
		transformGitHubEvents(ctx, cfg, dbClient)
	}
	if wtRes.err == nil && len(wtRes.records) > 0 {
		transformWeather(ctx, cfg, dbClient)
	}

	// ── 4. AGGREGATE → SERVING ZONE ──────────────────────────────────────────
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-24 * time.Hour)

	if err := transform.BuildEventTypeCounts(ctx, dbClient, windowStart, windowEnd); err != nil {
		fmt.Printf("[ingestor] event_type_counts error: %v\n", err)
	}
	if err := transform.BuildRegionalWeatherAgg(ctx, dbClient); err != nil {
		fmt.Printf("[ingestor] regional_weather_agg error: %v\n", err)
	}

	fmt.Printf("[ingestor] ── cycle done in %s ──\n", time.Since(start).Round(time.Millisecond))
}

// transformGitHubEvents reads from raw zone and upserts flattened records into
// the processed zone, deduplicating by event_id.
func transformGitHubEvents(ctx context.Context, cfg *config.Config, dbClient *db.Client) {
	cursor, err := dbClient.RawGitHubEvents().Find(ctx, bson.D{},
		options.Find().SetLimit(1000).SetSort(bson.D{{Key: "ingested_at", Value: -1}}),
	)
	if err != nil {
		fmt.Printf("[ingestor] transform: find raw events: %v\n", err)
		return
	}
	defer cursor.Close(ctx)

	// Decode raw events using the models package.
	type minimalEvent struct {
		EventID   string    `bson:"event_id"`
		Type      string    `bson:"type"`
		Actor     bson.M    `bson:"actor"`
		Repo      bson.M    `bson:"repo"`
		Org       bson.M    `bson:"org"`
		CreatedAt time.Time `bson:"created_at"`
	}

	var upserted int
	for cursor.Next(ctx) {
		var raw bson.M
		if err := cursor.Decode(&raw); err != nil {
			continue
		}

		// Build a minimal flat record from the BSON map.
		eventID, _ := raw["event_id"].(string)
		eventType, _ := raw["type"].(string)
		var createdAt time.Time
		rawCA := raw["created_at"]
		fmt.Printf("[raw date] %s", rawCA)
		switch v := rawCA.(type) {
		case time.Time:
			createdAt = v
			fmt.Printf("[transform][github] event_id=%s  created_at_bson_type=time.Time  value=%s\n", eventID, createdAt.Format(time.RFC3339))
		case primitive.DateTime:
			createdAt = v.Time().UTC()
			fmt.Printf("[transform][github] event_id=%s  created_at_bson_type=primitive.DateTime  value=%s\n", eventID, createdAt.Format(time.RFC3339))
		default:
			fmt.Printf("[transform][github] event_id=%s  created_at_bson_type=UNKNOWN(%T)  raw=%v  — falling back to zero time\n", eventID, rawCA, rawCA)
		}

		actor, _ := raw["actor"].(bson.M)
		actorLogin, _ := actor["login"].(string)

		repo, _ := raw["repo"].(bson.M)
		repoName, _ := repo["name"].(string)

		var orgLogin string
		if org, ok := raw["org"].(bson.M); ok && org != nil {
			orgLogin, _ = org["login"].(string)
		}

		isBot := len(actorLogin) > 5 && actorLogin[len(actorLogin)-5:] == "[bot]"
		qFlag := "ok"
		if orgLogin == "" {
			qFlag = "missing_org"
		}

		flat := bson.D{
			{Key: "event_id", Value: eventID},
			{Key: "type", Value: eventType},
			{Key: "actor_login", Value: actorLogin},
			{Key: "repo_name", Value: repoName},
			{Key: "org_login", Value: orgLogin},
			{Key: "is_bot", Value: isBot},
			{Key: "hour", Value: createdAt.UTC().Hour()},
			{Key: "day_of_week", Value: createdAt.UTC().Weekday().String()},
			{Key: "created_at", Value: createdAt},
			{Key: "processed_at", Value: time.Now().UTC()},
			{Key: "quality_flag", Value: qFlag},
		}

		if createdAt.IsZero() {
			fmt.Printf("[transform][github] WARNING event_id=%s  created_at is zero — skipping upsert\n", eventID)
			continue
		}
		filter := bson.D{{Key: "event_id", Value: eventID}}
		update := bson.D{{Key: "$setOnInsert", Value: flat}}
		_, err := dbClient.GitHubEventsFlat().UpdateOne(
			ctx, filter, update, options.Update().SetUpsert(true),
		)
		if err == nil {
			upserted++
		}
	}
	fmt.Printf("[ingestor] github_events_flat  upserted=%d\n", upserted)
}

// transformWeather reads from raw zone and upserts flattened records into the
// processed zone.
func transformWeather(ctx context.Context, cfg *config.Config, dbClient *db.Client) {
	cursor, err := dbClient.RawWeather().Find(ctx, bson.D{},
		options.Find().SetLimit(200).SetSort(bson.D{{Key: "ingested_at", Value: -1}}),
	)
	if err != nil {
		fmt.Printf("[ingestor] transform: find raw weather: %v\n", err)
		return
	}
	defer cursor.Close(ctx)

	var upserted int
	for cursor.Next(ctx) {
		var raw bson.M
		if err := cursor.Decode(&raw); err != nil {
			continue
		}

		cityName, _ := raw["name"].(string)
		dt, _ := raw["dt"].(int64)

		mainDoc, _ := raw["main"].(bson.M)
		tempK, _ := mainDoc["temp"].(float64)
		feelsK, _ := mainDoc["feels_like"].(float64)
		humidity := 0
		if h, ok := mainDoc["humidity"].(int32); ok {
			humidity = int(h)
		}

		windDoc, _ := raw["wind"].(bson.M)
		windSpeed, _ := windDoc["speed"].(float64)

		cloudsDoc, _ := raw["clouds"].(bson.M)
		cloudCover := 0
		if cc, ok := cloudsDoc["all"].(int32); ok {
			cloudCover = int(cc)
		}

		sysDoc, _ := raw["sys"].(bson.M)
		country, _ := sysDoc["country"].(string)

		visibility := 0
		if v, ok := raw["visibility"].(int32); ok {
			visibility = int(v)
		}

		condition := "Unknown"
		if weathers, ok := raw["weather"].(bson.A); ok && len(weathers) > 0 {
			if w, ok := weathers[0].(bson.M); ok {
				condition, _ = w["main"].(string)
			}
		}

		qFlag := "ok"
		if windSpeed == 0 {
			qFlag = "zero_wind"
		}
		if visibility >= 10000 {
			if qFlag == "ok" {
				qFlag = "capped_visibility"
			} else {
				qFlag += ",capped_visibility"
			}
		}

		coordDoc, _ := raw["coord"].(bson.M)
		lat, _ := coordDoc["lat"].(float64)
		lon, _ := coordDoc["lon"].(float64)

		tempC := math_round((tempK-273.15)*100) / 100
		feelsC := math_round((feelsK-273.15)*100) / 100
		region := cfg.RegionForCity(cityName)

		flat := bson.D{
			{Key: "city_name", Value: cityName},
			{Key: "country", Value: country},
			{Key: "region", Value: region},
			{Key: "lat", Value: lat},
			{Key: "lon", Value: lon},
			{Key: "temperature_c", Value: tempC},
			{Key: "feels_like_c", Value: feelsC},
			{Key: "humidity", Value: humidity},
			{Key: "wind_speed", Value: windSpeed},
			{Key: "condition", Value: condition},
			{Key: "visibility", Value: visibility},
			{Key: "cloud_cover", Value: cloudCover},
			{Key: "timestamp", Value: time.Unix(dt, 0).UTC()},
			{Key: "processed_at", Value: time.Now().UTC()},
			{Key: "quality_flag", Value: qFlag},
		}

		filter := bson.D{
			{Key: "city_name", Value: cityName},
			{Key: "timestamp", Value: time.Unix(dt, 0).UTC()},
		}
		update := bson.D{{Key: "$setOnInsert", Value: flat}}
		_, err := dbClient.WeatherFlat().UpdateOne(
			ctx, filter, update, options.Update().SetUpsert(true),
		)
		if err == nil {
			upserted++
		}
	}
	fmt.Printf("[ingestor] weather_flat  upserted=%d\n", upserted)
}

// bulkInsert calls InsertMany with ordered=false so duplicate key errors on
// event_id do not block the rest of the batch.  Returns (inserted, skipped).
func bulkInsert(ctx context.Context, col *mongo.Collection, docs []interface{}) (int, int) {
	if len(docs) == 0 {
		return 0, 0
	}
	res, err := col.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	inserted := 0
	if res != nil {
		inserted = len(res.InsertedIDs)
	}
	skipped := len(docs) - inserted
	if err != nil {
		// BulkWriteException is expected when some event_ids already exist.
		_ = err // duplicates are acceptable — logged via skipped count
	}
	return inserted, skipped
}

// math_round is a simple rounding helper (math package not imported to keep deps lean).
func math_round(x float64) float64 {
	if x < 0 {
		return float64(int(x - 0.5))
	}
	return float64(int(x + 0.5))
}
