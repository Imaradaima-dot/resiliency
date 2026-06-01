// Package ingestion implements concurrent data collectors for the
// GitHub Events API and OpenWeatherMap API.
package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/resiliency/global-service/config"
	"github.com/resiliency/global-service/internal/models"
)

// GitHubCollector fetches public GitHub events for the pages assigned
// to this region's workload (e.g. us-east owns pages 1-3, us-west 4-6).
type GitHubCollector struct {
	cfg    *config.Config
	client *http.Client
}

// NewGitHubCollector returns a ready-to-use GitHubCollector.
func NewGitHubCollector(cfg *config.Config) *GitHubCollector {
	return &GitHubCollector{
		cfg:    cfg,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// CollectResult bundles a page-level result for the goroutine channel.
type CollectResult struct {
	Events []models.RawGitHubEvent
	Page   int
	Err    error
}

// Collect fetches the pages assigned to this region concurrently.
// Each region ingestor owns a non-overlapping slice of pages so the
// GitHub API quota is shared evenly and no events are written twice.
//
//	us-east  → pages 1-3
//	us-west  → pages 4-6
//	europe   → pages 7-9
func (g *GitHubCollector) Collect(ctx context.Context) ([]models.RawGitHubEvent, error) {
	if g.cfg.OfflineMode {
		return g.loadFromDisk()
	}

	pages := g.cfg.GitHubPages() // e.g. [1, 2, 3] for us-east
	results := make(chan CollectResult, len(pages))
	var wg sync.WaitGroup

	for _, page := range pages {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			events, err := g.fetchPage(ctx, p)
			results <- CollectResult{Events: events, Page: p, Err: err}
		}(page)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	now := time.Now().UTC()
	var all []models.RawGitHubEvent
	var pageErrors int

	for r := range results {
		if r.Err != nil {
			pageErrors++
			fmt.Printf("[github][%s] page %d error: %v\n", g.cfg.Region, r.Page, r.Err)
			continue
		}
		for i := range r.Events {
			r.Events[i].IngestedAt = now
		}
		all = append(all, r.Events...)
	}

	fmt.Printf("[github][%s] pages=%v  events=%d  errors=%d\n",
		g.cfg.Region, pages, len(all), pageErrors)
	if len(all) > 0 {
		fmt.Printf("[github][%s] sample created_at after JSON decode: event_id=%s  created_at=%s  zero=%v\n",
			g.cfg.Region, all[0].EventID, all[0].CreatedAt.Format(time.RFC3339), all[0].CreatedAt.IsZero())
	}

	if len(all) == 0 && pageErrors == len(pages) {
		return nil, fmt.Errorf("github[%s]: all pages failed", g.cfg.Region)
	}

	if err := g.saveToDisk(all, now); err != nil {
		fmt.Printf("[github][%s] WARNING: snapshot save failed: %v\n", g.cfg.Region, err)
	}

	return all, nil
}

// fetchPage makes a single paginated GET request to the GitHub Events API.
func (g *GitHubCollector) fetchPage(ctx context.Context, page int) ([]models.RawGitHubEvent, error) {
	url := fmt.Sprintf("https://api.github.com/events?per_page=%d&page=%d",
		g.cfg.GitHubPerPage, page)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "global-service-resiliency/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if g.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "token "+g.cfg.GitHubToken)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining != "" {
		fmt.Printf("[github][%s] page=%d  rate_remaining=%s\n", g.cfg.Region, page, remaining)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}

	var events []models.RawGitHubEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return events, nil
}

func (g *GitHubCollector) saveToDisk(events []models.RawGitHubEvent, ts time.Time) error {
	dir := filepath.Join(g.cfg.RawDataDir, g.cfg.Region)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	filename := filepath.Join(dir, fmt.Sprintf("github_events_%s.json", ts.Format("20060102_150405")))
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(events)
}

func (g *GitHubCollector) loadFromDisk() ([]models.RawGitHubEvent, error) {
	dir := filepath.Join(g.cfg.RawDataDir, g.cfg.Region)
	matches, err := filepath.Glob(filepath.Join(dir, "github_events_*.json"))
	if err != nil || len(matches) == 0 {
		// Fall back to root raw dir
		matches, err = filepath.Glob(filepath.Join(g.cfg.RawDataDir, "github_events_*.json"))
		if err != nil || len(matches) == 0 {
			return nil, fmt.Errorf("github[%s] offline: no snapshots found", g.cfg.Region)
		}
	}
	latest := matches[len(matches)-1]
	fmt.Printf("[github][%s] offline mode — loading %s\n", g.cfg.Region, latest)
	f, err := os.Open(latest)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []models.RawGitHubEvent
	if err := json.NewDecoder(f).Decode(&events); err != nil {
		return nil, err
	}
	return events, nil
}
