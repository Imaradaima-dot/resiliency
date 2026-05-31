// Package config loads all environment variables once at startup and exposes
// a typed Config struct. Every service imports this package rather than
// calling os.Getenv scattered across the codebase.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the application.
type Config struct {
	// MongoDB
	MongoURI    string
	MongoDBName string

	// External APIs
	GitHubToken string
	OWMAPIKey   string

	// Service ports
	IngestorPort    int
	TransformerPort int
	HealthCheckPort int
	RouterPort      int
	APIPort         int

	// Deployment identity
	Region string

	// Scheduler intervals (seconds)
	IngestorInterval    int
	TransformerInterval int
	AggregatorInterval  int
	HealthCheckInterval int
}

// Load reads .env (if present) then environment variables, validates required
// fields, and returns a populated Config. It is safe to call multiple times —
// subsequent calls re-read the environment.
func Load() (*Config, error) {
	// Load .env file if it exists — silently skip if absent (production uses
	// real env vars injected by k8s Secrets / Docker Compose env_file).
	_ = godotenv.Load()

	cfg := &Config{
		MongoURI:    required("MONGODB_URI"),
		MongoDBName: withDefault("MONGODB_DB_NAME", "resiliency"),

		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		OWMAPIKey:   os.Getenv("OWM_API_KEY"),

		IngestorPort:    intEnv("INGESTOR_PORT", 8081),
		TransformerPort: intEnv("TRANSFORMER_PORT", 8082),
		HealthCheckPort: intEnv("HEALTHCHECK_PORT", 8083),
		RouterPort:      intEnv("ROUTER_PORT", 8084),
		APIPort:         intEnv("API_PORT", 8080),

		Region: withDefault("REGION", "us-east1"),

		IngestorInterval:    intEnv("INGESTOR_INTERVAL_SECONDS", 300),
		TransformerInterval: intEnv("TRANSFORMER_INTERVAL_SECONDS", 600),
		AggregatorInterval:  intEnv("AGGREGATOR_INTERVAL_SECONDS", 900),
		HealthCheckInterval: intEnv("HEALTHCHECK_INTERVAL_SECONDS", 30),
	}

	if cfg.MongoURI == "" {
		return nil, fmt.Errorf("MONGODB_URI is required but not set")
	}

	return cfg, nil
}

// MustLoad calls Load and panics on error. Use only in main().
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(fmt.Sprintf("config: %v", err))
	}
	return cfg
}

func required(key string) string {
	return os.Getenv(key)
}

func withDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
