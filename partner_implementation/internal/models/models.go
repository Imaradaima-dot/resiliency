// Package models defines typed structs for all MongoDB collections across
// the three data zones: Raw, Processed, and Serving.
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// =============================================================================
// RAW ZONE  (resiliency_raw)
// =============================================================================

// RawGitHubEvent mirrors the GitHub Events API response exactly.
// The Payload field is stored as a flexible BSON Document because its
// structure varies by event Type (PushEvent, IssuesEvent, etc.).
type RawGitHubEvent struct {
	ID         primitive.ObjectID     `bson:"_id,omitempty"     json:"-"`
	EventID    string                 `bson:"event_id"          json:"id"`
	Type       string                 `bson:"type"              json:"type"`
	Actor      GitHubActor            `bson:"actor"             json:"actor"`
	Repo       GitHubRepo             `bson:"repo"              json:"repo"`
	Org        *GitHubOrg             `bson:"org,omitempty"     json:"org,omitempty"`
	Payload    map[string]interface{} `bson:"payload"           json:"payload"`
	Public     bool                   `bson:"public"            json:"public"`
	CreatedAt  time.Time              `bson:"created_at"        json:"created_at"`
	IngestedAt time.Time              `bson:"ingested_at"       json:"-"`
}

// GitHubActor represents the actor sub-document in a GitHub event.
type GitHubActor struct {
	ID          int64  `bson:"id"           json:"id"`
	Login       string `bson:"login"        json:"login"`
	DisplayLogin string `bson:"display_login,omitempty" json:"display_login,omitempty"`
	GravatarID  string `bson:"gravatar_id"  json:"gravatar_id"`
	URL         string `bson:"url"          json:"url"`
	AvatarURL   string `bson:"avatar_url"   json:"avatar_url"`
}

// GitHubRepo represents the repo sub-document in a GitHub event.
type GitHubRepo struct {
	ID   int64  `bson:"id"   json:"id"`
	Name string `bson:"name" json:"name"`
	URL  string `bson:"url"  json:"url"`
}

// GitHubOrg represents the org sub-document (nullable for personal repos).
type GitHubOrg struct {
	ID         int64  `bson:"id"          json:"id"`
	Login      string `bson:"login"       json:"login"`
	GravatarID string `bson:"gravatar_id" json:"gravatar_id"`
	URL        string `bson:"url"         json:"url"`
	AvatarURL  string `bson:"avatar_url"  json:"avatar_url"`
}

// IsBot returns true if the actor login ends with "[bot]".
func (e *RawGitHubEvent) IsBot() bool {
	login := e.Actor.Login
	return len(login) > 5 && login[len(login)-5:] == "[bot]"
}

// OrgLogin returns the org login or empty string if no org is attached.
func (e *RawGitHubEvent) OrgLogin() string {
	if e.Org != nil {
		return e.Org.Login
	}
	return ""
}

// -----------------------------------------------------------------------------

// RawWeather mirrors the OpenWeatherMap Current Weather API response.
type RawWeather struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	CityID     int64              `bson:"city_id"     json:"id"`
	Name       string             `bson:"name"        json:"name"`
	Coord      OWMCoord           `bson:"coord"       json:"coord"`
	Main       OWMMain            `bson:"main"        json:"main"`
	Wind       OWMWind            `bson:"wind"        json:"wind"`
	Clouds     OWMClouds          `bson:"clouds"      json:"clouds"`
	Visibility int                `bson:"visibility"  json:"visibility"`
	Dt         int64              `bson:"dt"          json:"dt"`
	Sys        OWMSys             `bson:"sys"         json:"sys"`
	Weather    []OWMCondition     `bson:"weather"     json:"weather"`
	IngestedAt time.Time          `bson:"ingested_at"`
}

type OWMCoord struct {
	Lon float64 `bson:"lon" json:"lon"`
	Lat float64 `bson:"lat" json:"lat"`
}

type OWMMain struct {
	Temp      float64 `bson:"temp"       json:"temp"`       // Kelvin
	FeelsLike float64 `bson:"feels_like" json:"feels_like"` // Kelvin
	TempMin   float64 `bson:"temp_min"   json:"temp_min"`
	TempMax   float64 `bson:"temp_max"   json:"temp_max"`
	Pressure  int     `bson:"pressure"   json:"pressure"`
	Humidity  int     `bson:"humidity"   json:"humidity"`
}

type OWMWind struct {
	Speed float64 `bson:"speed" json:"speed"`
	Deg   int     `bson:"deg"   json:"deg"`
	Gust  float64 `bson:"gust"  json:"gust"`
}

type OWMClouds struct {
	All int `bson:"all" json:"all"`
}

type OWMSys struct {
	Country string `bson:"country" json:"country"`
	Sunrise int64  `bson:"sunrise" json:"sunrise"`
	Sunset  int64  `bson:"sunset"  json:"sunset"`
}

type OWMCondition struct {
	ID          int    `bson:"id"          json:"id"`
	Main        string `bson:"main"        json:"main"`
	Description string `bson:"description" json:"description"`
	Icon        string `bson:"icon"        json:"icon"`
}

// PrimaryCondition returns the main condition string or "Unknown".
func (w *RawWeather) PrimaryCondition() string {
	if len(w.Weather) > 0 {
		return w.Weather[0].Main
	}
	return "Unknown"
}

// =============================================================================
// PROCESSED ZONE  (resiliency_processed)
// =============================================================================

// GitHubEventFlat is the flattened, enriched version of RawGitHubEvent.
// Nested fields are promoted to scalars; timestamps are enriched with
// hour-of-day and day-of-week for temporal analysis.
type GitHubEventFlat struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	EventID     string             `bson:"event_id"`
	Type        string             `bson:"type"`
	ActorLogin  string             `bson:"actor_login"`
	RepoName    string             `bson:"repo_name"`
	OrgLogin    string             `bson:"org_login"`    // empty string for personal repos
	IsBot       bool               `bson:"is_bot"`
	Hour        int                `bson:"hour"`         // 0-23
	DayOfWeek   string             `bson:"day_of_week"`  // "Monday" … "Sunday"
	CreatedAt   time.Time          `bson:"created_at"`
	ProcessedAt time.Time          `bson:"processed_at"`
	QualityFlag string             `bson:"quality_flag"` // "ok" | "missing_org"
}

// WeatherFlat is the flattened, enriched version of RawWeather.
// Temperature is converted from Kelvin to Celsius during transformation.
type WeatherFlat struct {
	ID            primitive.ObjectID `bson:"_id,omitempty"`
	CityName      string             `bson:"city_name"`
	Country       string             `bson:"country"`
	Region        string             `bson:"region"`        // deployment region tag
	Lat           float64            `bson:"lat"`
	Lon           float64            `bson:"lon"`
	TemperatureC  float64            `bson:"temperature_c"` // converted from Kelvin
	FeelsLikeC    float64            `bson:"feels_like_c"`
	Humidity      int                `bson:"humidity"`
	WindSpeed     float64            `bson:"wind_speed"`     // m/s
	Condition     string             `bson:"condition"`      // "Clear", "Rain", etc.
	Visibility    int                `bson:"visibility"`     // metres (10000 = API cap)
	CloudCover    int                `bson:"cloud_cover"`    // percentage
	Timestamp     time.Time          `bson:"timestamp"`
	ProcessedAt   time.Time          `bson:"processed_at"`
	QualityFlag   string             `bson:"quality_flag"`   // "ok" | "zero_wind" | "capped_visibility"
}

// =============================================================================
// SERVING ZONE  (resiliency_serving)
// =============================================================================

// EventTypeCount is pre-aggregated GitHub event type statistics for RPT-01.
type EventTypeCount struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	EventType  string             `bson:"event_type"`
	Count      int                `bson:"count"`
	Percentage float64            `bson:"percentage"`
	WindowStart time.Time         `bson:"window_start"`
	WindowEnd   time.Time         `bson:"window_end"`
}

// RegionalWeatherAgg is pre-aggregated weather statistics per region for RPT-03.
type RegionalWeatherAgg struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	Region      string             `bson:"region"`
	AvgTempC    float64            `bson:"avg_temp_c"`
	AvgHumidity float64            `bson:"avg_humidity"`
	AvgWindMS   float64            `bson:"avg_wind_ms"`
	CityCount   int                `bson:"city_count"`
	Timestamp   time.Time          `bson:"timestamp"`
}

// RegionHealth tracks the health state of each deployment region for RPT-04.
type RegionHealth struct {
	ID              primitive.ObjectID `bson:"_id,omitempty"`
	Region          string             `bson:"region"`
	Status          string             `bson:"status"`          // "healthy" | "degraded" | "down"
	LatencyMS       float64            `bson:"latency_ms"`
	ReplicationLagMS float64           `bson:"replication_lag_ms"`
	WriteConcern    string             `bson:"write_concern"`
	LastCheck       time.Time          `bson:"last_check"`
}
