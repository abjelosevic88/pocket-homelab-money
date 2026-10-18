// Package fx looks exchange rates up so an expense paid in one currency can
// be counted in the group's currency. Two free, keyless sources:
//
//   - Frankfurter (api.frankfurter.app): ECB reference rates, ~30 major
//     currencies, with history — the right answer for the expense's date.
//   - ExchangeRate-API open endpoint (open.er-api.com): ~160 currencies
//     (BAM, RSD, …), today's rates only.
//
// "auto" tries Frankfurter first, then the open endpoint. The server caches
// answers in the database so a rate is fetched once per currency pair and day.
package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means no source could give a rate.
var ErrUnavailable = errors.New("no exchange rate available")

// Client fetches rates.
type Client struct {
	HTTP     *http.Client
	UA       string
	Provider string // auto | frankfurter | erapi | none

	mu    sync.Mutex
	erapi map[string]erapiTable // base → today's table
}

type erapiTable struct {
	fetched time.Time
	rates   map[string]float64
}

// New creates a client.
func New(provider, ua string, timeout time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}, UA: ua, Provider: provider, erapi: map[string]erapiTable{}}
}

// Enabled reports whether lookups are on at all.
func (c *Client) Enabled() bool { return c.Provider != "none" }

// Rate returns how many units of `to` one unit of `from` was worth on date
// (YYYY-MM-DD; today or the future mean "latest"), and which source said so.
func (c *Client) Rate(ctx context.Context, from, to, date string) (float64, string, error) {
	from, to = strings.ToUpper(from), strings.ToUpper(to)
	if from == to {
		return 1, "same", nil
	}
	if r, ok := Pegged(from, to); ok {
		return r, "fixed peg", nil
	}
	if !c.Enabled() {
		return 0, "", ErrUnavailable
	}
	// A euro-pegged currency (BAM, BGN, XOF…) goes through EUR so dated ECB
	// rates still apply: USD→BAM = USD→EUR × 1.95583.
	if p, ok := pegs[to]; ok && from != "EUR" {
		if _, fromPegged := pegs[from]; !fromPegged {
			if r, src, err := c.Rate(ctx, from, "EUR", date); err == nil {
				return r * p, src + " via EUR peg", nil
			}
		}
	}
	if p, ok := pegs[from]; ok && to != "EUR" {
		if _, toPegged := pegs[to]; !toPegged {
			if r, src, err := c.Rate(ctx, "EUR", to, date); err == nil {
				return r / p, src + " via EUR peg", nil
			}
		}
	}
	var errs []string
	if c.Provider == "auto" || c.Provider == "frankfurter" {
		if r, err := c.frankfurter(ctx, from, to, date); err == nil {
			return r, "frankfurter", nil
		} else {
			errs = append(errs, "frankfurter: "+err.Error())
		}
	}
	if c.Provider == "auto" || c.Provider == "erapi" {
		if r, err := c.erapiRate(ctx, from, to); err == nil {
			return r, "open.er-api.com", nil
		} else {
			errs = append(errs, "er-api: "+err.Error())
		}
	}
	return 0, "", fmt.Errorf("%w (%s)", ErrUnavailable, strings.Join(errs, "; "))
}

func (c *Client) get(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

func (c *Client) frankfurter(ctx context.Context, from, to, date string) (float64, error) {
	when := "latest"
	if d, err := time.Parse("2006-01-02", date); err == nil && d.Before(time.Now().Truncate(24*time.Hour)) {
		when = date
	}
	var out struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := c.get(ctx, fmt.Sprintf("https://api.frankfurter.app/%s?from=%s&to=%s", when, from, to), &out); err != nil {
		return 0, err
	}
	r, ok := out.Rates[to]
	if !ok || r <= 0 {
		return 0, errors.New("currency not covered")
	}
	return r, nil
}

func (c *Client) erapiRate(ctx context.Context, from, to string) (float64, error) {
	c.mu.Lock()
	tbl, ok := c.erapi[from]
	c.mu.Unlock()
	if !ok || time.Since(tbl.fetched) > 12*time.Hour {
		var out struct {
			Result string             `json:"result"`
			Rates  map[string]float64 `json:"rates"`
		}
		if err := c.get(ctx, "https://open.er-api.com/v6/latest/"+from, &out); err != nil {
			return 0, err
		}
		if out.Result != "success" || len(out.Rates) == 0 {
			return 0, errors.New("no rates for " + from)
		}
		tbl = erapiTable{fetched: time.Now(), rates: out.Rates}
		c.mu.Lock()
		c.erapi[from] = tbl
		c.mu.Unlock()
	}
	r, ok := tbl.rates[to]
	if !ok || r <= 0 {
		return 0, errors.New("currency not covered")
	}
	return r, nil
}

// pegs are currencies fixed to the euro by law; no lookup needed and the
// providers (ECB via Frankfurter) do not even list some of them.
var pegs = map[string]float64{"BAM": 1.95583, "BGN": 1.95583, "XOF": 655.957, "XAF": 655.957, "XPF": 119.33174, "CVE": 110.265, "KMF": 491.96775, "STN": 24.5}

// Pegged returns the fixed rate between two euro-pegged currencies (or EUR).
func Pegged(from, to string) (float64, bool) {
	if to == "EUR" {
		if r, ok := pegs[from]; ok {
			return 1 / r, true
		}
	}
	if from == "EUR" {
		if r, ok := pegs[to]; ok {
			return r, true
		}
	}
	if a, ok := pegs[from]; ok {
		if b, ok := pegs[to]; ok {
			return b / a, true
		}
	}
	return 0, false
}
