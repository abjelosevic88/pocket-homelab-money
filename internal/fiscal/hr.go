package fiscal

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// hrProvider reads Croatian fiscal receipts: https://porezna.gov.hr/rn?jir=…&datv=20261025_0924&izn=3064
// The link carries the receipt id (JIR or ZKI), the time and the total in
// cents; the tax office's check page shows nothing more (and sits behind a
// captcha), so the link is the whole receipt.
type hrProvider struct{}

func (hrProvider) ID() string      { return "hr" }
func (hrProvider) Country() string { return "HR" }
func (hrProvider) Name() string    { return "Croatia" }

func (p hrProvider) Match(u *url.URL) bool {
	return hostIs(u, "porezna.gov.hr") && strings.HasPrefix(strings.TrimSuffix(u.Path, "/"), "/rn") &&
		(rawParam(u.RawQuery, "jir") != "" || rawParam(u.RawQuery, "zki") != "")
}

func (p hrProvider) Decode(u *url.URL) (*Receipt, error) {
	q := u.RawQuery
	id := firstNonEmpty(rawParam(q, "jir"), rawParam(q, "zki"))
	r := &Receipt{Provider: p.ID(), Country: p.Country(), Currency: "EUR", Key: strings.ToLower(id), InvoiceNumber: id,
		Kind: "sale", InvoiceType: "normal", Complete: true}
	loc, err := time.LoadLocation("Europe/Zagreb")
	if err != nil {
		loc = time.UTC
	}
	if t, err := time.ParseInLocation("20060102_1504", rawParam(q, "datv"), loc); err == nil {
		r.IssuedAt = t
	}
	izn := strings.TrimSpace(rawParam(q, "izn"))
	if strings.ContainsAny(izn, ".,") {
		r.Total = parseLocalAmount(strings.ReplaceAll(izn, ".", ","))
	} else if n, err := strconv.ParseInt(izn, 10, 64); err == nil {
		r.Total = n
	}
	return r, nil
}

func (p hrProvider) Fetch(_ context.Context, _ *http.Client, u *url.URL) (*Receipt, []byte, error) {
	r, err := p.Decode(u)
	return r, nil, err
}

func (p hrProvider) Parse(u *url.URL, _ []byte) (*Receipt, error) { return p.Decode(u) }
