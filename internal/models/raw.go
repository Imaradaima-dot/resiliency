// Package models defines the typed structs for every MongoDB collection.
// Raw zone structs mirror the upstream API payloads as closely as possible.
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ---------------------------------------------------------------------------
// Raw zone — resiliency_raw
// ---------------------------------------------------------------------------

// RawGitHubEvent is a single event from the GitHub Events API stored verbatim
// in raw_github_events. The Payload field holds the event-type-specific JSON
// as a raw document to avoid schema lock-in at ingest time.
type RawGitHubEvent struct {
	ID         primitive.ObjectID     `bson:"_id,omitempty"`
	EventID    string                 `bson:"event_id"`
	Type       string                 `bson:"type"`
	Actor      GitHubActor            `bson:"actor"`
	Repo       GitHubRepo             `bson:"repo"`
	Org        *GitHubOrg             `bson:"org,omitempty"`
	Public     bool                   `bson:"public"`
	Payload    map[string]interface{} `bson:"payload"`
	CreatedAt  time.Time              `bson:"created_at"`
	IngestedAt time.Time              `bson:"ingested_at"`
	BatchID    string                 `bson:"batch_id"`
	Region     string                 `bson:"region"`
}

type GitHubActor struct {
	ID           int64  `bson:"id"`
	Login        string `bson:"login"`
	DisplayLogin string `bson:"display_login,omitempty"`
	GravatarID   string `bson:"gravatar_id,omitempty"`
	URL          string `bson:"url,omitempty"`
	AvatarURL    string `bson:"avatar_url,omitempty"`
}

type GitHubRepo struct {
	ID   int64  `bson:"id"`
	Name string `bson:"name"`
	URL  string `bson:"url,omitempty"`
}

type GitHubOrg struct {
	ID         int64  `bson:"id"`
	Login      string `bson:"login"`
	GravatarID string `bson:"gravatar_id,omitempty"`
	URL        string `bson:"url,omitempty"`
	AvatarURL  string `bson:"avatar_url,omitempty"`
}

// RawWeather is a single city weather record from the OpenWeatherMap API
// stored verbatim in raw_weather. Kelvin temperatures are preserved as-received
// and converted to Celsius only during transformation.
type RawWeather struct {
	ID         primitive.ObjectID     `bson:"_id,omitempty"`
	CityID     int64                  `bson:"city_id"`
	CityName   string                 `bson:"city_name"`
	Country    string                 `bson:"country"`
	Region     string                 `bson:"region"`
	Coord      OWMCoord               `bson:"coord"`
	Main       OWMMain                `bson:"main"`
	Wind       OWMWind                `bson:"wind"`
	Clouds     OWMClouds              `bson:"clouds"`
	Weather    []OWMWeatherDesc       `bson:"weather"`
	Visibility int                    `bson:"visibility"`
	Extra      map[string]interface{} `bson:"extra,omitempty"`
	Dt         int64                  `bson:"dt"`
	IngestedAt time.Time              `bson:"ingested_at"`
	BatchID    string                 `bson:"batch_id"`
}

type OWMCoord struct {
	Lon float64 `bson:"lon"`
	Lat float64 `bson:"lat"`
}

type OWMMain struct {
	Temp      float64 `bson:"temp"`       // Kelvin
	FeelsLike float64 `bson:"feels_like"` // Kelvin
	TempMin   float64 `bson:"temp_min"`   // Kelvin
	TempMax   float64 `bson:"temp_max"`   // Kelvin
	Pressure  int     `bson:"pressure"`   // hPa
	Humidity  int     `bson:"humidity"`   // %
}

type OWMWind struct {
	Speed float64 `bson:"speed"` // m/s
	Deg   int     `bson:"deg"`
	Gust  float64 `bson:"gust,omitempty"`
}

type OWMClouds struct {
	All int `bson:"all"` // %
}

type OWMWeatherDesc struct {
	ID          int    `bson:"id"`
	Main        string `bson:"main"`
	Description string `bson:"description"`
	Icon        string `bson:"icon"`
}
