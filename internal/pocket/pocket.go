// Package pocket talks to sibling Pocket apps. Pocket Money asks Pocket
// Travel which trip is running so a new expense can carry the trip's tag.
package pocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bearer-token JSON client for one Pocket app.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Configured reports whether URL and token are set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Token != "" }

func (c *Client) get(ctx context.Context, path string, q url.Values, v any) error {
	if !c.Configured() {
		return errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Health is the connection test for any Pocket app.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if !c.Configured() {
		return "", errors.New("not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	// also prove the token works
	var me map[string]any
	if err := c.get(ctx, "/api/v1/auth/me", nil, &me); err != nil {
		return out.Version, err
	}
	return out.Version, nil
}

// Trip is the slice of a Pocket Travel trip Money cares about.
type Trip struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Destination string `json:"destination"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Status      string `json:"status"`
	Currency    string `json:"currency"`
	Color       string `json:"color"`
	FireflyTag  string `json:"firefly_tag"`
}

// Trips lists the caller's trips (archived ones too, so old expenses still match).
func (c *Client) Trips(ctx context.Context) ([]Trip, error) {
	var out []Trip
	if err := c.get(ctx, "/api/v1/trips", url.Values{"archived": {"0"}}, &out); err != nil {
		return nil, err
	}
	var arch []Trip
	if err := c.get(ctx, "/api/v1/trips", url.Values{"archived": {"1"}}, &arch); err == nil {
		out = append(out, arch...)
	}
	return out, nil
}

// TripOn returns the trip running on a date, if any.
func TripOn(trips []Trip, date string) *Trip {
	for i := range trips {
		t := &trips[i]
		if t.StartDate != "" && t.EndDate != "" && date >= t.StartDate && date <= t.EndDate {
			return t
		}
	}
	return nil
}
