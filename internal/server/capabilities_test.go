package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// The four routes Pocket Search, Pocket Day and Pocket Hub call
// (capabilities.go): the shape of each answer, that it is the app's own
// data and numbers, and that nobody sees or books on another person's
// accounts unless one was shared with them.

// capCore is a Pocket Core that vouches for link tokens
// pcx_<owner|other>_<search|day|widget|capture|read|write> from Pocket Hub
// and remembers the manifest Money registers with.
type capCore struct {
	mu       sync.Mutex
	manifest map[string]any
}

func (f *capCore) handler() http.Handler {
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "transactions:read", "write": "accounts:read transactions:write"}
	people := map[string]int{"owner": 1, "other": 2}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			f.mu.Lock()
			f.manifest = m
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"app":{},"token":"pca_money"}`))
		case "/api/v1/auth/introspect":
			var in struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || people[parts[1]] == 0 || scopes[parts[2]] == "" {
				_, _ = w.Write([]byte(`{"active":false}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "scope": scopes[parts[2]], "audience": "money",
				"user": map[string]any{"id": people[parts[1]], "email": parts[1] + "@example.com", "name": parts[1]},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			http.NotFound(w, r)
		}
	})
}

// capItems are the rows of a capability answer.
func capItems(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

// capGet reads a capability route and wants a 200.
func capGet(t *testing.T, p *peer, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if code, raw := p.call("GET", path, nil, &m); code != 200 {
		t.Fatalf("GET %s: %d %s", path, code, raw)
	}
	return m
}

// capMetrics are an answer's labelled numbers by label.
func capMetrics(m map[string]any) map[string]string {
	out := map[string]string{}
	l, _ := m["metrics"].([]any)
	for _, v := range l {
		row := v.(map[string]any)
		out[row["label"].(string)] = row["value"].(string)
	}
	return out
}

// capLedger is what the tests search and read: the owner's accounts, a
// category and a few transactions, most of them on 2026-06-02.
type capLedger struct {
	cash, savings, euro                 store.Account
	food                                store.Category
	lunch, salary, moved, book, espress store.Transaction
}

func seedCapLedger(t *testing.T, p *peer) capLedger {
	t.Helper()
	var l capLedger
	p.call("POST", "/api/v1/accounts", map[string]any{"name": "Cash", "currency": "BAM", "institution": "Wallet", "opening_balance": 100000}, &l.cash)
	p.call("POST", "/api/v1/accounts", map[string]any{"name": "Savings", "currency": "BAM", "kind": "savings"}, &l.savings)
	p.call("POST", "/api/v1/accounts", map[string]any{"name": "Euro card", "currency": "EUR"}, &l.euro)
	p.call("POST", "/api/v1/categories", map[string]any{"name": "Food"}, &l.food)
	for _, c := range []struct {
		body map[string]any
		out  *store.Transaction
	}{
		{map[string]any{"kind": "expense", "account_id": l.cash.ID, "amount": 1250, "description": "Lunch", "payee": "Bistro Mala", "date": "2026-06-02", "time": "12:30",
			"category_id": l.food.ID, "tags": []string{"workday"}, "notes": "with the team"}, &l.lunch},
		{map[string]any{"kind": "income", "account_id": l.cash.ID, "amount": 300000, "description": "Plata", "date": "2026-06-02", "time": "08:00"}, &l.salary},
		{map[string]any{"kind": "transfer", "account_id": l.cash.ID, "to_account_id": l.savings.ID, "amount": 20000, "date": "2026-06-02", "time": "18:00"}, &l.moved},
		{map[string]any{"kind": "expense", "account_id": l.euro.ID, "amount": 2000, "description": "Book", "date": "2026-06-02", "time": "16:45"}, &l.book},
		{map[string]any{"kind": "expense", "account_id": l.cash.ID, "amount": 300, "currency": "EUR", "description": "Espresso", "date": "2026-06-05", "category_id": l.food.ID}, &l.espress},
	} {
		if code, raw := p.call("POST", "/api/v1/transactions", c.body, c.out); code != 201 || c.out.ID == 0 {
			t.Fatalf("seed %v: %d %s", c.body["description"], code, raw)
		}
	}
	return l
}

func TestPocketSearch(t *testing.T) {
	s, a, b, bu := newPerUserServer(t)
	t.Cleanup(s.receiptWork.Wait)
	l := seedCapLedger(t, a)

	// a transaction: by description, payee, notes and tag, as the list's own search finds it
	for _, q := range []string{"lunch", "bistro", "team", "workday", "mala+lunch"} {
		got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q="+q))
		if len(got) != 1 || got[0]["title"] != "Lunch" || got[0]["kind"] != "transaction" || got[0]["id"] != "transaction:"+itoa(l.lunch.ID) ||
			got[0]["url"] != "/transactions/"+itoa(l.lunch.ID) || got[0]["date"] != "2026-06-02" || got[0]["snippet"] != "12.50 BAM · Cash · Food · Bistro Mala" {
			t.Fatalf("%q: %v", q, got)
		}
	}
	// money coming in, a transfer and a payment in another currency read as such
	if got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=plata")); len(got) != 1 || got[0]["snippet"] != "+3000.00 BAM · Cash" {
		t.Fatalf("income: %v", got)
	}
	if got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=espresso")); len(got) != 1 ||
		got[0]["snippet"] != majorString(l.espress.Amount, "BAM")+" (3.00 EUR) · Cash · Food" || l.espress.Amount == 300 {
		t.Fatalf("paid in euros: %v (%d)", got, l.espress.Amount)
	}
	// an account by its name, before the transactions on it; a transfer names both accounts
	got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=savings"))
	if len(got) != 1 || got[0]["kind"] != "account" || got[0]["id"] != "account:"+itoa(l.savings.ID) || got[0]["url"] != "/accounts/"+itoa(l.savings.ID) ||
		got[0]["title"] != "Savings" || got[0]["snippet"] != "Balance 200.00 BAM" {
		t.Fatalf("account: %v", got)
	}
	if got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=wallet")); len(got) != 1 || got[0]["title"] != "Cash" || !strings.HasPrefix(got[0]["snippet"].(string), "Wallet · ") {
		t.Fatalf("account by its bank: %v", got)
	}
	// a category by its name, then what was booked in it, newest first
	got = capItems(t, capGet(t, a, "/api/v1/pocket/search?q=food"))
	if len(got) != 3 || got[0]["kind"] != "category" || got[0]["id"] != "category:"+itoa(l.food.ID) || got[0]["url"] != "/transactions?category="+itoa(l.food.ID) ||
		got[0]["snippet"] != "2 transactions" || got[1]["title"] != "Espresso" || got[2]["title"] != "Lunch" {
		t.Fatalf("category: %v", got)
	}
	// the same transactions the list's search gives, in its order
	var page struct {
		Items []store.Transaction `json:"items"`
	}
	a.call("GET", "/api/v1/transactions?q=cash", nil, &page)
	got = capItems(t, capGet(t, a, "/api/v1/pocket/search?q=cash&limit=50"))
	if len(page.Items) != 0 || len(got) != 1 || got[0]["kind"] != "account" {
		t.Fatalf("an account's name is not a transaction search: %d %v", len(page.Items), got)
	}
	a.call("GET", "/api/v1/transactions?q=o", nil, &page)
	got = capItems(t, capGet(t, a, "/api/v1/pocket/search?q=o&limit=50"))
	var txs []map[string]any
	for _, it := range got {
		if it["kind"] == "transaction" {
			txs = append(txs, it)
		}
	}
	if len(page.Items) < 3 || len(txs) != len(page.Items) {
		t.Fatalf("list finds %d, search %d", len(page.Items), len(txs))
	}
	for i, it := range txs {
		if it["id"] != "transaction:"+itoa(page.Items[i].ID) {
			t.Fatalf("order differs at %d: %v vs %d", i, it, page.Items[i].ID)
		}
	}
	if got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=food&limit=1")); len(got) != 1 || got[0]["kind"] != "category" {
		t.Fatalf("limit 1: %v", got)
	}
	if got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=")); len(got) != 0 {
		t.Fatalf("no words: %v", got)
	}
	// a transfer without a description has no words to be found by
	if got := capItems(t, capGet(t, a, "/api/v1/pocket/search?q=transfer")); len(got) != 0 {
		t.Fatalf("a transfer has no text to find: %v", got)
	}

	// another person's transactions, accounts and categories never appear
	for _, q := range []string{"lunch", "bistro", "plata", "book", "food", "cash", "savings", "euro", "o"} {
		if got := capItems(t, capGet(t, b, "/api/v1/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("member finds the owner's %q: %v", q, got)
		}
	}
	// until an account is shared: then that account and what is on it, and nothing else
	if code := a.status("PUT", "/api/v1/accounts/"+itoa(l.euro.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "viewer"}}}); code != 200 {
		t.Fatalf("share: %d", code)
	}
	if got := capItems(t, capGet(t, b, "/api/v1/pocket/search?q=book")); len(got) != 1 || got[0]["title"] != "Book" || got[0]["snippet"] != "20.00 EUR · Euro card" {
		t.Fatalf("shared account's transaction: %v", got)
	}
	if got := capItems(t, capGet(t, b, "/api/v1/pocket/search?q=euro")); len(got) != 1 || got[0]["kind"] != "account" {
		t.Fatalf("shared account: %v", got)
	}
	for _, q := range []string{"lunch", "plata", "food", "cash", "savings"} {
		if got := capItems(t, capGet(t, b, "/api/v1/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("member finds %q beyond the shared account: %v", q, got)
		}
	}
}

func TestPocketDay(t *testing.T) {
	s, a, b, bu := newPerUserServer(t)
	t.Cleanup(s.receiptWork.Wait)
	l := seedCapLedger(t, a)

	d := capGet(t, a, "/api/v1/pocket/day/2026-06-02")
	got := capItems(t, d)
	if d["date"] != "2026-06-02" || len(got) != 4 {
		t.Fatalf("day: %v", d)
	}
	for i, want := range []struct {
		id                        int64
		title, sub, kind, at, cur string
		amount                    float64
	}{
		{l.salary.ID, "Plata", "Cash", "income", "08:00", "BAM", 300000},
		{l.lunch.ID, "Lunch", "Food", "expense", "12:30", "BAM", 1250},
		{l.book.ID, "Book", "Euro card", "expense", "16:45", "EUR", 2000},
		{l.moved.ID, "Transfer", "Cash → Savings", "transfer", "18:00", "BAM", 20000},
	} {
		it := got[i]
		if it["id"] != itoa(want.id) || it["title"] != want.title || it["sub"] != want.sub || it["kind"] != want.kind || it["time"] != want.at ||
			it["currency"] != want.cur || it["amount"] != want.amount || it["url"] != "/transactions/"+itoa(want.id) {
			t.Fatalf("item %d: %v, want %+v", i, it, want)
		}
	}
	if got[1]["note"] != "with the team" {
		t.Fatalf("notes: %v", got[1])
	}
	// each currency on its own; the transfer is neither spent nor received
	if d["summary"] != "Spent 12.50 BAM and 20.00 EUR in 2 payments, received 3000.00 BAM" {
		t.Fatalf("summary: %v", d["summary"])
	}
	if m := capMetrics(d); len(m) != 2 || m["Spent"] != "12.50 BAM + 20.00 EUR" || m["Received"] != "3000.00 BAM" {
		t.Fatalf("metrics: %v", m)
	}
	// one payment, in words
	d = capGet(t, a, "/api/v1/pocket/day/2026-06-05")
	if d["summary"] != "Spent "+majorString(l.espress.Amount, "BAM")+" in 1 payment" || len(capItems(t, d)) != 1 || len(capMetrics(d)) != 1 {
		t.Fatalf("one payment: %v", d)
	}
	// only money moved between accounts
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "transfer", "account_id": l.cash.ID, "to_account_id": l.savings.ID, "amount": 5000, "date": "2026-06-07"}, nil)
	d = capGet(t, a, "/api/v1/pocket/day/2026-06-07")
	if d["summary"] != "Moved 50.00 BAM between accounts" || len(capItems(t, d)) != 1 || len(capMetrics(d)) != 0 {
		t.Fatalf("a transfer only: %v", d)
	}
	// a day with nothing
	d = capGet(t, a, "/api/v1/pocket/day/2026-06-03")
	if d["summary"] != nil || len(capItems(t, d)) != 0 || len(capMetrics(d)) != 0 {
		t.Fatalf("empty day: %v", d)
	}
	if code := a.status("GET", "/api/v1/pocket/day/yesterday", nil); code != 400 {
		t.Fatalf("a day that is not a date: %d", code)
	}

	// another person's day stays empty, until an account is shared with them
	d = capGet(t, b, "/api/v1/pocket/day/2026-06-02")
	if d["summary"] != nil || len(capItems(t, d)) != 0 {
		t.Fatalf("member reads the owner's day: %v", d)
	}
	if code := a.status("PUT", "/api/v1/accounts/"+itoa(l.euro.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "viewer"}}}); code != 200 {
		t.Fatalf("share: %d", code)
	}
	d = capGet(t, b, "/api/v1/pocket/day/2026-06-02")
	if got := capItems(t, d); len(got) != 1 || got[0]["title"] != "Book" || d["summary"] != "Spent 20.00 EUR in 1 payment" {
		t.Fatalf("the shared account's day: %v", d)
	}
}

func TestPocketWidget(t *testing.T) {
	s, a, b, _ := newPerUserServer(t)
	t.Cleanup(s.receiptWork.Wait)
	today := store.Today()

	// nobody has anything yet
	for _, p := range []*peer{a, b} {
		if w := capGet(t, p, "/api/v1/pocket/widget"); len(capItems(t, w)) != 0 || len(capMetrics(w)) != 0 || w["line"] != nil {
			t.Fatalf("empty widget: %v", w)
		}
	}
	l := seedCapLedger(t, a)
	// accounts, but nothing this month and nothing due
	if w := capGet(t, a, "/api/v1/pocket/widget"); len(capItems(t, w)) != 0 || len(capMetrics(w)) != 0 || w["line"] != nil {
		t.Fatalf("a quiet month: %v", w)
	}

	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "income", "account_id": l.cash.ID, "amount": 150000, "description": "Honorar", "date": today}, nil)
	w := capGet(t, a, "/api/v1/pocket/widget")
	if w["line"] != "Received 1500.00 BAM this month" || w["url"] != "/" {
		t.Fatalf("income only: %v", w)
	}
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": l.cash.ID, "amount": 4000, "description": "Pijaca", "date": today, "category_id": l.food.ID}, nil)
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": l.cash.ID, "amount": 2250, "description": "Kino", "date": today}, nil)
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "transfer", "account_id": l.cash.ID, "to_account_id": l.savings.ID, "amount": 10000, "date": today}, nil)
	var bud store.Budget
	a.call("POST", "/api/v1/budgets", map[string]any{"name": "Hrana", "amount": 50000, "period": "monthly", "category_ids": []int64{l.food.ID}}, &bud)
	a.call("POST", "/api/v1/budgets", map[string]any{"name": "Godina", "amount": 900000, "period": "yearly"}, nil)
	var rent, late, pay, paused store.Subscription
	a.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Stanarina", "amount": 60000, "currency": "BAM", "freq": "monthly", "next_date": addDays(today, 3), "mode": "remind"}, &rent)
	a.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Internet", "amount": 1599, "currency": "EUR", "freq": "monthly", "next_date": addDays(today, -2), "mode": "remind"}, &late)
	a.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Najam", "kind": "income", "amount": 40000, "currency": "BAM", "freq": "monthly", "next_date": addDays(today, 1), "mode": "remind"}, &pay)
	a.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Teretana", "amount": 5000, "currency": "BAM", "freq": "monthly", "next_date": addDays(today, 2), "mode": "remind", "active": false}, &paused)
	a.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Registracija", "amount": 30000, "currency": "BAM", "freq": "yearly", "next_date": addDays(today, 200), "mode": "remind"}, nil)
	if rent.ID == 0 || late.ID == 0 || pay.ID == 0 || paused.ID == 0 || bud.ID == 0 {
		t.Fatal("seed bills and budget")
	}

	w = capGet(t, a, "/api/v1/pocket/widget")
	if w["line"] != "Spent 62.50 BAM this month" {
		t.Fatalf("line: %v", w)
	}
	if m := capMetrics(w); len(m) != 3 || m["Spent"] != "62.50 BAM" || m["Received"] != "1500.00 BAM" || m["Budget left"] != "460.00 BAM" {
		t.Fatalf("metrics: %v", m)
	}
	// the bills due in the next 30 days, the missed one first; no income, nothing paused
	bills := capItems(t, w)
	if len(bills) != 2 || bills[0]["title"] != "Internet" || bills[0]["detail"] != "15.99 EUR · 2 days late" || bills[0]["id"] != itoa(late.ID) ||
		bills[0]["url"] != "/subscriptions/"+itoa(late.ID) || bills[1]["title"] != "Stanarina" || bills[1]["detail"] != "600.00 BAM · in 3 days" {
		t.Fatalf("bills: %v", bills)
	}
	// the same numbers the app's own pages show
	var dash map[string]any
	a.call("GET", "/api/v1/dashboard", nil, &dash)
	var bl struct {
		Total int64 `json:"month_total"`
		Spent int64 `json:"month_spent"`
	}
	a.call("GET", "/api/v1/budgets", nil, &bl)
	if m := capMetrics(w); m["Spent"] != dash["month_spent_text"] || m["Received"] != dash["month_income_text"] || m["Budget left"] != majorString(bl.Total-bl.Spent, "BAM") {
		t.Fatalf("widget %v, dashboard %v / %v, budgets %+v", m, dash["month_spent_text"], dash["month_income_text"], bl)
	}
	// more spent than the budgets allow
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": l.cash.ID, "amount": 50000, "description": "Slavlje", "date": today, "category_id": l.food.ID}, nil)
	if m := capMetrics(capGet(t, a, "/api/v1/pocket/widget")); m["Over budget"] != "40.00 BAM" || m["Budget left"] != "" || m["Spent"] != "562.50 BAM" {
		t.Fatalf("over budget: %v", m)
	}
	// a bill due today and one due tomorrow
	a.call("PUT", "/api/v1/subscriptions/"+itoa(late.ID), map[string]any{"next_date": today}, nil)
	a.call("PUT", "/api/v1/subscriptions/"+itoa(rent.ID), map[string]any{"next_date": addDays(today, 1)}, nil)
	if bills := capItems(t, capGet(t, a, "/api/v1/pocket/widget")); len(bills) != 2 || bills[0]["detail"] != "15.99 EUR · today" || bills[1]["detail"] != "600.00 BAM · tomorrow" {
		t.Fatalf("today and tomorrow: %v", bills)
	}

	// another person's widget stays empty
	if w := capGet(t, b, "/api/v1/pocket/widget"); len(capItems(t, w)) != 0 || len(capMetrics(w)) != 0 || w["line"] != nil {
		t.Fatalf("member's widget: %v", w)
	}
}

// capture sends a quick line and returns the status and the answer.
func capture(p *peer, requestID, text string, more map[string]any) (int, map[string]any) {
	body := map[string]any{"request_id": requestID, "kind": "expense", "text": text}
	for k, v := range more {
		body[k] = v
	}
	var m map[string]any
	code, raw := p.call("POST", "/api/v1/pocket/capture", body, nil)
	_ = json.Unmarshal(raw, &m)
	return code, m
}

// txsOf lists a person's transactions, newest first.
func txsOf(p *peer) []store.Transaction {
	var page struct {
		Items []store.Transaction `json:"items"`
	}
	p.call("GET", "/api/v1/transactions?limit=500", nil, &page)
	return page.Items
}

func TestPocketCapture(t *testing.T) {
	s, a, b, bu := newPerUserServer(t)
	t.Cleanup(s.receiptWork.Wait)
	today := store.Today()

	// nowhere to book yet
	if code, m := capture(a, "req-0000000000000000", "12.50 lunch", nil); code != 400 || m["error"] != "add an account in Pocket Money first" {
		t.Fatalf("no account: %d %v", code, m)
	}
	l := seedCapLedger(t, a)
	before := len(txsOf(a))

	code, m := capture(a, "req-0000000000000001", "12.50 lunch", nil)
	if code != 201 || m["title"] != "Lunch · 12.50 BAM · Cash" {
		t.Fatalf("capture: %d %v", code, m)
	}
	all := txsOf(a)
	if len(all) != before+1 {
		t.Fatalf("%d transactions, want %d", len(all), before+1)
	}
	tx := all[0]
	if m["id"] != itoa(tx.ID) || m["url"] != "/transactions/"+itoa(tx.ID) || tx.Kind != "expense" || tx.Amount != 1250 || tx.Currency != "BAM" || tx.AccountID != l.cash.ID ||
		tx.Description != "Lunch" || tx.Date != today || tx.Time == "" || tx.Source != "quick" || tx.CategoryName != "Food" {
		t.Fatalf("booked: %+v", tx)
	}
	// booked like the quick add bar books the same line
	var quick []store.Transaction
	if code, raw := a.call("POST", "/api/v1/quick", map[string]any{"text": "12.50 lunch"}, &quick); code != 201 || len(quick) != 1 {
		t.Fatalf("quick add: %d %s", code, raw)
	}
	if q := quick[0]; q.Kind != tx.Kind || q.Amount != tx.Amount || q.AccountID != tx.AccountID || q.Description != tx.Description || q.Date != tx.Date || q.Source != tx.Source ||
		q.AmountHome != tx.AmountHome || q.CategoryName != tx.CategoryName || q.UserID != tx.UserID {
		t.Fatalf("quick add %+v, capture %+v", q, tx)
	}
	before = len(txsOf(a))

	// the same request again: the same answer, nothing added
	for i := 0; i < 2; i++ {
		code, again := capture(a, "req-0000000000000001", "12.50 lunch", nil)
		if code != 201 || again["id"] != m["id"] || again["title"] != m["title"] || again["url"] != m["url"] {
			t.Fatalf("retry: %d %v", code, again)
		}
	}
	// the same id with other words is not a retry
	if code, m := capture(a, "req-0000000000000001", "99 dinner", nil); code != 400 || m["error"] != "this was already added with other words" {
		t.Fatalf("other words: %d %v", code, m)
	}
	// the same id from another person is their own request
	if code, m := capture(b, "req-0000000000000001", "12.50 lunch", nil); code != 400 || m["error"] != "add an account in Pocket Money first" {
		t.Fatalf("member, same id: %d %v", code, m)
	}
	if n := len(txsOf(a)); n != before {
		t.Fatalf("retries added %d", n-before)
	}

	// what cannot be read as an expense is refused, with a way out
	for _, c := range []struct{ id, text, want string }{
		{"req-0000000000000002", "lunch with Ana", `write the amount and what it was for, like "12.50 lunch"`},
		{"req-0000000000000003", "0 lunch", `write the amount and what it was for, like "12.50 lunch"`},
		{"req-0000000000000004", "+1500 plata", "only expenses can be added this way; add income and transfers in Pocket Money"},
		{"req-0000000000000005", "200 >savings", "only expenses can be added this way; add income and transfers in Pocket Money"},
	} {
		if code, m := capture(a, c.id, c.text, nil); code != 400 || m["error"] != c.want {
			t.Fatalf("%q: %d %v", c.text, code, m)
		}
	}
	if code, m := capture(a, "short", "5 coffee", nil); code != 400 {
		t.Fatalf("short request id: %d %v", code, m)
	}
	if code, m := capture(a, "req-0000000000000006", "5 coffee", map[string]any{"kind": "income"}); code != 400 {
		t.Fatalf("unknown kind: %d %v", code, m)
	}
	if n := len(txsOf(a)); n != before {
		t.Fatalf("refusals added %d", n-before)
	}
	// a refused line can be sent again with the same id once it is fixed
	if code, m := capture(a, "req-0000000000000002", "18 lunch with Ana", nil); code != 201 || m["title"] != "Lunch with Ana · 18.00 BAM · Cash" {
		t.Fatalf("fixed line: %d %v", code, m)
	}

	// the day sent with the line; a day written in the line wins over it
	if code, m := capture(a, "req-0000000000000007", "7 parking", map[string]any{"date": "2026-06-09"}); code != 201 {
		t.Fatalf("with a day: %d %v", code, m)
	}
	if code, m := capture(a, "req-0000000000000008", "8 taxi 2026-06-11", map[string]any{"date": "2026-06-09"}); code != 201 {
		t.Fatalf("a day in the line: %d %v", code, m)
	}
	// more lines are the notes; another currency, account and a tag work as in the quick add
	if code, m := capture(a, "req-0000000000000009", "20 eur hotel tip @euro #trip\nroom 12\nvery kind", nil); code != 201 || m["title"] != "Hotel tip · 20.00 EUR · Euro card" {
		t.Fatalf("notes and account: %d %v", code, m)
	}
	if code, m := capture(a, "req-000000000000000a", "3 eur gelato", nil); code != 201 || !strings.HasSuffix(m["title"].(string), "BAM (3.00 EUR) · Cash") {
		t.Fatalf("another currency: %d %v", code, m)
	}
	// a title alone is the line as well
	if code, m := capture(a, "req-000000000000000b", "", map[string]any{"title": "4.20 burek"}); code != 201 || m["title"] != "Burek · 4.20 BAM · Cash" {
		t.Fatalf("title: %d %v", code, m)
	}
	byText := map[string]store.Transaction{}
	for _, x := range txsOf(a) {
		byText[x.Description] = x
	}
	if x := byText["Parking"]; x.Date != "2026-06-09" || x.Amount != 700 || x.Time != "" {
		t.Fatalf("parking: %+v", x)
	}
	if x := byText["Taxi"]; x.Date != "2026-06-11" || x.Amount != 800 {
		t.Fatalf("taxi: %+v", x)
	}
	if x := byText["Hotel tip"]; x.AccountID != l.euro.ID || x.Amount != 2000 || x.Currency != "EUR" || x.Notes != "room 12\nvery kind" || len(x.Tags) != 1 || x.Tags[0] != "trip" || x.Date != today {
		t.Fatalf("hotel tip: %+v", x)
	}
	if x := byText["Gelato"]; x.AccountID != l.cash.ID || x.ForeignAmount != 300 || x.ForeignCurrency != "EUR" || x.Amount <= 300 {
		t.Fatalf("gelato: %+v", x)
	}

	// each person books on their own accounts only
	if got := txsOf(b); len(got) != 0 {
		t.Fatalf("member has %d transactions", len(got))
	}
	if code, m := capture(b, "req-000000000000000c", "5 coffee @cash", nil); code != 400 {
		t.Fatalf("member books on the owner's account: %d %v", code, m)
	}
	// someone who may only look at a shared account cannot book on it either
	if code := a.status("PUT", "/api/v1/accounts/"+itoa(l.cash.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "viewer"}}}); code != 200 {
		t.Fatalf("share: %d", code)
	}
	before = len(txsOf(a))
	if code, m := capture(b, "req-000000000000000d", "5 coffee @cash", nil); code != 400 || m["error"] != "add an account in Pocket Money first" {
		t.Fatalf("viewer books: %d %v", code, m)
	}
	if n := len(txsOf(a)); n != before {
		t.Fatalf("a viewer added %d", n-before)
	}
	// with the right to book, it lands on the shared account, in the owner's ledger, as the member's entry
	a.status("PUT", "/api/v1/accounts/"+itoa(l.cash.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "editor"}}})
	if code, m := capture(b, "req-000000000000000e", "5 coffee", nil); code != 201 || m["title"] != "Coffee · 5.00 BAM · Cash" {
		t.Fatalf("editor books: %d %v", code, m)
	}
	if x := txsOf(a)[0]; x.Description != "Coffee" || x.AccountID != l.cash.ID || x.CreatedBy == nil || *x.CreatedBy != bu.ID {
		t.Fatalf("the member's coffee: %+v", x)
	}
}

// The same line sent several times at once books one expense.
func TestPocketCaptureAtOnce(t *testing.T) {
	s, a, _, _ := newPerUserServer(t)
	t.Cleanup(s.receiptWork.Wait)
	seedCapLedger(t, a)
	before := len(txsOf(a))
	const n = 8
	ids := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, m := capture(a, "req-00000000000000aa", "9 only once", nil)
			id, _ := m["id"].(string)
			if code != 201 {
				id = "HTTP " + itoa(int64(code))
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]int{}
	for id := range ids {
		seen[id]++
	}
	if len(seen) != 1 {
		t.Errorf("answers: %v", seen)
	}
	if got := len(txsOf(a)) - before; got != 1 {
		t.Errorf("%d expenses booked, want 1", got)
	}
}

// newCapLinkServer is a Money registered with capCore, with the owner the
// pcx_owner_* tokens act for and their own API token.
func newCapLinkServer(t *testing.T) (*Server, *capCore, string, *peer) {
	t.Helper()
	fc := &capCore{}
	s, base, me := newLinkServer(t, fc.handler(), nil)
	t.Cleanup(s.receiptWork.Wait)
	return s, fc, base, me
}

func TestPocketLinkTokens(t *testing.T) {
	s, _, base, me := newCapLinkServer(t)
	l := seedCapLedger(t, me)
	tok := func(name string) *peer { return &peer{t: t, base: base, token: name} }
	line := func(requestID string) map[string]any {
		return map[string]any{"request_id": requestID, "kind": "expense", "text": "6 kafa"}
	}
	tx := "/api/v1/transactions/" + itoa(l.lunch.ID)
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_owner_search", "GET", "/api/v1/pocket/search?q=lunch", nil, 200},
		{"pcx_owner_day", "GET", "/api/v1/pocket/day/2026-06-02", nil, 200},
		{"pcx_owner_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_owner_capture", "POST", "/api/v1/pocket/capture", line("req-0000000000000001"), 201},
		{"pcx_owner_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_owner_search", "GET", "/api/v1/pocket/day/2026-06-02", nil, 403},
		{"pcx_owner_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_owner_search", "POST", "/api/v1/pocket/capture", line("req-0000000000000002"), 403},
		{"pcx_owner_day", "GET", "/api/v1/pocket/search?q=lunch", nil, 403},
		{"pcx_owner_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_owner_day", "POST", "/api/v1/pocket/capture", line("req-0000000000000003"), 403},
		{"pcx_owner_widget", "GET", "/api/v1/pocket/search?q=lunch", nil, 403},
		{"pcx_owner_widget", "GET", "/api/v1/pocket/day/2026-06-02", nil, 403},
		{"pcx_owner_widget", "POST", "/api/v1/pocket/capture", line("req-0000000000000004"), 403},
		{"pcx_owner_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_owner_capture", "GET", "/api/v1/pocket/search?q=lunch", nil, 403},
		{"pcx_owner_capture", "GET", "/api/v1/pocket/day/2026-06-02", nil, 403},
		// the rest of the app, read or written
		{"pcx_owner_search", "GET", "/api/v1/transactions?q=lunch", nil, 403},
		{"pcx_owner_search", "GET", tx, nil, 403},
		{"pcx_owner_search", "GET", "/api/v1/accounts", nil, 403},
		{"pcx_owner_search", "GET", "/api/v1/categories", nil, 403},
		{"pcx_owner_search", "GET", "/firefly/api/v1/transactions", nil, 403},
		{"pcx_owner_day", "GET", "/api/v1/transactions?from=2026-06-02&to=2026-06-02", nil, 403},
		{"pcx_owner_day", "GET", "/api/v1/reports/calendar", nil, 403},
		{"pcx_owner_day", "GET", "/firefly/api/v1/transactions", nil, 403},
		{"pcx_owner_widget", "GET", "/api/v1/dashboard", nil, 403},
		{"pcx_owner_widget", "GET", "/api/v1/home", nil, 403},
		{"pcx_owner_widget", "GET", "/api/v1/budgets", nil, 403},
		{"pcx_owner_widget", "GET", "/api/v1/subscriptions", nil, 403},
		{"pcx_owner_widget", "GET", "/api/v1/accounts", nil, 403},
		{"pcx_owner_capture", "POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": l.cash.ID, "amount": 100}, 403},
		{"pcx_owner_capture", "POST", "/api/v1/quick", map[string]any{"text": "6 kafa"}, 403},
		{"pcx_owner_capture", "POST", "/api/v1/quick/parse", map[string]any{"text": "6 kafa"}, 403},
		{"pcx_owner_capture", "PUT", tx, map[string]any{"amount": 1}, 403},
		{"pcx_owner_capture", "DELETE", tx, nil, 403},
		{"pcx_owner_capture", "GET", tx, nil, 403},
		{"pcx_owner_capture", "GET", "/api/v1/accounts", nil, 403},
		{"pcx_owner_capture", "GET", "/api/v1/settings", nil, 403},
		{"pcx_owner_capture", "GET", "/api/v1/export/money.json", nil, 403},
		// the app's own scopes do not open the new routes, and still open their own
		{"pcx_owner_read", "GET", "/api/v1/pocket/search?q=lunch", nil, 403},
		{"pcx_owner_read", "GET", "/api/v1/pocket/day/2026-06-02", nil, 403},
		{"pcx_owner_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_owner_read", "POST", "/api/v1/pocket/capture", line("req-0000000000000005"), 403},
		{"pcx_owner_write", "POST", "/api/v1/pocket/capture", line("req-0000000000000006"), 403},
		{"pcx_owner_write", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_owner_write", "GET", "/api/v1/pocket/search?q=lunch", nil, 403},
		{"pcx_owner_read", "GET", "/api/v1/transactions?q=lunch", nil, 200},
		{"pcx_owner_write", "GET", "/api/v1/accounts", nil, 200},
		{"pcx_nobody_widget", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		if code, raw := tok(c.tok).call(c.method, c.path, c.body, nil); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, raw)
		}
	}
	if code := (&peer{t: t, base: base, token: "nope"}).status("GET", "/api/v1/pocket/widget", nil); code != 401 {
		t.Errorf("no sign-in: %d", code)
	}

	// the token acts for its person only
	if got := capItems(t, capGet(t, tok("pcx_owner_search"), "/api/v1/pocket/search?q=lunch")); len(got) != 1 || got[0]["title"] != "Lunch" {
		t.Fatalf("the owner through the link: %v", got)
	}
	if got := capItems(t, capGet(t, tok("pcx_owner_day"), "/api/v1/pocket/day/2026-06-02")); len(got) != 4 {
		t.Fatalf("the owner's day through the link: %v", got)
	}
	for _, q := range []string{"lunch", "cash", "food", "kafa"} {
		if got := capItems(t, capGet(t, tok("pcx_other_search"), "/api/v1/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("someone else through the link finds %q: %v", q, got)
		}
	}
	if d := capGet(t, tok("pcx_other_day"), "/api/v1/pocket/day/2026-06-02"); len(capItems(t, d)) != 0 || d["summary"] != nil {
		t.Fatalf("someone else's day through the link: %v", d)
	}
	if w := capGet(t, tok("pcx_other_widget"), "/api/v1/pocket/widget"); len(capItems(t, w)) != 0 || len(capMetrics(w)) != 0 || w["line"] != nil {
		t.Fatalf("someone else's widget through the link: %v", w)
	}
	if code, m := capture(tok("pcx_other_capture"), "req-0000000000000001", "6 kafa", nil); code != 400 || m["error"] != "add an account in Pocket Money first" {
		t.Fatalf("someone else has no account to book on: %d %v", code, m)
	}

	// only the one allowed capture landed, booked as the app that sent it; a retry adds nothing
	code, again := capture(tok("pcx_owner_capture"), "req-0000000000000001", "6 kafa", nil)
	if code != 201 || again["title"] != "Kafa · 6.00 BAM · Cash" {
		t.Fatalf("retry through the link: %d %v", code, again)
	}
	var kafa []store.Transaction
	for _, x := range txsOf(me) {
		if x.Description == "Kafa" {
			kafa = append(kafa, x)
		}
	}
	if len(kafa) != 1 || kafa[0].Source != "hub" || kafa[0].Amount != 600 || kafa[0].AccountID != l.cash.ID || again["id"] != itoa(kafa[0].ID) {
		t.Fatalf("booked through the link: %+v", kafa)
	}
	// Core hears of it like of any other new transaction, once
	var events int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM pocket_outbox WHERE json_extract(body,'$.topic')='money.transaction_created' AND json_extract(body,'$.subject')=?`,
		"transaction:"+itoa(kafa[0].ID)).Scan(&events)
	if events != 1 {
		t.Fatalf("%d events for the captured expense", events)
	}
	// and the owner's history names the app
	var audit auditPage
	me.call("GET", "/api/v1/accounts/"+itoa(l.cash.ID)+"/audit", nil, &audit)
	if raw, _ := json.Marshal(audit); !strings.Contains(string(raw), "Pocket Hub (Pocket Core link)") {
		t.Fatalf("audit does not name the app: %s", raw)
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/transactions"), false},
		{core.ScopeWidget, req("GET", "/api/v1/dashboard"), false},
		{core.ScopeCapture, req("GET", "/api/v1/pocket/capture"), false},
		{core.ScopeCapture, req("POST", "/api/v1/transactions"), false},
		{core.ScopeCapture, req("POST", "/api/v1/quick"), false},
		{"transactions:read", req("GET", "/api/v1/pocket/search?q=x"), false},
		{"transactions:read", req("GET", "/api/v1/pocket/widget"), false},
		{"transactions:write", req("POST", "/api/v1/pocket/capture"), false},
		{"accounts:read", req("GET", "/api/v1/pocket/widget"), false},
		{"transactions:read", req("GET", "/api/v1/transactions"), true},
		{"transactions:write", req("POST", "/api/v1/transactions"), true},
	} {
		if got := linkAllows(&principal{Kind: "link", Scopes: []string{c.scope}}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Money tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	s, fc, _, _ := newCapLinkServer(t)
	if err := s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "money" || m["category"] != "Money" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	cap, _ := m["capture"].(map[string]any)
	if search == nil || day == nil || widget == nil || cap == nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, cap)
	}
	if b := search["bangs"].([]any); len(b) != 2 || b[0] != "money" || b[1] != "spend" || len(search["kinds"].([]any)) != 3 {
		t.Fatalf("search: %v", search)
	}
	if day["label"] != "Money" || day["one"] != "payment" || day["many"] != "payments" || day["unit"] != "amount" || widget["title"] != "This month" {
		t.Fatalf("day and widget: %v %v", day, widget)
	}
	if k := cap["kinds"].([]any); len(k) != 1 || k[0].(map[string]any)["kind"] != "expense" || len(k[0].(map[string]any)["fields"].([]any)) != 1 ||
		k[0].(map[string]any)["fields"].([]any)[0] != "date" {
		t.Fatalf("capture kinds: %v", cap)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"accounts:read", "transactions:read", "transactions:write", "jobs:read", "jobs:run", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeCapture} {
		if !offered[sc] {
			t.Errorf("manifest does not offer %s", sc)
		}
	}
}
