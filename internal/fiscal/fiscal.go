// Package fiscal reads the QR codes printed on fiscal receipts. Every country
// (or tax system) is one Provider: it recognises its own verification links,
// decodes what the link itself carries without going online, and fetches the
// full receipt (shop, items, payments) from the tax office.
//
// Money is in minor units (cents) of the receipt's currency.
package fiscal

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "time/tzdata" // receipts carry local times; the image may have no zoneinfo
)

// Receipt is one fiscal receipt, normalised across providers.
type Receipt struct {
	Provider      string    `json:"provider"`
	Country       string    `json:"country"`
	Currency      string    `json:"currency"`
	Key           string    `json:"key"` // unique per provider: the receipt number (or IIC/JIR)
	InvoiceNumber string    `json:"invoice_number"`
	IssuedAt      time.Time `json:"issued_at"`
	Total         int64     `json:"total"`
	TaxTotal      int64     `json:"tax_total"`
	Merchant      Merchant  `json:"merchant"`
	Cashier       string    `json:"cashier"`
	Device        string    `json:"device"` // the fiscal device that signed it: maps offline scans to a shop
	Payments      []Payment `json:"payments"`
	Kind          string    `json:"kind"`         // sale | refund
	InvoiceType   string    `json:"invoice_type"` // normal | proforma | copy | training | advance
	Referent      string    `json:"referent"`     // the original receipt of a refund or copy
	Items         []Item    `json:"items"`
	Journal       string    `json:"journal"`  // the receipt as printed
	Complete      bool      `json:"complete"` // shop and items are known (fetched, not only decoded)
	HasItems      bool      `json:"has_items"`
}

// Merchant is the shop that issued a receipt.
type Merchant struct {
	TaxID    string `json:"tax_id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Address  string `json:"address"`
	City     string `json:"city"`
}

// Payment is one way the receipt was paid.
type Payment struct {
	Method string `json:"method"` // cash | card | transfer | voucher | check | other
	Amount int64  `json:"amount"`
}

// Item is one receipt line.
type Item struct {
	Name      string  `json:"name"`
	GTIN      string  `json:"gtin"`
	Quantity  float64 `json:"quantity"`
	Unit      string  `json:"unit"`
	UnitPrice int64   `json:"unit_price"`
	Total     int64   `json:"total"`
	TaxLabel  string  `json:"tax_label"`
}

// Payment method of the largest payment ("card" when nothing is known).
func (r *Receipt) Method() string {
	best, amt := "", int64(-1)
	for _, p := range r.Payments {
		if p.Amount > amt {
			best, amt = p.Method, p.Amount
		}
	}
	return best
}

// Provider is one country's (or tax system's) receipt verification.
type Provider interface {
	ID() string      // stable id stored with every scan: ba-rs, rs, me, hr
	Country() string // ISO 3166 code (BA-SRP for Republika Srpska)
	Name() string
	Match(u *url.URL) bool
	// Decode reads what the link itself carries, offline. The result is not
	// Complete; it may be nil when the link carries nothing useful.
	Decode(u *url.URL) (*Receipt, error)
	// Fetch asks the tax office for the full receipt. raw is the response
	// body, kept in the scan log so a parser fix can re-read old scans.
	Fetch(ctx context.Context, hc *http.Client, u *url.URL) (rec *Receipt, raw []byte, err error)
	// Parse re-reads a stored raw response.
	Parse(u *url.URL, raw []byte) (*Receipt, error)
}

var (
	// ErrUnsupported: the link is not a fiscal receipt anyone here knows.
	ErrUnsupported = errors.New("this is not a receipt link Pocket Money can read")
	// ErrInvalid: the tax office says the receipt is not valid (permanent).
	ErrInvalid = errors.New("the tax office says this receipt is not valid")
	// ErrNotYet: the tax office does not have it yet (shops may report late).
	ErrNotYet = errors.New("the tax office does not have this receipt yet")
)

// Info describes a provider for the UI.
type Info struct {
	ID       string `json:"id"`
	Country  string `json:"country"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
	Items    bool   `json:"items"` // the tax office returns the item list
	Note     string `json:"note,omitempty"`
}

var providers = []Provider{
	sufProvider{id: "ba-rs", country: "BA-SRP", name: "Bosnia and Herzegovina – Republika Srpska", host: "suf.poreskaupravars.org", currency: "BAM", zone: "Europe/Sarajevo"},
	sufProvider{id: "rs", country: "RS", name: "Serbia", host: "suf.purs.gov.rs", currency: "RSD", zone: "Europe/Belgrade"},
	meProvider{},
	hrProvider{},
}

// Providers lists the supported countries.
func Providers() []Info {
	out := make([]Info, 0, len(providers))
	for _, p := range providers {
		out = append(out, info(p))
	}
	return out
}

func info(p Provider) Info {
	switch x := p.(type) {
	case sufProvider:
		return Info{ID: x.id, Country: x.country, Name: x.name, Currency: x.currency, Items: true}
	case meProvider:
		return Info{ID: x.ID(), Country: x.Country(), Name: x.Name(), Currency: "EUR", Items: true, Note: "Read from the public verification page; new receipts can take up to two days to appear there."}
	case hrProvider:
		return Info{ID: x.ID(), Country: x.Country(), Name: x.Name(), Currency: "EUR", Items: false, Note: "The Croatian QR code only carries the total and the time; the tax office does not publish the shop or the items."}
	}
	return Info{ID: p.ID(), Country: p.Country(), Name: p.Name()}
}

// Find picks the provider for a scanned text (a URL, maybe with spaces or
// line breaks around it).
func Find(text string) (Provider, *url.URL, error) {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "http"); i > 0 {
		text = text[i:]
	}
	if i := strings.IndexAny(text, " \t\r\n"); i > 0 {
		text = text[:i]
	}
	u, err := url.Parse(text)
	if err != nil || u.Host == "" {
		return nil, nil, ErrUnsupported
	}
	for _, p := range providers {
		if p.Match(u) {
			return p, u, nil
		}
	}
	return nil, nil, ErrUnsupported
}

// ByID returns a provider by its id.
func ByID(id string) Provider {
	for _, p := range providers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// IsReceiptURL tells whether text is a link any provider reads.
func IsReceiptURL(text string) bool {
	_, _, err := Find(text)
	return err == nil
}

// rawParam reads a query parameter without turning "+" into a space (the
// base64 in Serbian-style links and the "+01:00" in Montenegrin ones are
// printed unescaped).
func rawParam(rawQuery, name string) string {
	for _, part := range strings.Split(rawQuery, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || k != name {
			continue
		}
		if s, err := url.PathUnescape(v); err == nil {
			return s
		}
		return v
	}
	return ""
}

func minor(x float64) int64 { return int64(math.Round(x * 100)) }

func hostIs(u *url.URL, host string) bool {
	h := strings.ToLower(u.Hostname())
	return h == host || h == "www."+host
}
