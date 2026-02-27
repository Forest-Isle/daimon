package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultClawHubBaseURL = "https://clawhub.ai"

// ClawHubClient is an HTTP client for the ClawHub public skill registry.
type ClawHubClient struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
}

// SearchResult represents a single skill returned by the ClawHub search API.
type SearchResult struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
}

// NewClawHubClient creates a new ClawHubClient.
// If baseURL is empty, the default ClawHub URL is used.
func NewClawHubClient(baseURL, apiKey string) *ClawHubClient {
	if baseURL == "" {
		baseURL = defaultClawHubBaseURL
	}
	return &ClawHubClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Search queries ClawHub for skills matching query.
func (c *ClawHubClient) Search(ctx context.Context, query string) ([]SearchResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/skills/search?q=%s", c.baseURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("clawhub: build request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clawhub: search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clawhub: search returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var results []SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("clawhub: decode search response: %w", err)
	}

	return results, nil
}

// Download fetches a skill by slug from ClawHub, writes it to destDir/<slug>/SKILL.md,
// and returns the parsed Skill.
func (c *ClawHubClient) Download(ctx context.Context, slug, destDir string) (*Skill, error) {
	endpoint := fmt.Sprintf("%s/api/v1/skills/%s/raw", c.baseURL, url.PathEscape(slug))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("clawhub: build download request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clawhub: download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clawhub: download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("clawhub: read download body: %w", err)
	}

	// Write to destDir/<slug>/SKILL.md
	skillDir := filepath.Join(destDir, slug)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, fmt.Errorf("clawhub: create skill dir: %w", err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, content, 0644); err != nil {
		return nil, fmt.Errorf("clawhub: write skill file: %w", err)
	}

	return ParseSkill(skillPath)
}

func (c *ClawHubClient) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "ironclaw/1.0")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}
