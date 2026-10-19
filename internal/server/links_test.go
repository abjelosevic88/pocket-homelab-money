package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// fakeCore answers registration and introspection like Pocket Core does for
// app links: pcx_ tokens are delegated, for the "money" audience only.
type fakeCore struct {
	mu       sync.Mutex
	manifest map[string]any
}

func (f *fakeCore) handler(t *testing.T) http.Handler {
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
			scope := map[string]string{"pcx_full": "accounts:read transactions:write", "pcx_read": "accounts:read"}[in.Token]
			if scope == "" {
				_, _ = w.Write([]byte(`{"active":false}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "scope": scope, "audience": "money",
				"user": map[string]any{"id": 1, "email": "owner@example.com", "name": "Owner"},
				"app":  map[string]any{"id": 7, "slug": "garage", "name": "Pocket Garage"}})
		default:
			http.NotFound(w, r)
		}
	})
}

func TestCoreLinkScopes(t *testing.T) {
	fc := &fakeCore{}
	coreSrv := httptest.NewServer(fc.handler(t))
	t.Cleanup(coreSrv.Close)
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DBPath: dir + "/t.db", FilesDir: dir + "/files", AuthMode: "local", DefaultCurrency: "BAM", FXProvider: "none",
		MaxUploadBytes: 1 << 20, NotifyHour: 9, UserAgent: "test", PasswordLogin: true, CoreURL: coreSrv.URL, CoreRegistrationKey: "pcreg_x", CoreAppURL: "http://money"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(cfg, st, log, nil)
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.registerWithCore(ctx); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	offers, _ := json.Marshal(fc.manifest["offers"])
	fc.mu.Unlock()
	if !strings.Contains(string(offers), `"transactions:write"`) || !strings.Contains(string(offers), `"accounts:read"`) {
		t.Fatalf("manifest offers %s", offers)
	}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)

	owner, err := s.firstOrSystemUser(ctx, "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	otok, _, _ := st.CreateAPIToken(ctx, owner.ID, "test")
	me := &peer{t: t, base: ts.URL, token: otok}
	garage := &peer{t: t, base: ts.URL, token: "pcx_full"}
	readOnly := &peer{t: t, base: ts.URL, token: "pcx_read"}

	var acct store.Account
	me.call("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM", "opening_balance": 100000}, &acct)
	var mine store.Transaction
	me.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 900, "description": "Kafa"}, &mine)

	// What the link covers.
	var accts []store.Account
	if code, raw := garage.call("GET", "/api/v1/accounts", nil, &accts); code != 200 || len(accts) != 1 {
		t.Fatalf("linked accounts %d %s", code, raw)
	}
	if code := garage.status("GET", "/api/v1/categories", nil); code != 200 {
		t.Fatalf("linked categories %d", code)
	}
	var tx store.Transaction
	if code, raw := garage.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 8000, "currency": "BAM",
		"description": "Škoda Fabia fuel", "source": "manual", "external_id": "fuel:12"}, &tx); code != 201 {
		t.Fatalf("linked create %d %s", code, raw)
	}
	if tx.Source != "garage" || tx.ExternalID != "garage:fuel:12" || tx.UserID != owner.ID {
		t.Fatalf("a linked app's transaction carries its name: %+v", tx)
	}
	if code := garage.status("PUT", "/api/v1/transactions/"+itoa(tx.ID), map[string]any{"amount": 8500}); code != 200 {
		t.Fatalf("linked update own %d", code)
	}

	// What it does not.
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/transactions"}, {"GET", "/api/v1/reports/overview"}, {"GET", "/api/v1/export/money.json"},
		{"POST", "/api/v1/accounts"}, {"DELETE", "/api/v1/accounts/" + itoa(acct.ID)}, {"GET", "/api/v1/tokens"},
		{"POST", "/api/v1/transactions/bulk"}, {"GET", "/api/v1/audit"},
	} {
		if code := garage.status(c.m, c.p, map[string]any{}); code != 403 {
			t.Fatalf("%s %s with a link token: %d, want 403", c.m, c.p, code)
		}
	}
	if code := garage.status("GET", "/firefly/api/v1/accounts", nil); code != 401 {
		t.Fatalf("Firefly API with a link token: %d", code)
	}
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		if code := garage.status(m, "/api/v1/transactions/"+itoa(mine.ID), map[string]any{"amount": 1}); code != 404 {
			t.Fatalf("%s someone else's transaction through the link: %d", m, code)
		}
	}
	if code := readOnly.status("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 1}); code != 403 {
		t.Fatalf("read-only link created a transaction: %d", code)
	}

	// The owner's audit log names the app.
	var audit struct {
		Items []map[string]any `json:"items"`
	}
	me.call("GET", "/api/v1/audit", nil, &audit)
	found := false
	for _, e := range audit.Items {
		if v, _ := e["actor_label"].(string); strings.Contains(v, "Pocket Garage") {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit should name the linked app: %+v", audit.Items)
	}
	if code := garage.status("DELETE", "/api/v1/transactions/"+itoa(tx.ID), nil); code != 204 {
		t.Fatalf("linked delete own %d", code)
	}
	// The owner's own transaction is still there.
	if code := me.status("GET", "/api/v1/transactions/"+itoa(mine.ID), nil); code != 200 {
		t.Fatalf("owner's transaction %d", code)
	}
}
