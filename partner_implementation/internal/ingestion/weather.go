package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/models"
)

// WeatherCollector fetches current weather only for the cities assigned
// to this region's workload — no other ingestor will fetch the same cities.
//
//	us-east  → Atlanta, Miami, Washington, New York, Boston         (5 cities)
//	us-west  → Los Angeles, San Francisco, Seattle, Denver, Phoenix  (5 cities)
//	europe   → London, Paris, Berlin, Amsterdam, Stockholm,          (10 cities)
//	           Tokyo, Seoul, Singapore, Mumbai, Shanghai
type WeatherCollector struct {
	cfg    *config.Config
	client *http.Client
}

// NewWeatherCollector returns a ready-to-use WeatherCollector.
func NewWeatherCollector(cfg *config.Config) *WeatherCollector {
	return &WeatherCollector{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// WeatherResult bundles a city-level result for the goroutine channel.
type WeatherResult struct {
	Weather models.RawWeather
	City    string
	Err     error
}

// Collect fetches weather for only the cities in cfg.Workload.WeatherCities.
func (w *WeatherCollector) Collect(ctx context.Context) ([]models.RawWeather, error) {
	if w.cfg.OfflineMode {
		return w.loadFromDisk()
	}
	if w.cfg.OWMKey == "" {
		return nil, fmt.Errorf("weather[%s]: OWM_API_KEY not set", w.cfg.Region)
	}

	cities := w.cfg.Workload.WeatherCities
	results := make(chan WeatherResult, len(cities))
	var wg sync.WaitGroup
	now := time.Now().UTC()

	for _, city := range cities {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			rec, err := w.fetchCity(ctx, c)
			if err != nil {
				results <- WeatherResult{City: c, Err: err}
				return
			}
			rec.IngestedAt = now
			results <- WeatherResult{Weather: *rec, City: c}
		}(city)
	}

	go func() { wg.Wait(); close(results) }()

	var all []models.RawWeather
	var errs int
	for r := range results {
		if r.Err != nil {
			errs++
			fmt.Printf("[weather][%s] %s: %v\n", w.cfg.Region, r.City, r.Err)
			continue
		}
		all = append(all, r.Weather)
	}

	fmt.Printf("[weather][%s] cities=%d  fetched=%d  errors=%d\n",
		w.cfg.Region, len(cities), len(all), errs)

	if len(all) == 0 {
		return nil, fmt.Errorf("weather[%s]: all city requests failed", w.cfg.Region)
	}

	if err := w.saveToDisk(all, now); err != nil {
		fmt.Printf("[weather][%s] WARNING: snapshot save failed: %v\n", w.cfg.Region, err)
	}
	return all, nil
}

func (w *WeatherCollector) fetchCity(ctx context.Context, city string) (*models.RawWeather, error) {
	params := url.Values{}
	params.Set("q", city)
	params.Set("appid", w.cfg.OWMKey)
	fullURL := "https://api.openweathermap.org/data/2.5/weather?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var rec models.RawWeather
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (w *WeatherCollector) saveToDisk(records []models.RawWeather, ts time.Time) error {
	dir := filepath.Join(w.cfg.RawDataDir, w.cfg.Region)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	filename := filepath.Join(dir, fmt.Sprintf("weather_%s.json", ts.Format("20060102_150405")))
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(records)
}

func (w *WeatherCollector) loadFromDisk() ([]models.RawWeather, error) {
	dir := filepath.Join(w.cfg.RawDataDir, w.cfg.Region)
	matches, err := filepath.Glob(filepath.Join(dir, "weather_*.json"))
	if err != nil || len(matches) == 0 {
		matches, err = filepath.Glob(filepath.Join(w.cfg.RawDataDir, "weather_*.json"))
		if err != nil || len(matches) == 0 {
			return nil, fmt.Errorf("weather[%s] offline: no snapshots", w.cfg.Region)
		}
	}
	latest := matches[len(matches)-1]
	fmt.Printf("[weather][%s] offline — loading %s\n", w.cfg.Region, latest)
	f, err := os.Open(latest)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var records []models.RawWeather
	if err := json.NewDecoder(f).Decode(&records); err != nil {
		return nil, err
	}
	return records, nil
}
