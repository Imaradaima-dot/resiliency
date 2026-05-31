package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ---------------------------------------------------------------------------
// Processed zone — resiliency_processed
// ---------------------------------------------------------------------------

// GitHubEventFlat is a cleaned, flattened GitHub event stored in
// github_events_flat. Actor and repo are promoted to top-level fields.
// Enriched fields (IsBot, Hour, DayOfWeek) are derived during transformation.
type GitHubEventFlat struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	EventID     string             `bson:"event_id"`
	Type        string             `bson:"type"`
	ActorLogin  string             `bson:"actor_login"`
	ActorID     int64              `bson:"actor_id"`
	IsBot       bool               `bson:"is_bot"`
	RepoName    string             `bson:"repo_name"`
	RepoID      int64              `bson:"repo_id"`
	OrgLogin    *string            `bson:"org_login"` // nil when personal repo
	Public      bool               `bson:"public"`
	CreatedAt   time.Time          `bson:"created_at"`
	Hour        int                `bson:"hour"`         // 0-23 UTC
	DayOfWeek   int                `bson:"day_of_week"`  // 0=Sunday
	QualityFlag string             `bson:"quality_flag"` // OK | MISSING_ORG
	ProcessedAt time.Time          `bson:"processed_at"`
	Region      string             `bson:"region"`
	BatchID     string             `bson:"batch_id"`
}

// WeatherFlat is a cleaned, enriched weather record stored in weather_flat.
// Temperatures are converted from Kelvin to Celsius. Zero-value wind speeds
// and invalid visibility readings are flagged rather than dropped.
type WeatherFlat struct {
	ID           primitive.ObjectID `bson:"_id,omitempty"`
	CityName     string             `bson:"city_name"`
	Country      string             `bson:"country"`
	Region       string             `bson:"region"`
	Latitude     float64            `bson:"latitude"`
	Longitude    float64            `bson:"longitude"`
	TemperatureC float64            `bson:"temperature_c"`
	FeelsLikeC   float64            `bson:"feels_like_c"`
	Humidity     int                `bson:"humidity"`
	WindSpeed    float64            `bson:"wind_speed"`
	Condition    string             `bson:"condition"`
	Visibility   int                `bson:"visibility"`
	ObservedAt   time.Time          `bson:"observed_at"`
	Hour         int                `bson:"hour"`
	QualityFlag  string             `bson:"quality_flag"` // OK | ZERO_SENSOR_VALUE | CAPPED_VISIBILITY
	ProcessedAt  time.Time          `bson:"processed_at"`
	BatchID      string             `bson:"batch_id"`
}

// QualityFlag values
const (
	QualityOK               = "OK"
	QualityMissingOrg       = "MISSING_ORG"
	QualityZeroSensorValue  = "ZERO_SENSOR_VALUE"
	QualityCappedVisibility = "CAPPED_VISIBILITY"
)

// ---------------------------------------------------------------------------
// Serving zone — resiliency_serving
// ---------------------------------------------------------------------------

// EventTypeCount holds aggregated event-type distribution for a time window.
// Stored in event_type_counts. Refreshed by the aggregator every 15 minutes.
type EventTypeCount struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	EventType   string             `bson:"event_type" json:"event_type"`
	Count       int64              `bson:"count" json:"count"`
	Percentage  float64            `bson:"percentage" json:"percentage"`
	WindowStart time.Time          `bson:"window_start" json:"window_start"`
	WindowEnd   time.Time          `bson:"window_end" json:"window_end"`
	ComputedAt  time.Time          `bson:"computed_at" json:"computed_at"`
	Region      string             `bson:"region" json:"region"`
}

// RegionalWeatherAgg holds pre-aggregated weather metrics per region.
// Stored in regional_weather_agg. Refreshed by the aggregator every 15 minutes.
type RegionalWeatherAgg struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Region        string             `bson:"region" json:"region"`
	CityCount     int                `bson:"city_count" json:"city_count"`
	AvgTempC      float64            `bson:"avg_temp_c" json:"avg_temp_c"`
	AvgHumidity   float64            `bson:"avg_humidity" json:"avg_humidity"`
	AvgWindSpeed  float64            `bson:"avg_wind_speed" json:"avg_wind_speed"`
	ObservationTs time.Time          `bson:"observation_ts" json:"observation_ts"`
	ComputedAt    time.Time          `bson:"computed_at" json:"computed_at"`
}

// RegionHealth records the health of a single deployment region.
// Written by the Health-Check service every 30 seconds.
// Read by the Traffic Routing service to make routing decisions.
type RegionHealth struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Region           string             `bson:"region" json:"region"`
	Status           string             `bson:"status" json:"status"` // healthy | degraded | down
	LatencyMs        int64              `bson:"latency_ms" json:"latency_ms"`
	ReplicationLagMs int64              `bson:"replication_lag_ms" json:"replication_lag_ms"`
	WriteConcern     string             `bson:"write_concern" json:"write_concern"`
	ReadPreference   string             `bson:"read_preference" json:"read_preference"`
	LastCheck        time.Time          `bson:"last_check" json:"last_check"`
	CheckedAt        time.Time          `bson:"checked_at" json:"checked_at"`
}

// RegionStatus values
const (
	StatusHealthy  = "healthy"
	StatusDegraded = "degraded"
	StatusDown     = "down"
)
