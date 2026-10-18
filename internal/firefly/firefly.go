// Package firefly reads a Firefly III instance (and the Firefly Pico
// templates next to it) so Pocket Money can import everything once — or keep
// mirroring it while both run side by side.
package firefly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a read-only Firefly III API client.
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

func (c *Client) get(ctx context.Context, base, path string, q url.Values, v any) error {
	if !c.Configured() {
		return errors.New("Firefly III is not configured")
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.api+json, application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

type page[T any] struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes T      `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
}

// Item is an object with its Firefly id.
type Item[T any] struct {
	ID   string
	Attr T
}

func all[T any](ctx context.Context, c *Client, path string, q url.Values) ([]Item[T], error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "200")
	var out []Item[T]
	for p := 1; p <= 500; p++ {
		q.Set("page", strconv.Itoa(p))
		var res page[T]
		if err := c.get(ctx, c.BaseURL, path, q, &res); err != nil {
			return nil, err
		}
		for _, d := range res.Data {
			out = append(out, Item[T]{ID: d.ID, Attr: d.Attributes})
		}
		if res.Meta.Pagination.TotalPages <= p {
			break
		}
	}
	return out, nil
}

// About returns the Firefly version (connection test).
func (c *Client) About(ctx context.Context) (string, error) {
	var out struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := c.get(ctx, c.BaseURL, "/api/v1/about", nil, &out); err != nil {
		return "", err
	}
	return out.Data.Version, nil
}

// Account is a Firefly account.
type Account struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	AccountRole     string `json:"account_role"`
	CurrencyCode    string `json:"currency_code"`
	CurrentBalance  string `json:"current_balance"`
	Active          bool   `json:"active"`
	IBAN            string `json:"iban"`
	AccountNumber   string `json:"account_number"`
	Notes           string `json:"notes"`
	Order           int    `json:"order"`
	IncludeNetWorth bool   `json:"include_net_worth"`
	OpeningBalance  string `json:"opening_balance"`
	OpeningDate     string `json:"opening_balance_date"`
	CreditCardType  string `json:"credit_card_type"`
	LiabilityType   string `json:"liability_type"`
	LiabilityDir    string `json:"liability_direction"`
	Interest        string `json:"interest"`
	InterestPeriod  string `json:"interest_period"`
}

// Accounts lists asset and liability accounts with the balance on date.
func (c *Client) Accounts(ctx context.Context, date string) ([]Item[Account], error) {
	q := url.Values{"type": {"asset"}}
	if date != "" {
		q.Set("date", date)
	}
	out, err := all[Account](ctx, c, "/api/v1/accounts", q)
	if err != nil {
		return nil, err
	}
	q.Set("type", "liabilities")
	if l, err := all[Account](ctx, c, "/api/v1/accounts", q); err == nil {
		out = append(out, l...)
	}
	return out, nil
}

// Category is a Firefly category.
type Category struct {
	Name  string `json:"name"`
	Notes string `json:"notes"`
}

// Categories lists categories.
func (c *Client) Categories(ctx context.Context) ([]Item[Category], error) {
	return all[Category](ctx, c, "/api/v1/categories", nil)
}

// Tag is a Firefly tag.
type Tag struct {
	Tag string `json:"tag"`
}

// Tags lists tags.
func (c *Client) Tags(ctx context.Context) ([]Item[Tag], error) {
	return all[Tag](ctx, c, "/api/v1/tags", nil)
}

// Split is one line of a Firefly transaction group.
type Split struct {
	JournalID       string   `json:"transaction_journal_id"`
	Type            string   `json:"type"`
	Date            string   `json:"date"`
	Amount          string   `json:"amount"`
	CurrencyCode    string   `json:"currency_code"`
	ForeignAmount   *string  `json:"foreign_amount"`
	ForeignCurrency *string  `json:"foreign_currency_code"`
	Description     string   `json:"description"`
	SourceID        string   `json:"source_id"`
	SourceName      string   `json:"source_name"`
	SourceType      string   `json:"source_type"`
	DestinationID   string   `json:"destination_id"`
	DestinationName string   `json:"destination_name"`
	DestinationType string   `json:"destination_type"`
	CategoryName    *string  `json:"category_name"`
	BudgetName      *string  `json:"budget_name"`
	Tags            []string `json:"tags"`
	Notes           *string  `json:"notes"`
	BillID          *string  `json:"bill_id"`
	RecurrenceID    *string  `json:"recurrence_id"`
	Latitude        *float64 `json:"latitude"`
	Longitude       *float64 `json:"longitude"`
	ExternalID      *string  `json:"external_id"`
}

// Group is a Firefly transaction (one or more splits).
type Group struct {
	GroupTitle   *string `json:"group_title"`
	Transactions []Split `json:"transactions"`
}

// Transactions lists every transaction group in [start, end] ("" = all).
func (c *Client) Transactions(ctx context.Context, start, end string) ([]Item[Group], error) {
	q := url.Values{}
	if start != "" {
		q.Set("start", start)
	}
	if end != "" {
		q.Set("end", end)
	}
	return all[Group](ctx, c, "/api/v1/transactions", q)
}

// Bill is a Firefly bill / subscription.
type Bill struct {
	Name         string   `json:"name"`
	AmountMin    string   `json:"amount_min"`
	AmountMax    string   `json:"amount_max"`
	CurrencyCode string   `json:"currency_code"`
	Date         string   `json:"date"`
	EndDate      *string  `json:"end_date"`
	RepeatFreq   string   `json:"repeat_freq"` // weekly | monthly | quarterly | half-year | yearly
	Skip         int      `json:"skip"`
	Active       bool     `json:"active"`
	Notes        *string  `json:"notes"`
	NextExpected *string  `json:"next_expected_match"`
	PayDates     []string `json:"pay_dates"`
	ObjectGroup  *string  `json:"object_group_title"`
}

// Bills lists bills with pay dates over the next year.
func (c *Client) Bills(ctx context.Context) ([]Item[Bill], error) {
	now := time.Now()
	return all[Bill](ctx, c, "/api/v1/bills", url.Values{"start": {now.Format("2006-01-02")}, "end": {now.AddDate(1, 0, 0).Format("2006-01-02")}})
}

// Recurrence is a Firefly recurring transaction.
type Recurrence struct {
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	FirstDate   string  `json:"first_date"`
	RepeatUntil *string `json:"repeat_until"`
	Repeats     *int    `json:"nr_of_repetitions"`
	Active      bool    `json:"active"`
	Notes       *string `json:"notes"`
	Repetitions []struct {
		Type     string   `json:"type"` // daily | weekly | ndom | monthly | yearly
		Moment   string   `json:"moment"`
		Skip     int      `json:"skip"`
		Weekend  int      `json:"weekend"` // 1 as is, 2 skip, 3 previous Friday, 4 next Monday
		Upcoming []string `json:"occurrences"`
	} `json:"repetitions"`
	Transactions []struct {
		Description     string   `json:"description"`
		Amount          string   `json:"amount"`
		CurrencyCode    string   `json:"currency_code"`
		SourceID        string   `json:"source_id"`
		SourceName      string   `json:"source_name"`
		DestinationID   string   `json:"destination_id"`
		DestinationName string   `json:"destination_name"`
		CategoryName    *string  `json:"category_name"`
		Tags            []string `json:"tags"`
		BillID          *string  `json:"bill_id"`
	} `json:"transactions"`
}

// Recurrences lists recurring transactions.
func (c *Client) Recurrences(ctx context.Context) ([]Item[Recurrence], error) {
	return all[Recurrence](ctx, c, "/api/v1/recurrences", nil)
}

// PiggyBank is a Firefly savings jar.
type PiggyBank struct {
	Name          string  `json:"name"`
	AccountID     string  `json:"account_id"`
	CurrencyCode  string  `json:"currency_code"`
	TargetAmount  string  `json:"target_amount"`
	CurrentAmount string  `json:"current_amount"`
	TargetDate    *string `json:"target_date"`
	StartDate     *string `json:"start_date"`
	Notes         *string `json:"notes"`
}

// PiggyBanks lists piggy banks.
func (c *Client) PiggyBanks(ctx context.Context) ([]Item[PiggyBank], error) {
	return all[PiggyBank](ctx, c, "/api/v1/piggy-banks", nil)
}

// PicoTemplate is a Firefly Pico transaction template.
type PicoTemplate struct {
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	Amount               *float64 `json:"amount"`
	AccountSourceID      *int64   `json:"account_source_id"`
	AccountDestinationID *int64   `json:"account_destination_id"`
	CategoryID           *int64   `json:"category_id"`
	Type                 string   `json:"type"`
	Notes                *string  `json:"notes"`
	Description          *string  `json:"description"`
	Tags                 []struct {
		TagID int64 `json:"tag_id"`
	} `json:"tags"`
	ExtraNames []struct {
		Name string `json:"name"`
	} `json:"extra_names"`
}

// PicoTemplates lists the templates of a Firefly Pico at picoURL (same token).
func (c *Client) PicoTemplates(ctx context.Context, picoURL string) ([]PicoTemplate, error) {
	picoURL = strings.TrimRight(picoURL, "/")
	var out []PicoTemplate
	for p := 1; p <= 50; p++ {
		var res struct {
			Data []PicoTemplate `json:"data"`
			Meta struct {
				Pagination struct {
					TotalPages int `json:"total_pages"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if err := c.get(ctx, picoURL, "/api/transaction-templates", url.Values{"limit": {"100"}, "page": {strconv.Itoa(p)}}, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Data...)
		if res.Meta.Pagination.TotalPages <= p {
			break
		}
	}
	return out, nil
}
