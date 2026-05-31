// Package weather implements concurrent ingestion from the OpenWeatherMap API.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

const owmCurrentURL = "https://api.openweathermap.org/data/2.5/weather"

// Cities defines the 20 cities across 4 regions used in the project.
var Cities = map[string][]string{
	"us-east": {"Atlanta", "Miami", "Washington", "New York", "Boston"},
	"us-west": {"Los Angeles", "San Francisco", "Seattle", "Denver", "Phoenix"},
	"europe":  {"London", "Paris", "Berlin", "Amsterdam", "Madrid"},
	"asia":    {"Tokyo", "Singapore", "Mumbai", "Seoul", "Sydney"},
}

// Collector fetches current weather for all cities concurrently and writes to raw_weather.
type Collector struct {
	client *db.Client
	apiKey string
	region string
	log    *zap.Logger
}

// NewCollector creates a Collector. apiKey must not be empty.
func NewCollector(client *db.Client, apiKey, region string, log *zap.Logger) *Collector {
	return &Collector{
		client: client,
		apiKey: apiKey,
		region: region,
		log:    log,
	}
}

// Run fetches all 20 cities concurrently and upserts to raw_weather.
// Returns the number of records written.
func (c *Collector) Run(ctx context.Context) (int, error) {
	batchID := fmt.Sprintf("owm-%d", time.Now().UnixMilli())
	ingested := time.Now().UTC()

	type result struct {
		cityRegion string
		city       string
		record     *models.RawWeather
		err        error
	}

	total := 0
	for _, cities := range Cities {
		total += len(cities)
	}

	results := make(chan result, total)
	var wg sync.WaitGroup

	for cityRegion, cities := range Cities {
		for _, city := range cities {
			wg.Add(1)
			go func(cr, ct string) {
				defer wg.Done()
				rec, err := c.fetchCity(ctx, cr, ct, batchID, ingested)
				results <- result{cityRegion: cr, city: ct, record: rec, err: err}
			}(cityRegion, city)
		}
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var records []*models.RawWeather
	var fetchErrors int
	for r := range results {
		if r.err != nil {
			fetchErrors++
			c.log.Warn("weather fetch error",
				zap.String("city", r.city),
				zap.Error(r.err),
			)
			continue
		}
		records = append(records, r.record)
	}

	c.log.Info("weather fetch complete",
		zap.Int("records", len(records)),
		zap.Int("errors", fetchErrors),
		zap.String("batch_id", batchID),
	)

	written, err := c.upsertRecords(ctx, records)
	if err != nil {
		return 0, err
	}
	return written, nil
}

// fetchCity retrieves current weather for a single city.
func (c *Collector) fetchCity(ctx context.Context, region, city, batchID string, ingested time.Time) (*models.RawWeather, error) {
	u := fmt.Sprintf("%s?q=%s&appid=%s", owmCurrentURL,
		fmt.Sprintf("%s", encodeCity(city)), c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OWM status %d for city %q", resp.StatusCode, city)
	}

	var raw owmResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode city %q: %w", city, err)
	}

	weather := []models.OWMWeatherDesc{}
	for _, w := range raw.Weather {
		weather = append(weather, models.OWMWeatherDesc{
			ID:          w.ID,
			Main:        w.Main,
			Description: w.Description,
			Icon:        w.Icon,
		})
	}

	return &models.RawWeather{
		CityID:   raw.ID,
		CityName: raw.Name,
		Country:  raw.Sys.Country,
		Region:   region,
		Coord: models.OWMCoord{
			Lon: raw.Coord.Lon,
			Lat: raw.Coord.Lat,
		},
		Main: models.OWMMain{
			Temp:      raw.Main.Temp,
			FeelsLike: raw.Main.FeelsLike,
			TempMin:   raw.Main.TempMin,
			TempMax:   raw.Main.TempMax,
			Pressure:  raw.Main.Pressure,
			Humidity:  raw.Main.Humidity,
		},
		Wind: models.OWMWind{
			Speed: raw.Wind.Speed,
			Deg:   raw.Wind.Deg,
			Gust:  raw.Wind.Gust,
		},
		Clouds: models.OWMClouds{
			All: raw.Clouds.All,
		},
		Weather:    weather,
		Visibility: raw.Visibility,
		Dt:         raw.Dt,
		IngestedAt: ingested,
		BatchID:    batchID,
	}, nil
}

// upsertRecords writes weather records to raw_weather with idempotent upserts
// keyed on city_name + dt (observation timestamp).
func (c *Collector) upsertRecords(ctx context.Context, records []*models.RawWeather) (int, error) {
	coll := c.client.RawWeather()
	var written int

	for _, rec := range records {
		filter := bson.M{
			"city_name": rec.CityName,
			"dt":        rec.Dt,
		}
		update := bson.M{"$setOnInsert": rec}
		opts := options.Update().SetUpsert(true)

		res, err := coll.UpdateOne(ctx, filter, update, opts)
		if err != nil {
			return written, fmt.Errorf("upsert weather %s: %w", rec.CityName, err)
		}
		if res.UpsertedCount > 0 {
			written++
		}
	}
	return written, nil
}

// EnsureIndexes creates indexes on raw_weather if they don't exist.
func EnsureIndexes(ctx context.Context, client *db.Client) error {
	coll := client.RawWeather()
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "city_name", Value: 1}, {Key: "dt", Value: -1}},
			Options: options.Index().SetUnique(true).SetName("city_dt_unique"),
		},
		{
			Keys:    bson.D{{Key: "region", Value: 1}, {Key: "ingested_at", Value: -1}},
			Options: options.Index().SetName("region_ingested"),
		},
	}
	_, err := coll.Indexes().CreateMany(ctx, indexes)
	return err
}

// encodeCity URL-encodes a city name so multi-word names like "New York" work
// correctly in OWM API calls. This is the fix carried over from Phase 1.
func encodeCity(city string) string {
	return url.QueryEscape(city)
}

// ---------------------------------------------------------------------------
// OWM API response types — private
// ---------------------------------------------------------------------------

type owmResponse struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Coord      owmCoord     `json:"coord"`
	Main       owmMain      `json:"main"`
	Wind       owmWind      `json:"wind"`
	Clouds     owmClouds    `json:"clouds"`
	Weather    []owmWeather `json:"weather"`
	Visibility int          `json:"visibility"`
	Dt         int64        `json:"dt"`
	Sys        owmSys       `json:"sys"`
}

type owmCoord struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
}

type owmMain struct {
	Temp      float64 `json:"temp"`
	FeelsLike float64 `json:"feels_like"`
	TempMin   float64 `json:"temp_min"`
	TempMax   float64 `json:"temp_max"`
	Pressure  int     `json:"pressure"`
	Humidity  int     `json:"humidity"`
}

type owmWind struct {
	Speed float64 `json:"speed"`
	Deg   int     `json:"deg"`
	Gust  float64 `json:"gust"`
}

type owmClouds struct {
	All int `json:"all"`
}

type owmWeather struct {
	ID          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type owmSys struct {
	Country string `json:"country"`
}
