package server

import (
	"context"
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
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM pocket_outbox WHERE topic='money.subscription_paid'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate paid event", count)
	}
}
