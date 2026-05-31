// cmd/api is the REST API Gateway.
// It reads from the serving zone and exposes the six endpoints consumed by
// the Streamlit dashboard. JSON shapes match the validated Step 9 evidence.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/resiliency/global/internal/config"
	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"github.com/resiliency/global/internal/router"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

var (
	apiRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "api_requests_total",
		Help: "Total API requests by endpoint.",
	}, []string{"endpoint", "method", "status"})
	apiDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "api_request_duration_seconds",
		Help:    "API request duration by endpoint.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint"})
)

func init() {
	prometheus.MustRegister(apiRequests, apiDuration)
}

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg := config.MustLoad()
	log.Info("api gateway starting",
		zap.String("region", cfg.Region),
		zap.Int("port", cfg.APIPort),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbClient, err := db.Connect(ctx, cfg.MongoURI)
	if err != nil {
		log.Fatal("api: mongo connect failed", zap.Error(err))
	}
	defer dbClient.Disconnect(context.Background())
	log.Info("api: connected to MongoDB Atlas")

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, req)
			apiDuration.WithLabelValues(req.URL.Path).Observe(time.Since(start).Seconds())
		})
	})

	// Prometheus metrics
	r.Handle("/metrics", promhttp.Handler())

	// Health
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy"}`)
		apiRequests.WithLabelValues("/health", "GET", "200").Inc()
	})

	// Dashboard endpoints — all read from serving zone
	r.Get("/api/summary", makeSummaryHandler(dbClient, log))
	r.Get("/api/events/types", makeEventTypesHandler(dbClient, log))
	r.Get("/api/events/activity-categories", makeActivityCategoriesHandler(dbClient, log))
	r.Get("/api/weather/regions", makeWeatherRegionsHandler(dbClient, log))
	r.Get("/api/regions/health", makeRegionsHealthHandler(dbClient, log))
	r.Get("/api/routing/current", makeRoutingCurrentHandler(dbClient, log))
	r.Get("/api/routing/current/rows", makeRoutingRowsHandler(dbClient, log))

	// Refresh endpoint — used by the failover test to trigger router re-evaluation
	r.Post("/api/router/refresh", makeRouterRefreshHandler(dbClient, log))

	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.APIPort), Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info("api: listening", zap.Int("port", cfg.APIPort))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("api: listen error", zap.Error(err))
		}
	}()

	<-quit
	log.Info("api: shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
}

// ---- Handler constructors -----------------------------------------------

func makeSummaryHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Total GitHub events + top event type
		var topEvent models.EventTypeCount
		err := dbClient.EventTypeCounts().FindOne(ctx, bson.M{},
			options.FindOne().SetSort(bson.D{{Key: "count", Value: -1}}),
		).Decode(&topEvent)

		totalEvents, _ := dbClient.GitHubEventsFlat().EstimatedDocumentCount(ctx)
		eventTypeCount, _ := dbClient.EventTypeCounts().EstimatedDocumentCount(ctx)

		// Weather summary
		weatherCursor, _ := dbClient.RegionalWeatherAgg().Find(ctx, bson.M{})
		var weatherRows []models.RegionalWeatherAgg
		weatherCursor.All(ctx, &weatherRows)
		weatherCursor.Close(ctx)

		var avgTemp float64
		for _, w := range weatherRows {
			avgTemp += w.AvgTempC
		}
		if len(weatherRows) > 0 {
			avgTemp = roundTo2(avgTemp / float64(len(weatherRows)))
		}

		// Region health
		healthCursor, _ := dbClient.RegionHealth().Find(ctx, bson.M{})
		var healthRows []models.RegionHealth
		healthCursor.All(ctx, &healthRows)
		healthCursor.Close(ctx)

		var healthyCount, degradedCount, downCount int
		for _, h := range healthRows {
			switch h.Status {
			case models.StatusHealthy:
				healthyCount++
			case models.StatusDegraded:
				degradedCount++
			case models.StatusDown:
				downCount++
			}
		}

		// Current routing
		rtr := router.New(dbClient, 3600, log)
		currentRouting, _ := rtr.Current(ctx)

		summary := map[string]interface{}{
			"generated_at":          time.Now().UTC(),
			"event_type_count":      eventTypeCount,
			"total_github_events":   totalEvents,
			"weather_region_count":  len(weatherRows),
			"avg_regional_temp_c":   avgTemp,
			"health_region_count":   len(healthRows),
			"healthy_region_count":  healthyCount,
			"degraded_region_count": degradedCount,
			"down_region_count":     downCount,
			"data_quality_reminders": []string{
				"org_login can be null for personal GitHub repositories",
				"weather sensor zero/null values should be flagged before aggregation",
				"routing decisions depend on fresh region_health rows",
			},
		}
		if err == nil {
			summary["top_event_type"] = topEvent
		}
		if currentRouting != nil {
			summary["current_routing"] = currentRouting
		}

		writeJSON(w, summary)
		apiRequests.WithLabelValues("/api/summary", "GET", "200").Inc()
	}
}

func makeEventTypesHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cursor, err := dbClient.EventTypeCounts().Find(ctx, bson.M{},
			options.Find().SetSort(bson.D{{Key: "count", Value: -1}}),
		)
		if err != nil {
			http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)
		var rows []models.EventTypeCount
		cursor.All(ctx, &rows)
		writeJSON(w, rows)
		apiRequests.WithLabelValues("/api/events/types", "GET", "200").Inc()
	}
}

func makeWeatherRegionsHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cursor, err := dbClient.RegionalWeatherAgg().Find(ctx, bson.M{},
			options.Find().SetSort(bson.D{{Key: "region", Value: 1}}),
		)
		if err != nil {
			http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)
		var rows []models.RegionalWeatherAgg
		cursor.All(ctx, &rows)
		writeJSON(w, rows)
		apiRequests.WithLabelValues("/api/weather/regions", "GET", "200").Inc()
	}
}

func makeRegionsHealthHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cursor, err := dbClient.RegionHealth().Find(ctx, bson.M{},
			options.Find().SetSort(bson.D{{Key: "region", Value: 1}}),
		)
		if err != nil {
			http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)
		var rows []models.RegionHealth
		cursor.All(ctx, &rows)
		writeJSON(w, rows)
		apiRequests.WithLabelValues("/api/regions/health", "GET", "200").Inc()
	}
}

func makeRoutingCurrentHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtr := router.New(dbClient, 3600, log)
		decision, err := rtr.Current(r.Context())
		if err != nil {
			http.Error(w, `{"error":"no routing decision"}`, http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, decision)
		apiRequests.WithLabelValues("/api/routing/current", "GET", "200").Inc()
	}
}

func makeRoutingRowsHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtr := router.New(dbClient, 3600, log)

		decision, err := rtr.Current(r.Context())
		if err != nil {
			http.Error(w, `{"error":"no routing decision"}`, http.StatusServiceUnavailable)
			return
		}

		rows := []map[string]interface{}{
			{
				"decision_id":    decision.DecisionID,
				"routing_role":   "Preferred",
				"region":         decision.PreferredRegion,
				"status":         decision.PreferredStatus,
				"latency_ms":     decision.PreferredLatencyMs,
				"reason":         decision.Reason,
				"healthy_count":  decision.HealthyCount,
				"degraded_count": decision.DegradedCount,
				"down_count":     decision.DownCount,
				"stale_count":    decision.StaleCount,
				"decided_at":     decision.DecidedAt,
			},
			{
				"decision_id":    decision.DecisionID,
				"routing_role":   "Fallback",
				"region":         decision.FallbackRegion,
				"status":         decision.FallbackStatus,
				"latency_ms":     decision.FallbackLatencyMs,
				"reason":         decision.Reason,
				"healthy_count":  decision.HealthyCount,
				"degraded_count": decision.DegradedCount,
				"down_count":     decision.DownCount,
				"stale_count":    decision.StaleCount,
				"decided_at":     decision.DecidedAt,
			},
		}

		writeJSON(w, rows)
		apiRequests.WithLabelValues("/api/routing/current/rows", "GET", "200").Inc()
	}
}

func makeRouterRefreshHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtr := router.New(dbClient, 3600, log)
		decision, err := rtr.Run(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, decision)
		apiRequests.WithLabelValues("/api/router/refresh", "POST", "200").Inc()
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func roundTo2(f float64) float64 {
	return float64(int(f*100)) / 100
}

// makeActivityCategoriesHandler serves RPT-02 — groups event types into
// five developer activity categories for the Grafana activity composition dashboard.
func makeActivityCategoriesHandler(dbClient *db.Client, log *zap.Logger) http.HandlerFunc {
	// Maps each GitHub event type to a human-readable activity category.
	categoryMap := map[string]string{
		"PushEvent":                     "Code Change",
		"CreateEvent":                   "Code Change",
		"DeleteEvent":                   "Code Change",
		"IssuesEvent":                   "Issue Collaboration",
		"IssueCommentEvent":             "Issue Collaboration",
		"PullRequestEvent":              "Code Review",
		"PullRequestReviewEvent":        "Code Review",
		"PullRequestReviewCommentEvent": "Code Review",
		"ForkEvent":                     "Repository Activity",
		"WatchEvent":                    "Repository Activity",
		"ReleaseEvent":                  "Repository Activity",
		"MemberEvent":                   "Repository Activity",
		"CommitCommentEvent":            "Community / Watch",
		"GollumEvent":                   "Community / Watch",
		"PublicEvent":                   "Community / Watch",
		"DiscussionEvent":               "Community / Watch",
	}

	type categoryRow struct {
		ActivityCategory string  `json:"activity_category"`
		Count            int64   `json:"count"`
		Percentage       float64 `json:"percentage"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cursor, err := dbClient.EventTypeCounts().Find(ctx, bson.M{},
			options.Find().SetSort(bson.D{{Key: "count", Value: -1}}),
		)
		if err != nil {
			http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)

		var rows []models.EventTypeCount
		cursor.All(ctx, &rows)

		// Aggregate counts per category
		catCounts := map[string]int64{}
		var total int64
		for _, row := range rows {
			cat, ok := categoryMap[row.EventType]
			if !ok {
				cat = "Other"
			}
			catCounts[cat] += row.Count
			total += row.Count
		}

		// Build ordered result
		order := []string{"Code Change", "Issue Collaboration", "Code Review", "Repository Activity", "Community / Watch", "Other"}
		var result []categoryRow
		for _, cat := range order {
			count, ok := catCounts[cat]
			if !ok || count == 0 {
				continue
			}
			pct := 0.0
			if total > 0 {
				pct = roundTo2(float64(count) / float64(total) * 100)
			}
			result = append(result, categoryRow{
				ActivityCategory: cat,
				Count:            count,
				Percentage:       pct,
			})
		}

		writeJSON(w, result)
		apiRequests.WithLabelValues("/api/events/activity-categories", "GET", "200").Inc()
	}
}
