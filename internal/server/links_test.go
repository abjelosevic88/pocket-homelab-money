package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
			link := map[string][3]string{
				"pcx_full":   {"accounts:read transactions:write", "garage", "Pocket Garage"},
				"pcx_read":   {"accounts:read", "garage", "Pocket Garage"},
				"pcx_travel": {"transactions:read", "travel", "Pocket Travel"},
				"pcx_day":    {"transactions:read", "day", "Pocket Day"},
			}[in.Token]
			if link[0] == "" {
				_, _ = w.Write([]byte(`{"active":false}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "scope": link[0], "audience": "money",
				"user": map[string]any{"id": 1, "email": "owner@example.com", "name": "Owner"},
				"app":  map[string]any{"id": 7, "slug": link[1], "name": link[2]}})
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
	if err := s.core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
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
	// Same operation is returned without booking or auditing twice.
	request := map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 8000, "currency": "BAM", "description": "Škoda Fabia fuel", "source": "manual", "external_id": "fuel:12"}
	var retry store.Transaction
	if code, raw := garage.call("POST", "/api/v1/transactions", request, &retry); code != 201 || retry.ID != tx.ID {
		t.Fatalf("retry: %d %s", code, raw)
	}
	request["amount"] = 8001
	if code := garage.status("POST", "/api/v1/transactions", request); code != 409 {
		t.Fatalf("conflicting retry: %d", code)
	}
	request["amount"] = 8000
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
	for _, p := range []string{"/firefly/api/v1/accounts", "/firefly/api/v1/transactions", "/firefly/api/v1/about"} {
		if code := garage.status("GET", p, nil); code != 403 {
			t.Fatalf("Firefly API with a link token without transactions:read: %s %d", p, code)
		}
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
	if code := garage.status("POST", "/api/v1/transactions", request); code != 404 {
		t.Fatalf("deleted operation must not be recreated: %d", code)
	}
	// The owner's own transaction is still there.
	if code := me.status("GET", "/api/v1/transactions/"+itoa(mine.ID), nil); code != 200 {
		t.Fatalf("owner's transaction %d", code)
	}
}

// newLinkServer is a Money registered with a fake Core, with the owner
// (owner@example.com, whom the fake Core's link tokens act for) and their API token.
func newLinkServer(t *testing.T, fc http.Handler, tweak func(*config.Config)) (*Server, string, *peer) {
	t.Helper()
	coreSrv := httptest.NewServer(fc)
	t.Cleanup(coreSrv.Close)
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DBPath: dir + "/t.db", FilesDir: dir + "/files", AuthMode: "local", DefaultCurrency: "BAM", FXProvider: "none",
		MaxUploadBytes: 1 << 20, NotifyHour: 9, UserAgent: "test", PasswordLogin: true, CoreURL: coreSrv.URL, CoreRegistrationKey: "pcreg_x", CoreAppURL: "http://money"}
	if tweak != nil {
		tweak(cfg)
	}
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
	ctx := context.Background()
	owner, err := s.firstOrSystemUser(ctx, "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.registerWithCore(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	otok, _, _ := st.CreateAPIToken(ctx, owner.ID, "test")
	return s, ts.URL, &peer{t: t, base: ts.URL, token: otok}
}

// ffGet makes a GET the way Pocket Travel's and Pocket Day's Firefly clients
// do (bearer token, Accept: application/vnd.api+json) and decodes the answer.
func ffGet(t *testing.T, base, token, path string, q url.Values, out any) int {
	t.Helper()
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.api+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("GET %s: %v: %s", u, err, raw)
		}
	}
	return res.StatusCode
}

// ffTxPage is the transaction list both clients parse (Travel's txPage, Day's
// inline struct), field for field.
type ffTxPage struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Transactions []struct {
				JournalID       string   `json:"transaction_journal_id"`
				Type            string   `json:"type"`
				Date            string   `json:"date"`
				Description     string   `json:"description"`
				Amount          string   `json:"amount"`
				CurrencyCode    string   `json:"currency_code"`
				CurrencyDec     int      `json:"currency_decimal_places"`
				ForeignAmount   *string  `json:"foreign_amount"`
				ForeignCurrency *string  `json:"foreign_currency_code"`
				CategoryName    *string  `json:"category_name"`
				BudgetName      *string  `json:"budget_name"`
				SourceName      *string  `json:"source_name"`
				DestinationName *string  `json:"destination_name"`
				Notes           *string  `json:"notes"`
				Tags            []string `json:"tags"`
				ExternalID      *string  `json:"external_id"`
				OriginalSource  *string  `json:"original_source"`
			} `json:"transactions"`
		} `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
}

func TestCoreLinkTransactionsRead(t *testing.T) {
	fc := &fakeCore{}
	s, base, me := newLinkServer(t, fc.handler(t), nil)
	ctx := context.Background()
	fc.mu.Lock()
	offers, _ := json.Marshal(fc.manifest["offers"])
	fc.mu.Unlock()
	if !strings.Contains(string(offers), `"transactions:read"`) {
		t.Fatalf("manifest offers %s", offers)
	}

	var acct store.Account
	me.call("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM", "opening_balance": 100000}, &acct)
	var cat store.Category
	me.call("POST", "/api/v1/categories", map[string]any{"name": "restoran"}, &cat)
	var dinner, coffee, salary, other store.Transaction
	me.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 4590, "description": "Trattoria", "date": "2026-06-02",
		"time": "20:15", "category_id": cat.ID, "tags": []string{"Rome 2026"}, "payee": "Trattoria da Enzo"}, &dinner)
	me.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 2000, "currency": "EUR", "description": "Espresso", "date": "2026-06-03",
		"tags": []string{"rome 2026"}}, &coffee)
	me.call("POST", "/api/v1/transactions", map[string]any{"kind": "income", "account_id": acct.ID, "amount": 300000, "description": "Plata", "date": "2026-06-02", "tags": []string{"Rome 2026"}}, &salary)
	me.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 1000, "description": "Bread", "date": "2026-06-10", "tags": []string{"Rome 2026"}}, &other)
	if dinner.ID == 0 || coffee.ID == 0 || salary.ID == 0 || other.ID == 0 {
		t.Fatal("seed transactions")
	}
	// Someone else on the box, with the same tag on the same day: never in the owner's answers.
	member, _ := s.firstOrSystemUser(ctx, "member@example.com", "Member")
	mtok, _, _ := s.store.CreateAPIToken(ctx, member.ID, "m")
	them := &peer{t: t, base: base, token: mtok}
	var macct store.Account
	them.call("POST", "/api/v1/accounts", map[string]any{"name": "Theirs", "currency": "BAM"}, &macct)
	them.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": macct.ID, "amount": 777, "description": "Not yours", "date": "2026-06-02", "tags": []string{"Rome 2026"}}, nil)

	// ---- Pocket Travel's calls, exactly (internal/firefly/firefly.go), at <money>/firefly ----
	travel := "pcx_travel"
	ff := base + "/firefly"
	var about struct {
		Data struct {
			Version    string `json:"version"`
			PHPVersion string `json:"php_version"`
		} `json:"data"`
	}
	if code := ffGet(t, ff, travel, "/api/v1/about", nil, &about); code != 200 || about.Data.Version == "" || !strings.HasPrefix(about.Data.PHPVersion, "pocket-money") {
		t.Fatalf("travel about %d %+v", code, about)
	}
	// Tags (paged) → TagByName, case-insensitive.
	var tagID string
	for page := 1; page < 50; page++ {
		var res struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					Tag string `json:"tag"`
				} `json:"attributes"`
			} `json:"data"`
			Meta struct {
				Pagination struct {
					TotalPages int `json:"total_pages"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if code := ffGet(t, ff, travel, "/api/v1/tags", url.Values{"limit": {"100"}, "page": {strconv.Itoa(page)}}, &res); code != 200 {
			t.Fatalf("travel tags %d", code)
		}
		for _, d := range res.Data {
			if strings.EqualFold(d.Attributes.Tag, "rome 2026") {
				tagID = d.ID
			}
		}
		if page >= res.Meta.Pagination.TotalPages {
			break
		}
	}
	if tagID == "" {
		t.Fatal("travel did not find the trip's tag")
	}
	// TagTransactions: withdrawals with the tag inside the trip's dates.
	var got []string
	for page := 1; page < 100; page++ {
		var res ffTxPage
		q := url.Values{"limit": {"100"}, "page": {strconv.Itoa(page)}, "type": {"withdrawal"}, "start": {"2026-06-01"}, "end": {"2026-06-05"}}
		if code := ffGet(t, ff, travel, "/api/v1/tags/"+url.PathEscape(tagID)+"/transactions", q, &res); code != 200 {
			t.Fatalf("travel tag transactions %d", code)
		}
		for _, g := range res.Data {
			for _, x := range g.Attributes.Transactions {
				if x.Type != "withdrawal" {
					continue
				}
				amt, err := strconv.ParseFloat(x.Amount, 64)
				if err != nil || x.CurrencyCode == "" || x.CurrencyDec != 2 || len(x.Date) < 10 || x.JournalID == "" || g.ID == "" {
					t.Fatalf("travel cannot read %+v", x)
				}
				if x.ForeignAmount != nil {
					if x.ForeignCurrency == nil {
						t.Fatalf("foreign amount without currency %+v", x)
					}
					if _, err := strconv.ParseFloat(*x.ForeignAmount, 64); err != nil {
						t.Fatalf("foreign amount %q", *x.ForeignAmount)
					}
				}
				if x.OriginalSource == nil || !strings.HasPrefix(*x.OriginalSource, "pocket-money") {
					t.Fatalf("original_source %+v", x.OriginalSource)
				}
				got = append(got, fmt.Sprintf("%s %s %.2f", x.Date[:10], x.Description, amt))
				if x.Description == "Trattoria" && (x.CategoryName == nil || *x.CategoryName != "restoran" || x.DestinationName == nil || *x.DestinationName != "Trattoria da Enzo" ||
					x.SourceName == nil || *x.SourceName != "UniCredit" || len(x.Tags) != 1) {
					t.Fatalf("dinner as travel reads it %+v", x)
				}
			}
		}
		if page >= res.Meta.Pagination.TotalPages {
			break
		}
	}
	if j := strings.Join(got, " | "); !strings.Contains(j, "2026-06-02 Trattoria 45.90") || !strings.Contains(j, "2026-06-03 Espresso") || len(got) != 2 {
		t.Fatalf("travel's trip costs: %s", j)
	}

	// ---- Pocket Day's calls, exactly (internal/sources/firefly.go) ----
	day := "pcx_day"
	if code := ffGet(t, ff, day, "/api/v1/about", nil, &about); code != 200 || about.Data.Version == "" {
		t.Fatalf("day about %d", code)
	}
	var items []string
	for page := 1; page < 50; page++ {
		var res ffTxPage
		q := url.Values{"start": {"2026-06-02"}, "end": {"2026-06-02"}, "type": {"withdrawal"}, "limit": {"100"}, "page": {strconv.Itoa(page)}}
		if code := ffGet(t, ff, day, "/api/v1/transactions", q, &res); code != 200 {
			t.Fatalf("day transactions %d", code)
		}
		for _, g := range res.Data {
			for _, x := range g.Attributes.Transactions {
				if x.Type != "withdrawal" {
					continue
				}
				ts, err := time.Parse(time.RFC3339, x.Date)
				if err != nil {
					t.Fatalf("day cannot parse the date %q", x.Date)
				}
				items = append(items, fmt.Sprintf("%s %s %s %s", ts.Format("15:04"), x.Description, x.Amount, x.CurrencyCode))
			}
		}
		if page >= res.Meta.Pagination.TotalPages {
			break
		}
	}
	if len(items) != 1 || !strings.HasPrefix(items[0], "20:15 Trattoria 45.9") || !strings.HasSuffix(items[0], "BAM") {
		t.Fatalf("day's spending on 2026-06-02: %v", items)
	}

	// ---- everything else transactions:read covers ----
	id := itoa(dinner.ID)
	for _, p := range []string{
		"/api/v1/transactions", "/api/v1/transactions/" + id, "/api/v1/transactions/" + itoa(salary.ID), "/api/v1/tags", "/api/v1/accounts", "/api/v1/categories", "/api/v1/auth/me",
		"/firefly/api/v1/about/user", "/firefly/api/v1/accounts", "/firefly/api/v1/accounts/" + itoa(acct.ID), "/firefly/api/v1/accounts/" + itoa(acct.ID) + "/transactions",
		"/firefly/api/v1/categories", "/firefly/api/v1/categories/" + itoa(cat.ID), "/firefly/api/v1/tags", "/firefly/api/v1/tags/" + tagID, "/firefly/api/v1/tags/Rome%202026/transactions",
		"/firefly/api/v1/transactions", "/firefly/api/v1/transactions/" + id, "/firefly/api/v1/currencies",
	} {
		if code := ffGet(t, base, travel, p, nil, nil); code != 200 {
			t.Fatalf("GET %s with transactions:read: %d", p, code)
		}
	}
	var list struct {
		Items []store.Transaction `json:"items"`
	}
	(&peer{t: t, base: base, token: travel}).call("GET", "/api/v1/transactions?limit=100", nil, &list)
	if len(list.Items) != 4 {
		t.Fatalf("native list through the link: %d items, want the owner's 4", len(list.Items))
	}

	// ---- and nothing that changes anything, nor what it does not cover ----
	reader := &peer{t: t, base: base, token: travel}
	for _, c := range []struct{ m, p string }{
		{"POST", "/api/v1/transactions"}, {"PUT", "/api/v1/transactions/" + id}, {"DELETE", "/api/v1/transactions/" + id},
		{"POST", "/api/v1/transactions/bulk"}, {"POST", "/api/v1/tags"}, {"PUT", "/api/v1/tags/1"}, {"POST", "/api/v1/accounts"},
		{"GET", "/api/v1/accounts/" + itoa(acct.ID) + "/categories"}, {"GET", "/api/v1/reports/overview"}, {"GET", "/api/v1/export/money.json"},
		{"GET", "/api/v1/audit"}, {"GET", "/api/v1/tokens"}, {"GET", "/api/v1/settings"},
		{"POST", "/firefly/api/v1/transactions"}, {"PUT", "/firefly/api/v1/transactions/" + id}, {"DELETE", "/firefly/api/v1/transactions/" + id},
		{"GET", "/firefly/api/v1/bills"}, {"GET", "/firefly/api/v1/exchange-rates"}, {"GET", "/firefly/api/v1/insight/expense/total"},
		{"GET", "/firefly/api/transaction-templates"},
	} {
		if code := reader.status(c.m, c.p, map[string]any{"amount": 1, "transactions": []map[string]any{{"type": "withdrawal", "amount": "1", "source_id": acct.ID}}}); code != 403 {
			t.Fatalf("%s %s with transactions:read: %d, want 403", c.m, c.p, code)
		}
	}
	var after store.Transaction
	if code, _ := me.call("GET", "/api/v1/transactions/"+id, nil, &after); code != 200 || after.Amount != 4590 {
		t.Fatalf("the dinner after a read-only link tried to change it: %d %+v", code, after)
	}
}
