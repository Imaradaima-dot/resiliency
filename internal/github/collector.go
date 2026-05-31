// Package github implements concurrent ingestion from the GitHub Events API.
// It is a production promotion of the Phase 1 EDA collector, extended to
// write each batch to MongoDB with idempotent upserts and full lineage metadata.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/resiliency/global/internal/db"
	"github.com/resiliency/global/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

const (
	githubAPIBase = "https://api.github.com/events"
	maxPages      = 10
)

// Collector fetches GitHub events concurrently and persists them to the raw zone.
type Collector struct {
	client *db.Client
	token  string
	region string
	log    *zap.Logger
}

// NewCollector creates a Collector. token may be empty for unauthenticated
// requests (60 req/hr); a personal access token raises the limit to 5,000/hr.
func NewCollector(client *db.Client, token, region string, log *zap.Logger) *Collector {
	return &Collector{
		client: client,
		token:  token,
		region: region,
		log:    log,
	}
}

// Run executes one full collection pass: fetches all pages concurrently,
// deduplicates, and upserts to MongoDB. Returns the number of new events written.
func (c *Collector) Run(ctx context.Context) (int, error) {
	batchID := fmt.Sprintf("gh-%d", time.Now().UnixMilli())
	ingested := time.Now().UTC()

	type result struct {
		page   int
		events []apiEvent
		err    error
	}

	results := make(chan result, maxPages)
	var wg sync.WaitGroup

	for page := 1; page <= maxPages; page++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			events, err := c.fetchPage(ctx, p)
			results <- result{page: p, events: events, err: err}
		}(page)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allEvents []apiEvent
	var pageErrors int
	for r := range results {
		if r.err != nil {
			pageErrors++
			c.log.Warn("github page error",
				zap.Int("page", r.page),
				zap.Error(r.err),
			)
			continue
		}
		allEvents = append(allEvents, r.events...)
	}

	c.log.Info("github fetch complete",
		zap.Int("total_events", len(allEvents)),
		zap.Int("page_errors", pageErrors),
		zap.String("batch_id", batchID),
	)

	if len(allEvents) == 0 {
		return 0, nil
	}

	written, err := c.upsertEvents(ctx, allEvents, batchID, ingested)
	if err != nil {
		return 0, fmt.Errorf("github collector upsert: %w", err)
	}
	return written, nil
}

// fetchPage retrieves a single page from the GitHub Events API.
func (c *Collector) fetchPage(ctx context.Context, page int) ([]apiEvent, error) {
	u := fmt.Sprintf("%s?page=%d&per_page=100", githubAPIBase, page)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var events []apiEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode page %d: %w", page, err)
	}
	return events, nil
}

// upsertEvents writes events to raw_github_events using idempotent upserts
// keyed on event_id so re-runs never create duplicates.
func (c *Collector) upsertEvents(ctx context.Context, events []apiEvent, batchID string, ingested time.Time) (int, error) {
	coll := c.client.RawGitHubEvents()
	var written int

	for _, e := range events {
		raw, err := toRaw(e, batchID, ingested, c.region)
		if err != nil {
			c.log.Warn("skipping malformed event",
				zap.String("event_id", e.ID),
				zap.Error(err),
			)
			continue
		}

		filter := bson.M{"event_id": raw.EventID}
		update := bson.M{"$setOnInsert": raw}
		opts := options.Update().SetUpsert(true)

		res, err := coll.UpdateOne(ctx, filter, update, opts)
		if err != nil {
			return written, fmt.Errorf("upsert event %s: %w", raw.EventID, err)
		}
		if res.UpsertedCount > 0 {
			written++
		}
	}
	return written, nil
}

// EnsureIndexes creates indexes on raw_github_events if they don't exist.
// Call once at service startup.
func EnsureIndexes(ctx context.Context, client *db.Client) error {
	coll := client.RawGitHubEvents()
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "event_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("event_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "type", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("type_created_at"),
		},
		{
			Keys:    bson.D{{Key: "ingested_at", Value: -1}},
			Options: options.Index().SetName("ingested_at_desc"),
		},
	}
	_, err := coll.Indexes().CreateMany(ctx, indexes)
	return err
}

// ---------------------------------------------------------------------------
// API response types — private, only used within this package
// ---------------------------------------------------------------------------

type apiEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Actor     apiActor        `json:"actor"`
	Repo      apiRepo         `json:"repo"`
	Org       *apiOrg         `json:"org"`
	Public    bool            `json:"public"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

type apiActor struct {
	ID           int64  `json:"id"`
	Login        string `json:"login"`
	DisplayLogin string `json:"display_login"`
	GravatarID   string `json:"gravatar_id"`
	URL          string `json:"url"`
	AvatarURL    string `json:"avatar_url"`
}

type apiRepo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type apiOrg struct {
	ID         int64  `json:"id"`
	Login      string `json:"login"`
	GravatarID string `json:"gravatar_id"`
	URL        string `json:"url"`
	AvatarURL  string `json:"avatar_url"`
}

// toRaw converts an apiEvent to a RawGitHubEvent for storage.
func toRaw(e apiEvent, batchID string, ingested time.Time, region string) (*models.RawGitHubEvent, error) {
	createdAt, err := time.Parse(time.RFC3339, e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", e.CreatedAt, err)
	}

	var payload map[string]interface{}
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			payload = map[string]interface{}{"raw": string(e.Payload)}
		}
	}

	raw := &models.RawGitHubEvent{
		EventID: e.ID,
		Type:    e.Type,
		Actor: models.GitHubActor{
			ID:           e.Actor.ID,
			Login:        e.Actor.Login,
			DisplayLogin: e.Actor.DisplayLogin,
			GravatarID:   e.Actor.GravatarID,
			URL:          e.Actor.URL,
			AvatarURL:    e.Actor.AvatarURL,
		},
		Repo: models.GitHubRepo{
			ID:   e.Repo.ID,
			Name: e.Repo.Name,
			URL:  e.Repo.URL,
		},
		Public:     e.Public,
		Payload:    payload,
		CreatedAt:  createdAt,
		IngestedAt: ingested,
		BatchID:    batchID,
		Region:     region,
	}

	if e.Org != nil {
		raw.Org = &models.GitHubOrg{
			ID:         e.Org.ID,
			Login:      e.Org.Login,
			GravatarID: e.Org.GravatarID,
			URL:        e.Org.URL,
			AvatarURL:  e.Org.AvatarURL,
		}
	}

	return raw, nil
}

// isBot returns true if the login looks like an automated actor.
func isBot(login string) bool {
	lower := strings.ToLower(login)
	return strings.HasSuffix(lower, "[bot]") ||
		strings.HasSuffix(lower, "-bot") ||
		lower == "copilot"
}

// URLEncode safely encodes a city name for use in OWM API URLs.
// Kept here for cross-package use; mirrors the Phase 1 fix.
func URLEncode(s string) string {
	return url.QueryEscape(s)
}
