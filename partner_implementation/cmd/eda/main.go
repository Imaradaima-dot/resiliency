// cmd/eda/main.go — Exploratory Data Analysis Tool
//
// Implements Section 5 of the project report.  Reads from MongoDB processed
// and serving zones and prints:
//
//   - GitHub event type distribution (RPT-01)
//   - Top 10 actors and repositories
//   - Bot vs human actor split
//   - Org affiliation rate (GAP-001)
//   - Regional weather summary (RPT-03)
//   - Data quality flag counts
//
// Results are also exported as CSV files for Grafana / Streamlit ingestion.
package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/db"
	"go.mongodb.org/mongo-driver/bson"
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

	dbClient, err := db.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer dbClient.Disconnect(context.Background())

	fmt.Println("\n╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║       Global Service Resiliency — EDA Report                ║")
	fmt.Printf("║       Timestamp: %-42s║\n", time.Now().Format("2006-01-02 15:04:05 MST"))
	fmt.Println("╚══════════════════════════════════════════════════════════════╝\n")

	// ── GitHub Events Analysis ────────────────────────────────────────────────
	analyzeGitHubEvents(ctx, dbClient)

	// ── Weather Analysis ──────────────────────────────────────────────────────
	analyzeWeather(ctx, dbClient)

	// ── Data Quality Report ───────────────────────────────────────────────────
	analyzeDataQuality(ctx, dbClient)

	// ── Region Health Summary ─────────────────────────────────────────────────
	analyzeRegionHealth(ctx, dbClient)

	fmt.Println("\n[eda] done — CSV exports written to ./data/eda/")
}

// =============================================================================
// GitHub Events Analysis
// =============================================================================

func analyzeGitHubEvents(ctx context.Context, dbClient *db.Client) {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  GitHub Events Analysis (github_events_flat)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	total := countDocs(ctx, dbClient.GitHubEventsFlat(), bson.D{})
	fmt.Printf("  Total events: %d\n\n", total)
	if total == 0 {
		fmt.Println("  No data yet — run the ingestor first.")
		return
	}

	// Event type distribution.
	typeDist := aggregateTopN(ctx, dbClient.GitHubEventsFlat(), "$type", 15)
	fmt.Println("  Event Type Distribution:")
	fmt.Printf("  %-35s %8s %8s\n", "Type", "Count", "%")
	fmt.Println("  " + repeat("─", 55))

	rows := [][]string{{"event_type", "count", "percentage"}}
	for _, r := range typeDist {
		pct := 0.0
		if total > 0 {
			pct = float64(r.count) / float64(total) * 100
		}
		fmt.Printf("  %-35s %8d %7.1f%%\n", r.key, r.count, pct)
		rows = append(rows, []string{r.key, strconv.Itoa(r.count), fmt.Sprintf("%.2f", pct)})
	}
	writeCSV("event_type_distribution.csv", rows)

	// Top actors.
	fmt.Println("\n  Top 10 Actors:")
	topActors := aggregateTopN(ctx, dbClient.GitHubEventsFlat(), "$actor_login", 10)
	for i, r := range topActors {
		fmt.Printf("  %2d. %-30s %d events\n", i+1, r.key, r.count)
	}

	// Bot share.
	bots := countDocs(ctx, dbClient.GitHubEventsFlat(), bson.D{{Key: "is_bot", Value: true}})
	botPct := 0.0
	if total > 0 {
		botPct = float64(bots) / float64(total) * 100
	}
	fmt.Printf("\n  Bot actors: %d / %d (%.1f%%)\n", bots, total, botPct)

	// Org affiliation (GAP-001).
	withOrg := countDocs(ctx, dbClient.GitHubEventsFlat(), bson.D{{Key: "org_login", Value: bson.D{{Key: "$ne", Value: ""}}}})
	orgPct := 0.0
	if total > 0 {
		orgPct = float64(withOrg) / float64(total) * 100
	}
	fmt.Printf("  Org-affiliated events: %d / %d (%.1f%%)  [GAP-001: %.1f%% null]\n",
		withOrg, total, orgPct, 100-orgPct)

	// Unique actors and repos.
	uniqueActors := countDistinct(ctx, dbClient.GitHubEventsFlat(), "actor_login")
	uniqueRepos := countDistinct(ctx, dbClient.GitHubEventsFlat(), "repo_name")
	fmt.Printf("  Unique actors: %d    Unique repos: %d\n", uniqueActors, uniqueRepos)
}

// =============================================================================
// Weather Analysis
// =============================================================================

func analyzeWeather(ctx context.Context, dbClient *db.Client) {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  OpenWeatherMap Analysis (weather_flat → regional_weather_agg)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Read from serving zone (pre-aggregated).
	cursor, err := dbClient.RegionalWeatherAgg().Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "avg_temp_c", Value: -1}}),
	)
	if err != nil {
		fmt.Printf("  error reading regional_weather_agg: %v\n", err)
		return
	}
	defer cursor.Close(ctx)

	type agg struct {
		Region      string  `bson:"region"`
		AvgTempC    float64 `bson:"avg_temp_c"`
		AvgHumidity float64 `bson:"avg_humidity"`
		AvgWindMS   float64 `bson:"avg_wind_ms"`
		CityCount   int     `bson:"city_count"`
	}

	var rows []agg
	cursor.All(ctx, &rows)

	if len(rows) == 0 {
		fmt.Println("  No data yet — run the ingestor first.")
		return
	}

	fmt.Printf("  %-12s %10s %12s %12s %8s\n", "Region", "AvgTemp°C", "AvgHumidity%", "AvgWind m/s", "Cities")
	fmt.Println("  " + repeat("─", 60))

	csvRows := [][]string{{"region", "avg_temp_c", "avg_humidity", "avg_wind_ms", "city_count"}}
	for _, r := range rows {
		fmt.Printf("  %-12s %10.2f %12.0f %12.2f %8d\n",
			r.Region, r.AvgTempC, r.AvgHumidity, r.AvgWindMS, r.CityCount)
		csvRows = append(csvRows, []string{
			r.Region,
			fmt.Sprintf("%.2f", r.AvgTempC),
			fmt.Sprintf("%.1f", r.AvgHumidity),
			fmt.Sprintf("%.2f", r.AvgWindMS),
			strconv.Itoa(r.CityCount),
		})
	}
	writeCSV("regional_weather_summary.csv", csvRows)
}

// =============================================================================
// Data Quality Report
// =============================================================================

func analyzeDataQuality(ctx context.Context, dbClient *db.Client) {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Data Quality Flags")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// GitHub quality flags.
	githubFlags := aggregateTopN(ctx, dbClient.GitHubEventsFlat(), "$quality_flag", 10)
	fmt.Println("  github_events_flat:")
	for _, r := range githubFlags {
		fmt.Printf("    %-30s %d\n", r.key, r.count)
	}

	// Weather quality flags.
	weatherFlags := aggregateTopN(ctx, dbClient.WeatherFlat(), "$quality_flag", 10)
	fmt.Println("  weather_flat:")
	for _, r := range weatherFlags {
		fmt.Printf("    %-30s %d\n", r.key, r.count)
	}
}

// =============================================================================
// Region Health Summary
// =============================================================================

func analyzeRegionHealth(ctx context.Context, dbClient *db.Client) {
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Region Health (region_health)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	cursor, err := dbClient.RegionHealth().Find(ctx, bson.D{},
		options.Find().SetSort(bson.D{{Key: "last_check", Value: -1}}).SetLimit(5),
	)
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		return
	}
	defer cursor.Close(ctx)

	type health struct {
		Region    string    `bson:"region"`
		Status    string    `bson:"status"`
		LatencyMS float64   `bson:"latency_ms"`
		LastCheck time.Time `bson:"last_check"`
	}
	var records []health
	cursor.All(ctx, &records)

	if len(records) == 0 {
		fmt.Println("  No health records — run the healthcheck service first.")
		return
	}

	fmt.Printf("  %-40s %-12s %10s %s\n", "Region", "Status", "Latency ms", "Last Check")
	fmt.Println("  " + repeat("─", 80))
	for _, r := range records {
		fmt.Printf("  %-40s %-12s %10.1f %s\n",
			truncate(r.Region, 40), r.Status, r.LatencyMS, r.LastCheck.Format("15:04:05"))
	}
}

// =============================================================================
// Aggregation helpers
// =============================================================================

type countResult struct {
	key   string
	count int
}

func aggregateTopN(ctx context.Context, col *mongo.Collection, groupField string, n int) []countResult {
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: groupField},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
		{{Key: "$limit", Value: n}},
	}

	cursor, err := col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var raw []bson.M
	cursor.All(ctx, &raw)

	var out []countResult
	for _, r := range raw {
		key, _ := r["_id"].(string)
		cnt := 0
		switch v := r["count"].(type) {
		case int32:
			cnt = int(v)
		case int64:
			cnt = int(v)
		}
		out = append(out, countResult{key: key, count: cnt})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].count > out[j].count })
	return out
}

func countDocs(ctx context.Context, col *mongo.Collection, filter bson.D) int64 {
	n, _ := col.CountDocuments(ctx, filter)
	return n
}

func countDistinct(ctx context.Context, col *mongo.Collection, field string) int {
	vals, err := col.Distinct(ctx, field, bson.D{})
	if err != nil {
		return 0
	}
	return len(vals)
}

// =============================================================================
// CSV export
// =============================================================================

func writeCSV(filename string, rows [][]string) {
	dir := "./data/eda"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.Create(dir + "/" + filename)
	if err != nil {
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.WriteAll(rows)
	fmt.Printf("[eda] exported %s (%d rows)\n", filename, len(rows)-1)
}

// =============================================================================
// Utility
// =============================================================================

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
