package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func TestAddPeriodKeepsAnchorDay(t *testing.T) {
	cases := []struct {
		from, freq    string
		every, anchor int
		want          string
	}{
		{"2026-01-31", "monthly", 1, 31, "2026-02-28"},
		{"2026-02-28", "monthly", 1, 31, "2026-03-31"},
		{"2026-11-15", "monthly", 3, 15, "2027-02-15"},
		{"2024-02-29", "yearly", 1, 29, "2025-02-28"},
		{"2026-10-18", "weekly", 2, 0, "2026-11-01"},
		{"2026-10-18", "daily", 1, 0, "2026-10-19"},
	}
	for _, c := range cases {
		if got := addPeriod(c.from, c.freq, c.every, c.anchor); got != c.want {
			t.Errorf("addPeriod(%s,%s,%d) = %s, want %s", c.from, c.freq, c.every, got, c.want)
		}
	}
}

func TestPeriodBoundsPaydayMonth(t *testing.T) {
	f, to := periodBounds("2026-10-18", "monthly", 25, 1)
	if f != "2026-09-25" || to != "2026-10-24" {
		t.Errorf("payday month = %s..%s", f, to)
	}
	f, to = periodBounds("2026-10-25", "monthly", 25, 1)
	if f != "2026-10-25" || to != "2026-11-24" {
		t.Errorf("payday month on the day = %s..%s", f, to)
	}
	f, to = periodBounds("2026-10-18", "weekly", 1, 1) // a Sunday
	if f != "2026-10-12" || to != "2026-10-18" {
		t.Errorf("week = %s..%s", f, to)
	}
	f, _ = prevPeriod("2026-03-01", "monthly", 1, 1)
	if f != "2026-02-01" {
		t.Errorf("previous month = %s", f)
	}
}

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in, dec string
		want    float64
	}{
		{"-12,50", "auto", -12.5}, {"1.234,56", "auto", 1234.56}, {"1,234.56", "auto", 1234.56}, {"(45.00)", "auto", -45},
		{"KM 1.500,00", "auto", 1500}, {"20-", "auto", -20}, {"3,5", "comma", 3.5}, {"1,234", "auto", 1234},
	}
	for _, c := range cases {
		if got, ok := parseAmount(c.in, c.dec); !ok || got != c.want {
			t.Errorf("parseAmount(%q) = %v %v, want %v", c.in, got, ok, c.want)
		}
	}
	if d, ok := parseCSVDate("18.10.2026", "auto"); !ok || d != "2026-10-18" {
		t.Errorf("date %s", d)
	}
}

func TestRuleMatching(t *testing.T) {
	tx := &store.Transaction{Kind: "expense", Description: "UBER *TRIP", Payee: "", Amount: 1250, Currency: "EUR"}
	r := &store.Rule{Match: "all", Conditions: []store.Condition{{Field: "text", Op: "contains", Value: "uber"}, {Field: "amount", Op: "lt", Value: "20"}}}
	if !ruleMatches(r, tx) {
		t.Fatal("rule should match")
	}
	r.Conditions[1].Value = "10"
	if ruleMatches(r, tx) {
		t.Fatal("amount condition should fail")
	}
	cid := int64(7)
	if !applyActions(store.RuleActions{CategoryID: &cid, AddTags: []string{"work"}}, tx, false) || tx.CategoryID == nil || len(tx.Tags) != 1 {
		t.Fatal("actions not applied")
	}
}

// ---- end to end over HTTP ----

type tc struct {
	t   *testing.T
	ts  *httptest.Server
	srv *Server
}

// settle waits for the work a receipt does after its booking was answered.
func (c *tc) settle() { c.srv.receiptWork.Wait() }

func (c *tc) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	if res.StatusCode >= 400 {
		c.t.Logf("%s %s → %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode
}

func newTestServer(t *testing.T) *tc {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DBPath: dir + "/t.db", FilesDir: dir + "/files", AuthMode: "none", DefaultCurrency: "BAM", FXProvider: "none", MaxUploadBytes: 1 << 20, NotifyHour: 9, UserAgent: "test"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(cfg, st, log, nil)
	if err := s.core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	t.Cleanup(s.receiptWork.Wait) // before the database closes
	return &tc{t: t, ts: ts, srv: s}
}

func TestEndToEnd(t *testing.T) {
	c := newTestServer(t)
	var bam, eur store.Account
	if c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM", "opening_balance": 100000}, &bam) != 201 {
		t.Fatal("create account")
	}
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "Devizni EUR", "currency": "EUR", "kind": "savings"}, &eur)
	var cat store.Category
	c.do("POST", "/api/v1/categories", map[string]any{"name": "restoran"}, &cat)
	if cat.Icon == "" {
		t.Error("category should get an emoji")
	}

	// paid 20 EUR from the BAM account: the euro peg converts it
	var tx store.Transaction
	if c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bam.ID, "amount": 2000, "currency": "EUR", "description": "Večera", "category_id": cat.ID, "tags": []string{"crna gora"}}, &tx) != 201 {
		t.Fatal("create tx")
	}
	if tx.Amount != 3912 || tx.ForeignAmount != 2000 || tx.ForeignCurrency != "EUR" || tx.AmountHome != 3912 {
		t.Errorf("foreign conversion: amount %d foreign %d %s home %d", tx.Amount, tx.ForeignAmount, tx.ForeignCurrency, tx.AmountHome)
	}
	// transfer BAM → EUR converts the destination amount
	var tr store.Transaction
	c.do("POST", "/api/v1/transactions", map[string]any{"kind": "transfer", "account_id": bam.ID, "to_account_id": eur.ID, "amount": 19558}, &tr)
	if tr.ToAmount != 10000 {
		t.Errorf("transfer to_amount = %d", tr.ToAmount)
	}

	// quick add learns the category from the earlier description
	var drafts []map[string]any
	c.do("POST", "/api/v1/quick/parse", map[string]any{"text": "večera 30 juče"}, &drafts)
	if len(drafts) != 1 || drafts[0]["category_id"].(float64) != float64(cat.ID) || drafts[0]["amount"].(float64) != 3000 {
		t.Errorf("quick parse: %v", drafts)
	}
	var created []store.Transaction
	if c.do("POST", "/api/v1/quick", map[string]any{"text": "kafa 3,5; +1500 plata"}, &created) != 201 || len(created) != 2 || created[1].Kind != "income" {
		t.Fatalf("quick add: %+v", created)
	}

	// balance: 1000 − 39.12 − 195.58 − 3.50 + 1500 = 2261.80 BAM
	var a store.Account
	c.do("GET", "/api/v1/accounts/"+itoa(bam.ID), nil, &a)
	if a.Balance != 226180 {
		t.Errorf("balance = %d", a.Balance)
	}

	// a subscription and a payment that matches it by name, amount and date
	var sub store.Subscription
	c.do("POST", "/api/v1/subscriptions", map[string]any{"name": "Netflix", "amount": 2499, "currency": "BAM", "account_id": bam.ID, "freq": "monthly", "next_date": store.Today(), "mode": "remind"}, &sub)
	var pay store.Transaction
	c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bam.ID, "amount": 2499, "description": "NETFLIX.COM"}, &pay)
	if pay.SubscriptionID == nil || *pay.SubscriptionID != sub.ID {
		t.Errorf("payment not linked to the subscription: %+v", pay.SubscriptionID)
	}
	c.do("GET", "/api/v1/subscriptions/"+itoa(sub.ID), nil, &struct {
		Subscription *store.Subscription `json:"subscription"`
	}{&sub})
	if sub.NextDate <= store.Today() {
		t.Errorf("next date did not move: %s", sub.NextDate)
	}

	// Firefly-compatible API: a token, a withdrawal the Raycast way, and reading it back by tag
	var tok struct {
		Token string `json:"token"`
	}
	c.do("POST", "/api/v1/tokens", map[string]any{"name": "raycast"}, &tok)
	body := `{"transactions":[{"type":"withdrawal","date":"2026-10-01T12:30:00+02:00","amount":"11.70","description":"pekara","source_id":"` + itoa(bam.ID) + `","category_name":"hrana","tags":["licno"],"foreign_amount":"6.00","foreign_currency_code":"EUR","notes":"[currency:EUR]"}]}`
	req, _ := http.NewRequest("POST", c.ts.URL+"/firefly/api/v1/transactions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("compat create: %v %d", err, res.StatusCode)
	}
	var grp struct {
		Data struct {
			Attributes struct {
				Transactions []map[string]any `json:"transactions"`
			} `json:"attributes"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&grp)
	res.Body.Close()
	sp := grp.Data.Attributes.Transactions[0]
	if sp["amount"] != "11.700000000000" || sp["foreign_currency_code"] != "EUR" || sp["category_name"] != "hrana" || sp["notes"] != nil || sp["transaction_journal_id"] == "" {
		t.Errorf("compat split: %v", sp)
	}
	req, _ = http.NewRequest("GET", c.ts.URL+"/firefly/api/v1/tags/licno/transactions?type=withdrawal", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	res, _ = http.DefaultClient.Do(req)
	var list struct {
		Data []any `json:"data"`
		Meta struct {
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	_ = json.NewDecoder(res.Body).Decode(&list)
	res.Body.Close()
	if list.Meta.Pagination.Total != 1 {
		t.Errorf("tag transactions = %d", list.Meta.Pagination.Total)
	}
	req, _ = http.NewRequest("GET", c.ts.URL+"/firefly/api/v1/bills", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	res, _ = http.DefaultClient.Do(req)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(raw), `"name":"Netflix"`) || !strings.Contains(string(raw), "pay_dates") {
		t.Errorf("bills: %s", raw)
	}
	// unauthenticated compat calls are refused even in AUTH_MODE=none? no — none trusts everyone; a wrong token is refused
	req, _ = http.NewRequest("GET", c.ts.URL+"/firefly/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer nope")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Errorf("bad token = %d", res.StatusCode)
	}

	// home + dashboard answer
	var dash map[string]any
	if c.do("GET", "/api/v1/dashboard", nil, &dash) != 200 || dash["currency"] != "BAM" {
		t.Errorf("dashboard: %v", dash)
	}
	var home map[string]any
	if c.do("GET", "/api/v1/home", nil, &home) != 200 || home["forecast"] == nil {
		t.Errorf("home")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
