package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
	"net/http"
	"sync"
	"testing"
)

func TestConcurrentTransactionRetryAndOutboxRollback(t *testing.T) {
	fc := &fakeCore{}
	s, base, owner := newLinkServer(t, fc.handler(t), nil)
	var acct store.Account
	owner.call("POST", "/api/v1/accounts", map[string]any{"name": "Test", "currency": "EUR"}, &acct)
	body := map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 100, "currency": "EUR", "external_id": "retry:1"}
	b, _ := json.Marshal(body)
	results := make(chan int64, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", base+"/api/v1/transactions", bytes.NewReader(b))
			req.Header.Set("Authorization", "Bearer pcx_full")
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer res.Body.Close()
			var tx store.Transaction
			err = json.NewDecoder(res.Body).Decode(&tx)
			if res.StatusCode != 201 || err != nil {
				errs <- fmt.Errorf("status %d: %v", res.StatusCode, err)
				return
			}
			results <- tx.ID
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var id int64
	for got := range results {
		if id == 0 {
			id = got
		}
		if id != got {
			t.Fatalf("retry created %d and %d", id, got)
		}
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE external_id='garage:retry:1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d transactions", n)
	}
	// Failure to persist publication work must roll back the financial write
	// and operation receipt, so the same request remains retryable.
	if _, err := s.store.DB.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON pocket_outbox BEGIN SELECT RAISE(FAIL,'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	body["external_id"] = "retry:2"
	garage := &peer{t: t, base: base, token: "pcx_full"}
	if code := garage.status("POST", "/api/v1/transactions", body); code != 500 {
		t.Fatalf("acknowledged failed outbox: %d", code)
	}
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE external_id='garage:retry:2'`).Scan(&n)
	if n != 0 {
		t.Fatal("write survived outbox rollback")
	}
	s.store.DB.Exec(`DROP TRIGGER fail_outbox`)
	if code := garage.status("POST", "/api/v1/transactions", body); code != 201 {
		t.Fatalf("retry after rollback: %d", code)
	}
}
