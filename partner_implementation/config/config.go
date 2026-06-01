// Package config loads all runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RegionNode represents a named MongoDB node for per-region health checks.
type RegionNode struct {
	Region string // e.g. "us-east"
	Host   string // e.g. "mongo-us-east"
	Port   string // e.g. "27017"
}

// RegionWorkload defines what slice of work this region's ingestor owns.
// GitHub pages and weather cities are partitioned so each ingestor fetches
// a non-overlapping slice — no duplicate writes, no wasted API quota.
type RegionWorkload struct {
	GitHubPageStart int      // first page to fetch (1-based, inclusive)
	GitHubPageEnd   int      // last page to fetch (inclusive)
	WeatherCities   []string // cities this region is responsible for
}

// Config holds all application configuration.
type Config struct {
	// MongoDB
	MongoURI      string
	MongoDatabase string
	WriteConcern  string // "majority" | "1" | "2"
	ReadPref      string // "nearest" | "primary" | "secondaryPreferred"

	// This service's region tag (set per-container in docker-compose).
	Region string // "us-east" | "us-west" | "europe" | "asia"

	// Work owned by this ingestor instance (derived from Region).
	Workload RegionWorkload

	// Per-region node list for health checker to ping individually.
	RegionNodes []RegionNode

	// GitHub Events API
	GitHubToken   string
	GitHubPerPage int

	// OpenWeatherMap API
	OWMKey string

	// Health-check
	HealthInterval time.Duration
	HealthPort     string

	// Ingestion
	IngestInterval time.Duration
	OfflineMode    bool
	RawDataDir     string

	// Region → cities mapping (full list, used for lookups)
	Regions map[string][]string
}

// DefaultCities maps deployment regions to representative cities.
var DefaultCities = map[string][]string{
	"us-east": {"Atlanta", "Miami", "Washington", "New York", "Boston"},
	"us-west": {"Los Angeles", "San Francisco", "Seattle", "Denver", "Phoenix"},
	"europe":  {"London", "Paris", "Berlin", "Amsterdam", "Stockholm"},
	"asia":    {"Tokyo", "Seoul", "Singapore", "Mumbai", "Shanghai"},
}

// regionWorkloads partitions GitHub pages and weather cities across ingestors.
// Total GitHub pages = 9 (pages 1–9 across 3 ingestors).
// asia has no ingestor, so europe covers its 5 cities too.
var regionWorkloads = map[string]RegionWorkload{
	"us-east": {
		GitHubPageStart: 1,
		GitHubPageEnd:   3,
		WeatherCities:   DefaultCities["us-east"],
	},
	"us-west": {
		GitHubPageStart: 4,
		GitHubPageEnd:   6,
		WeatherCities:   DefaultCities["us-west"],
	},
	"europe": {
		GitHubPageStart: 7,
		GitHubPageEnd:   9,
		// europe ingestor also covers asia's cities since there is no ingestor-asia
		WeatherCities: append(DefaultCities["europe"], DefaultCities["asia"]...),
	},
}

// DefaultRegionNodes matches the docker-compose service names.
var DefaultRegionNodes = []RegionNode{
	{Region: "us-east", Host: "mongo-us-east", Port: "27017"},
	{Region: "us-west", Host: "mongo-us-west", Port: "27017"},
	{Region: "europe",  Host: "mongo-europe",  Port: "27017"},
	{Region: "asia",    Host: "mongo-asia",    Port: "27017"},
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	region := getEnv("REGION", "us-east")

	// Derive workload from region; fall back to full set if region unknown.
	workload, ok := regionWorkloads[region]
	if !ok {
		workload = RegionWorkload{GitHubPageStart: 1, GitHubPageEnd: 3, WeatherCities: DefaultCities["us-east"]}
	}

	// Allow env override of page range for flexibility.
	if v := getEnvInt("GITHUB_PAGE_START", 0); v > 0 {
		workload.GitHubPageStart = v
	}
	if v := getEnvInt("GITHUB_PAGE_END", 0); v > 0 {
		workload.GitHubPageEnd = v
	}
	// Allow override of weather cities.
	if raw := getEnv("OWM_CITIES", ""); raw != "" {
		workload.WeatherCities = strings.Split(raw, ",")
	}

	cfg := &Config{
		MongoURI:       getEnv("MONGO_URI", "mongodb://mongo-us-east:27017,mongo-us-west:27017,mongo-europe:27017,mongo-asia:27017/?replicaSet=rs0"),
		MongoDatabase:  getEnv("MONGO_DATABASE", "resiliency"),
		WriteConcern:   getEnv("MONGO_WRITE_CONCERN", "majority"),
		ReadPref:       getEnv("MONGO_READ_PREF", "nearest"),
		Region:         region,
		Workload:       workload,
		GitHubToken:    getEnv("GITHUB_TOKEN", ""),
		GitHubPerPage:  getEnvInt("GITHUB_PER_PAGE", 100),
		OWMKey:         getEnv("OWM_API_KEY", ""),
		HealthInterval: getEnvDuration("HEALTH_INTERVAL", 15*time.Second),
		HealthPort:     getEnv("HEALTH_PORT", "8080"),
		IngestInterval: getEnvDuration("INGEST_INTERVAL", 5*time.Minute),
		OfflineMode:    getEnvBool("OFFLINE_MODE", false),
		RawDataDir:     getEnv("RAW_DATA_DIR", "./data/raw"),
		Regions:        DefaultCities,
		RegionNodes:    DefaultRegionNodes,
	}

	// Parse REGION_NODES override if provided.
	if raw := getEnv("REGION_NODES", ""); raw != "" {
		cfg.RegionNodes = parseRegionNodes(raw)
	}

	if cfg.OWMKey == "" && !cfg.OfflineMode {
		fmt.Println("[config] WARNING: OWM_API_KEY not set — weather ingestion will be skipped")
	}

	fmt.Printf("[config] region=%-8s  github_pages=%d-%d  weather_cities=%d  write_concern=%s\n",
		cfg.Region,
		cfg.Workload.GitHubPageStart,
		cfg.Workload.GitHubPageEnd,
		len(cfg.Workload.WeatherCities),
		cfg.WriteConcern,
	)

	return cfg, nil
}

// RawDB returns the raw zone database name.
func (c *Config) RawDB() string { return c.MongoDatabase + "_raw" }

// ProcessedDB returns the processed zone database name.
func (c *Config) ProcessedDB() string { return c.MongoDatabase + "_processed" }

// ServingDB returns the serving zone database name.
func (c *Config) ServingDB() string { return c.MongoDatabase + "_serving" }

// RegionForCity returns the deployment region tag for a given city name.
func (c *Config) RegionForCity(city string) string {
	for region, cities := range c.Regions {
		for _, ct := range cities {
			if strings.EqualFold(ct, city) {
				return region
			}
		}
	}
	return "unknown"
}

// NodeURI returns a direct MongoDB URI for a single region node.
func (c *Config) NodeURI(n RegionNode) string {
	return fmt.Sprintf("mongodb://%s:%s/?directConnection=true", n.Host, n.Port)
}

// GitHubPages returns the slice of page numbers this region should fetch.
func (c *Config) GitHubPages() []int {
	var pages []int
	for p := c.Workload.GitHubPageStart; p <= c.Workload.GitHubPageEnd; p++ {
		pages = append(pages, p)
	}
	return pages
}

// ── helpers ──────────────────────────────────────────────────────────────────

func parseRegionNodes(raw string) []RegionNode {
	var out []RegionNode
	for _, part := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(part), ":", 3)
		if len(parts) != 3 {
			continue
		}
		out = append(out, RegionNode{Region: parts[0], Host: parts[1], Port: parts[2]})
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
