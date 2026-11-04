package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/version"
)

// Client calls the HookShield control API with an API key.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewClient returns a client for cfg.
func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, ErrNotLoggedIn
	}
	return &Client{BaseURL: cfg.APIURL, APIKey: cfg.APIKey, HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

// APIError is an error answer of the API ({"error": {"code": "..."}}).
type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string {
	if hint := errorHints[e.Code]; hint != "" {
		return hint
	}
	if e.Code != "" {
		return fmt.Sprintf("API error %d: %s", e.Status, e.Code)
	}
	return fmt.Sprintf("API error %d", e.Status)
}

var errorHints = map[string]string{
	"auth.unauthorized":       "the API key is invalid or was revoked: run `hookshield login` again",
	"auth.insufficient_scope": "this API key's scope does not allow that (create one with scope \"ingest\" or \"admin\" in the dashboard → API keys)",
	"auth.session_required":   "that needs a dashboard session, not an API key",
	"endpoint.not_found":      "endpoint not found",
	"endpoint.paused":         "the endpoint is paused: resume it in the dashboard",
	"rate_limited":            "rate limited: too many requests for this endpoint, try again in a moment",
	"quota_exceeded":          "the monthly quota of your plan is used up",
}

// Do sends a request and decodes a JSON answer into out (nil = ignore).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	c.authorize(req)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	return decode(res, out)
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hookshield-cli/"+version.Version)
}

func decode(res *http.Response, out any) error {
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if res.StatusCode == http.StatusNotFound && e.Error.Code == "" {
			return fmt.Errorf("%s answered 404: is this the HookShield API URL? (`hookshield login --api-url …`)", res.Request.URL.Host)
		}
		return &APIError{Status: res.StatusCode, Code: e.Error.Code}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected answer from %s (is this the HookShield API URL?): %w", res.Request.URL.Host, err)
	}
	return nil
}

// IsUnauthorized reports whether err means the key is not accepted.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// User is the account an API key belongs to.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	PlanType string `json:"plan_type"`
}

// Me returns the account of the key.
func (c *Client) Me(ctx context.Context) (User, error) {
	var out struct {
		User User `json:"user"`
	}
	err := c.Do(ctx, http.MethodGet, "/v1/me", nil, nil, &out)
	return out.User, err
}

// Endpoint is the part of an endpoint the CLI uses.
type Endpoint struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Slug            string            `json:"slug"`
	Status          string            `json:"status"`
	IngestURL       string            `json:"ingest_url"`
	TargetURL       string            `json:"target_url"`
	OutboundHeaders map[string]string `json:"outbound_headers"`
}

// Endpoints lists the account's endpoints (newest first).
func (c *Client) Endpoints(ctx context.Context) ([]Endpoint, error) {
	var out struct {
		Endpoints []Endpoint `json:"endpoints"`
	}
	err := c.Do(ctx, http.MethodGet, "/v1/endpoints", nil, nil, &out)
	return out.Endpoints, err
}

// Endpoint returns one endpoint by ID.
func (c *Client) Endpoint(ctx context.Context, id string) (Endpoint, error) {
	var out struct {
		Endpoint Endpoint `json:"endpoint"`
	}
	err := c.Do(ctx, http.MethodGet, "/v1/endpoints/"+url.PathEscape(id), nil, nil, &out)
	return out.Endpoint, err
}

// FindEndpoint resolves an endpoint by ID, slug or exact name.
func FindEndpoint(eps []Endpoint, ref string) (Endpoint, error) {
	var byName []Endpoint
	for _, e := range eps {
		if e.ID == ref || e.Slug == ref {
			return e, nil
		}
		if e.Name == ref {
			byName = append(byName, e)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Endpoint{}, fmt.Errorf("no endpoint %q: use its slug (ep_…), ID or name — `hookshield endpoints` lists them", ref)
	default:
		return Endpoint{}, fmt.Errorf("%d endpoints are named %q: use the slug (ep_…) instead", len(byName), ref)
	}
}
