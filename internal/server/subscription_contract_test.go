package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
	"sync"
	"testing"
)

func TestSubscriptionOccurrenceCommitsPaymentRecurrenceAndEvents(t *testing.T) {
	fc := &fakeCore{}
	s, _, owner := newLinkServer(t, fc.handler(t), nil)
	var account store.Account
	owner.call("POST", "/api/v1/accounts", map[string]any{"name": "Test", "currency": "EUR"}, &account)
	var subscription store.Subscription
	code, raw := owner.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Monthly", "amount": 100, "currency": "EUR", "account_id": account.ID, "freq": "monthly", "next_date": "2026-10-01", "mode": "remind"}, &subscription)
	if code != 201 {
		t.Fatal(code, string(raw))
	}
	x, err := s.store.GetSubscription(t.Context(), subscription.ID)
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.store.GetUser(t.Context(), x.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), userKey, user)
	if _, err = s.store.DB.Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON pocket_outbox BEGIN SELECT RAISE(ABORT,'queue unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.bookSubscription(ctx, x, "2026-10-01", nil, nil, true); err == nil {
		t.Fatal("payment survived queue failure")
	}
	var count int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
	fresh, _ := s.store.GetSubscription(ctx, x.ID)
	if fresh.NextDate != x.NextDate || fresh.DoneCount != x.DoneCount {
		t.Fatal("recurrence advanced on rollback")
	}
	s.store.DB.Exec(`DROP TRIGGER reject_event`)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *x
			if _, msg, e := s.bookSubscription(ctx, &copy, "2026-10-01", nil, nil, true); e != nil || msg != "" {
				t.Errorf("retry: %v %s", e, msg)
			}
		}()
	}
	wg.Wait()
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate payment", count)
	}
	fresh, _ = s.store.GetSubscription(ctx, x.ID)
	if fresh.NextDate != "2026-11-01" || fresh.DoneCount != x.DoneCount+1 {
		t.Fatal("recurrence", fresh.NextDate, fresh.DoneCount)
	}
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM pocket_outbox WHERE json_extract(body,'$.topic')='money.subscription_paid'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate paid event", count)
	}
}

func TestSubscriptionSavedEventOnCreateAndUpdate(t *testing.T) {
	fc := &fakeCore{}
	s, _, owner := newLinkServer(t, fc.handler(t), nil)
	saved := func() []map[string]any {
		t.Helper()
		rows, err := s.store.DB.Query(`SELECT body FROM pocket_outbox WHERE json_extract(body,'$.topic')='money.subscription_saved' ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var body string
			rows.Scan(&body)
			var ev map[string]any
			if err := json.Unmarshal([]byte(body), &ev); err != nil {
				t.Fatal(err)
			}
			out = append(out, ev)
		}
		return out
	}
	var x store.Subscription
	code, raw := owner.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Netflix", "amount": 1299, "currency": "EUR", "freq": "monthly", "every": 1, "next_date": "2026-12-01"}, &x)
	if code != 201 {
		t.Fatal(code, string(raw))
	}
	evs := saved()
	if len(evs) != 1 {
		t.Fatal("events after create", len(evs))
	}
	if evs[0]["subject"] != fmt.Sprintf("subscription:%d", x.ID) {
		t.Fatal("subject", evs[0]["subject"])
	}
	p := evs[0]["payload"].(map[string]any)
	if p["subscription_id"] != float64(x.ID) || p["subscription"] != "Netflix" || p["amount"] != float64(1299) || p["currency"] != "EUR" ||
		p["freq"] != "monthly" || p["every"] != float64(1) || p["next_date"] != "2026-12-01" || p["end_date"] != "" || p["active"] != true || p["user_email"] != "owner@example.com" {
		t.Fatal("create payload", p)
	}
	code, raw = owner.call("PUT", fmt.Sprintf("/api/v1/subscriptions/%d", x.ID), map[string]any{"amount": 1499, "end_date": "2027-06-01", "active": false}, nil)
	if code != 200 {
		t.Fatal(code, string(raw))
	}
	evs = saved()
	if len(evs) != 2 {
		t.Fatal("events after update", len(evs))
	}
	p = evs[1]["payload"].(map[string]any)
	if p["amount"] != float64(1499) || p["end_date"] != "2027-06-01" || p["active"] != false || p["next_date"] != "2026-12-01" {
		t.Fatal("update payload", p)
	}
	// a rejected save publishes nothing
	code, _ = owner.call("PUT", fmt.Sprintf("/api/v1/subscriptions/%d", x.ID), map[string]any{"freq": "hourly"}, nil)
	if code != 400 {
		t.Fatal("bad freq", code)
	}
	if len(saved()) != 2 {
		t.Fatal("event for a rejected save")
	}
}
