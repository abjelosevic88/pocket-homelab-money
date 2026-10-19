package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// Everything is per person: user B must not list, read, change, delete,
// search or export anything of user A's — not through the web API, not
// through an API token on the Firefly-compatible API — unless A shared an
// account with B.

type peer struct {
	t     *testing.T
	base  string
	token string // "" = the AUTH_MODE=none admin (user A)
}

func (p *peer) call(method, path string, body any, out any) (int, []byte) {
	p.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, p.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			p.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	return res.StatusCode, raw
}

func (p *peer) status(method, path string, body any) int {
	p.t.Helper()
	code, _ := p.call(method, path, body, nil)
	return code
}

func newPerUserServer(t *testing.T) (*Server, *peer, *peer, *store.User) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DBPath: dir + "/t.db", FilesDir: dir + "/files", AuthMode: "none", DefaultCurrency: "BAM", FXProvider: "none",
		MaxUploadBytes: 1 << 20, NotifyHour: 9, UserAgent: "test", PasswordLogin: true}
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
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	a := &peer{t: t, base: ts.URL}
	// the first request creates the owner (admin) in AUTH_MODE=none
	var me store.User
	if code, _ := a.call("GET", "/api/v1/auth/me", nil, &me); code != 200 || me.Role != "admin" {
		t.Fatalf("owner: %d %+v", code, me)
	}
	// a household member, the way Core / PocketID sign-ins create them
	ctx := context.Background()
	bu, err := s.firstOrSystemUser(ctx, "member@example.com", "Member")
	if err != nil || bu.Role != "member" {
		t.Fatalf("auto-created member: %v %+v", err, bu)
	}
	btok, _, err := st.CreateAPIToken(ctx, bu.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	return s, a, &peer{t: t, base: ts.URL, token: btok}, bu
}

func TestPerUserIsolation(t *testing.T) {
	s, a, b, bu := newPerUserServer(t)

	// ---- user A's books ----
	var acct store.Account
	a.call("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM", "opening_balance": 100000}, &acct)
	var cat store.Category
	a.call("POST", "/api/v1/categories", map[string]any{"name": "restoran"}, &cat)
	var tx store.Transaction
	if code, raw := a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 2500, "description": "Večera tajna", "payee": "Kod Keme", "category_id": cat.ID, "tags": []string{"tajno"}}, &tx); code != 201 {
		t.Fatalf("A tx: %d %s", code, raw)
	}
	if tx.UserID == 0 {
		t.Errorf("transaction has no owner: %+v", tx)
	}
	var tags []store.Tag
	a.call("GET", "/api/v1/tags", nil, &tags)
	if len(tags) != 1 {
		t.Fatalf("A tags: %+v", tags)
	}
	tag := tags[0]
	var sub store.Subscription
	a.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Netflix", "amount": 2499, "currency": "BAM", "account_id": acct.ID, "freq": "monthly", "next_date": store.Today(), "mode": "remind"}, &sub)
	var bud store.Budget
	a.call("POST", "/api/v1/budgets", map[string]any{"name": "Hrana", "amount": 50000, "category_ids": []int64{cat.ID}}, &bud)
	var goal store.Goal
	a.call("POST", "/api/v1/goals", map[string]any{"name": "Japan", "target": 400000, "currency": "BAM"}, &goal)
	var tpl store.Template
	a.call("POST", "/api/v1/templates", map[string]any{"name": "Kafa", "amount": 350, "account_id": acct.ID, "category_id": cat.ID}, &tpl)
	var rule store.Rule
	if code, raw := a.call("POST", "/api/v1/rules", map[string]any{"name": "uber", "match": "all", "conditions": []map[string]any{{"field": "text", "op": "contains", "value": "uber"}}, "actions": map[string]any{"category_id": cat.ID}, "active": true}, &rule); code != 201 {
		t.Fatalf("A rule: %d %s", code, raw)
	}
	var hook store.Webhook
	a.call("POST", "/api/v1/webhooks", map[string]any{"name": "day", "url": "http://127.0.0.1:1/hook", "events": []string{"transaction.created"}}, &hook)
	if code, _ := a.call("PUT", "/api/v1/settings", map[string]any{"notify_url": "http://apprise/notify/a", "ai_key": "sk-owner-secret", "firefly_url": "http://firefly", "firefly_token": "ff-owner", "ai_context": "maraka = BAM", "title": "Our Money"}, nil); code != 200 {
		t.Fatal("A settings")
	}
	// a receipt
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	fw, _ := mw.CreateFormFile("file", "racun.txt")
	fw.Write([]byte("receipt"))
	mw.Close()
	req, _ := http.NewRequest("POST", a.base+"/api/v1/transactions/"+itoa(tx.ID)+"/attachments", &mp)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 201 {
		t.Fatalf("attachment: %v %d", err, res.StatusCode)
	}
	var att store.Attachment
	_ = json.NewDecoder(res.Body).Decode(&att)
	res.Body.Close()

	// ---- user B sees an empty ledger ----
	empty := func(path string) {
		t.Helper()
		code, raw := b.call("GET", path, nil, nil)
		if code != 200 {
			t.Errorf("B GET %s = %d", path, code)
			return
		}
		body := string(raw)
		for _, leak := range []string{"UniCredit", "Večera", "Kod Keme", "restoran", "tajno", "Netflix", "Hrana", "Japan", "Kafa", "uber", "sk-owner", "ff-owner", "maraka", "apprise/notify/a"} {
			if strings.Contains(body, leak) {
				t.Errorf("B GET %s leaks %q: %s", path, leak, body)
			}
		}
	}
	for _, p := range []string{"/api/v1/accounts?archived=1", "/api/v1/transactions", "/api/v1/transactions?q=ve%C4%8Dera", "/api/v1/categories", "/api/v1/tags",
		"/api/v1/templates", "/api/v1/subscriptions", "/api/v1/budgets", "/api/v1/goals", "/api/v1/rules", "/api/v1/webhooks", "/api/v1/activity",
		"/api/v1/settings", "/api/v1/suggest?q=ve", "/api/v1/payees", "/api/v1/home", "/api/v1/dashboard", "/api/v1/reports/overview",
		"/api/v1/reports/categories", "/api/v1/reports/tags", "/api/v1/subscriptions/detect", "/api/v1/export/money.json", "/api/v1/export/transactions.csv",
		"/api/v1/ai/usage", "/api/v1/import/firefly/status", "/api/v1/settings/system", "/api/v1/tokens",
		"/firefly/api/v1/accounts", "/firefly/api/v1/transactions", "/firefly/api/v1/categories", "/firefly/api/v1/tags", "/firefly/api/v1/bills",
		"/firefly/api/transaction-templates", "/firefly/api/v1/tags/tajno/transactions"} {
		empty(p)
	}
	var list struct {
		Items []store.Transaction `json:"items"`
	}
	b.call("GET", "/api/v1/transactions", nil, &list)
	if len(list.Items) != 0 {
		t.Errorf("B lists %d transactions", len(list.Items))
	}
	var bset map[string]any
	b.call("GET", "/api/v1/settings", nil, &bset)
	if bset["ai_configured"] != false || bset["firefly_configured"] != false || bset["notify_configured"] != false || bset["title"] != "Our Money" {
		t.Errorf("B settings should be empty personal + shared title: %v", bset)
	}
	var sys map[string]any
	b.call("GET", "/api/v1/settings/system", nil, &sys)
	if _, ok := sys["db_path"]; ok {
		t.Errorf("member sees server paths: %v", sys)
	}
	if c, _ := sys["counts"].(map[string]any); c == nil || c["transactions"].(float64) != 0 {
		t.Errorf("member counts: %v", sys["counts"])
	}

	// every id of A's is a 404 for B — read, change, delete
	id := itoa
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/accounts/" + id(acct.ID)}, {"PUT", "/api/v1/accounts/" + id(acct.ID)}, {"DELETE", "/api/v1/accounts/" + id(acct.ID)},
		{"GET", "/api/v1/accounts/" + id(acct.ID) + "/history"}, {"PUT", "/api/v1/accounts/" + id(acct.ID) + "/members"},
		{"POST", "/api/v1/accounts/" + id(acct.ID) + "/reconcile"}, {"GET", "/api/v1/accounts/" + id(acct.ID) + "/payoff"},
		{"POST", "/api/v1/accounts/" + id(acct.ID) + "/interest"},
		{"GET", "/api/v1/transactions/" + id(tx.ID)}, {"PUT", "/api/v1/transactions/" + id(tx.ID)}, {"DELETE", "/api/v1/transactions/" + id(tx.ID)},
		{"POST", "/api/v1/transactions/" + id(tx.ID) + "/duplicate"}, {"POST", "/api/v1/transactions/" + id(tx.ID) + "/attachments"},
		{"GET", "/api/v1/attachments/" + id(att.ID)}, {"DELETE", "/api/v1/attachments/" + id(att.ID)},
		{"PUT", "/api/v1/categories/" + id(cat.ID)}, {"DELETE", "/api/v1/categories/" + id(cat.ID)},
		{"PUT", "/api/v1/tags/" + id(tag.ID)}, {"DELETE", "/api/v1/tags/" + id(tag.ID)},
		{"PUT", "/api/v1/templates/" + id(tpl.ID)}, {"DELETE", "/api/v1/templates/" + id(tpl.ID)}, {"POST", "/api/v1/templates/" + id(tpl.ID) + "/use"},
		{"GET", "/api/v1/subscriptions/" + id(sub.ID)}, {"PUT", "/api/v1/subscriptions/" + id(sub.ID)}, {"DELETE", "/api/v1/subscriptions/" + id(sub.ID)},
		{"POST", "/api/v1/subscriptions/" + id(sub.ID) + "/pay"}, {"POST", "/api/v1/subscriptions/" + id(sub.ID) + "/skip"},
		{"GET", "/api/v1/budgets/" + id(bud.ID)}, {"PUT", "/api/v1/budgets/" + id(bud.ID)}, {"DELETE", "/api/v1/budgets/" + id(bud.ID)},
		{"GET", "/api/v1/goals/" + id(goal.ID)}, {"PUT", "/api/v1/goals/" + id(goal.ID)}, {"DELETE", "/api/v1/goals/" + id(goal.ID)},
		{"POST", "/api/v1/goals/" + id(goal.ID) + "/entries"},
		{"PUT", "/api/v1/rules/" + id(rule.ID)}, {"DELETE", "/api/v1/rules/" + id(rule.ID)}, {"POST", "/api/v1/rules/" + id(rule.ID) + "/apply"},
		{"PUT", "/api/v1/webhooks/" + id(hook.ID)}, {"DELETE", "/api/v1/webhooks/" + id(hook.ID)}, {"POST", "/api/v1/webhooks/" + id(hook.ID) + "/test"},
		{"GET", "/api/v1/webhooks/" + id(hook.ID) + "/deliveries"},
		{"GET", "/firefly/api/v1/accounts/" + id(acct.ID)}, {"GET", "/firefly/api/v1/accounts/" + id(acct.ID) + "/transactions"},
		{"GET", "/firefly/api/v1/transactions/" + id(tx.ID)}, {"PUT", "/firefly/api/v1/transactions/" + id(tx.ID)}, {"DELETE", "/firefly/api/v1/transactions/" + id(tx.ID)},
		{"GET", "/firefly/api/v1/categories/" + id(cat.ID)}, {"GET", "/firefly/api/v1/tags/" + id(tag.ID)}, {"GET", "/firefly/api/v1/tags/tajno"},
	} {
		body := map[string]any{"name": "pwned", "amount": 1, "into": tag.ID, "transactions": []map[string]any{{"amount": "1"}}, "members": []map[string]any{{"user_id": bu.ID, "role": "editor"}}}
		if code := b.status(c.m, c.p, body); code != 404 {
			t.Errorf("B %s %s = %d, want 404", c.m, c.p, code)
		}
	}
	// bulk actions and merges skip what is not B's
	var bulk struct {
		Count int `json:"count"`
	}
	b.call("POST", "/api/v1/transactions/bulk", map[string]any{"ids": []int64{tx.ID}, "action": "delete"}, &bulk)
	if bulk.Count != 0 {
		t.Errorf("B bulk-deleted %d of A's transactions", bulk.Count)
	}
	b.call("PUT", "/api/v1/accounts/reorder", map[string]any{"ids": []int64{acct.ID}}, nil)

	// B cannot book onto A's account or use A's category, subscription or template
	var bacct store.Account
	b.call("POST", "/api/v1/accounts", map[string]any{"name": "Moj novčanik", "currency": "BAM", "kind": "cash"}, &bacct)
	if code := b.status("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 100}); code != 400 {
		t.Errorf("B booked on A's account: %d", code)
	}
	if code := b.status("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 100, "category_id": cat.ID}); code != 400 {
		t.Errorf("B used A's category: %d", code)
	}
	if code := b.status("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 100, "subscription_id": sub.ID}); code != 400 {
		t.Errorf("B linked to A's subscription: %d", code)
	}
	if code := b.status("POST", "/api/v1/transactions", map[string]any{"kind": "transfer", "account_id": bacct.ID, "to_account_id": acct.ID, "amount": 100}); code != 400 {
		t.Errorf("B transferred into A's account: %d", code)
	}
	if code := b.status("POST", "/api/v1/budgets", map[string]any{"name": "x", "amount": 100, "category_ids": []int64{cat.ID}}); code != 400 {
		t.Errorf("B budget on A's category: %d", code)
	}
	if code := b.status("POST", "/api/v1/rules", map[string]any{"match": "all", "conditions": []map[string]any{{"field": "text", "op": "contains", "value": "x"}}, "actions": map[string]any{"category_id": cat.ID}}); code != 400 {
		t.Errorf("B rule with A's category: %d", code)
	}
	if code := b.status("POST", "/api/v1/categories", map[string]any{"name": "child", "parent_id": cat.ID}); code != 400 {
		t.Errorf("B category under A's parent: %d", code)
	}
	// B's template use must not count A's template
	b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 100, "template_id": tpl.ID}, nil)
	if got, _ := s.store.GetTemplate(context.Background(), tpl.ID); got.UseCount != 0 {
		t.Errorf("B touched A's template: %d", got.UseCount)
	}
	// the Firefly API with B's token cannot reach A's account by id or name
	if code := b.status("POST", "/firefly/api/v1/transactions", map[string]any{"transactions": []map[string]any{{"type": "withdrawal", "amount": "1", "source_id": itoa(acct.ID), "description": "x"}}}); code != 422 {
		t.Errorf("B compat booking on A's account: %d", code)
	}
	if code := b.status("POST", "/firefly/api/v1/transactions", map[string]any{"transactions": []map[string]any{{"type": "withdrawal", "amount": "1", "source_name": "UniCredit", "description": "x"}}}); code != 422 {
		t.Errorf("B compat booking on A's account by name: %d", code)
	}
	// a member does not change the server's settings
	b.call("PUT", "/api/v1/settings", map[string]any{"title": "Hacked", "home_currency": "USD", "notify_url": "http://b/notify"}, nil)
	var aset map[string]any
	a.call("GET", "/api/v1/settings", nil, &aset)
	if aset["title"] != "Our Money" || aset["home_currency"] != "BAM" || aset["notify_url"] != "http://apprise/notify/a" {
		t.Errorf("member changed A's/server settings: %v", aset)
	}
	b.call("GET", "/api/v1/settings", nil, &bset)
	if bset["notify_url"] != "http://b/notify" {
		t.Errorf("member's own notify url not saved: %v", bset["notify_url"])
	}
	// names are unique per person, not per server
	if code := b.status("POST", "/api/v1/categories", map[string]any{"name": "restoran"}); code != 201 {
		t.Errorf("B could not make his own 'restoran': %d", code)
	}
	var btx store.Transaction
	b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 700, "description": "burek", "tags": []string{"tajno"}}, &btx)
	b.call("GET", "/api/v1/tags", nil, &tags)
	if len(tags) != 1 || tags[0].ID == tag.ID || tags[0].UserID != bu.ID {
		t.Errorf("B's tag should be his own: %+v (A's is %d)", tags, tag.ID)
	}
	// a full DB download is refused once someone else's money is in it
	if code := a.status("GET", "/api/v1/backup", nil); code != 403 {
		t.Errorf("admin backup with two people = %d", code)
	}

	// ---- A is untouched and does not see B's ----
	var alist struct {
		Items []store.Transaction `json:"items"`
	}
	a.call("GET", "/api/v1/transactions", nil, &alist)
	if len(alist.Items) != 1 || alist.Items[0].ID != tx.ID {
		t.Errorf("A sees %d transactions", len(alist.Items))
	}
	var acats []store.Category
	a.call("GET", "/api/v1/categories", nil, &acats)
	if len(acats) != 1 || acats[0].ID != cat.ID {
		t.Errorf("A categories: %+v", acats)
	}
	if code := a.status("GET", "/api/v1/transactions/"+itoa(btx.ID), nil); code != 404 {
		t.Errorf("admin reads B's transaction: %d", code)
	}
	if code := a.status("GET", "/api/v1/accounts/"+itoa(bacct.ID), nil); code != 404 {
		t.Errorf("admin reads B's account: %d", code)
	}

	// ---- explicit sharing: A shares the account with B as a viewer ----
	if code := a.status("PUT", "/api/v1/accounts/"+itoa(acct.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "viewer"}}}); code != 200 {
		t.Fatal("share")
	}
	if code := b.status("GET", "/api/v1/transactions/"+itoa(tx.ID), nil); code != 200 {
		t.Errorf("shared transaction not visible: %d", code)
	}
	if code := b.status("PUT", "/api/v1/transactions/"+itoa(tx.ID), map[string]any{"amount": 1}); code != 404 {
		t.Errorf("viewer could edit: %d", code)
	}
	if code := b.status("PUT", "/api/v1/accounts/"+itoa(acct.ID), map[string]any{"name": "x"}); code != 403 {
		t.Errorf("viewer editing the shared account = %d, want 403", code)
	}
	// sharing an account shares its transactions, not A's categories, budgets or settings
	var bcats []store.Category
	b.call("GET", "/api/v1/categories", nil, &bcats)
	for _, c := range bcats {
		if c.ID == cat.ID || c.UserID != bu.ID {
			t.Errorf("sharing leaked a category: %+v", c)
		}
	}
	empty("/api/v1/budgets")
	empty("/api/v1/settings")
}

// A webhook only hears about its owner's ledger (and accounts shared with
// them), and says whose it is.
func TestPerUserWebhooks(t *testing.T) {
	_, a, b, _ := newPerUserServer(t)
	got := make(chan map[string]any, 10)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		got <- v
	}))
	defer recv.Close()
	var hook store.Webhook
	a.call("POST", "/api/v1/webhooks", map[string]any{"name": "day", "url": recv.URL, "events": []string{"transaction.created"}}, &hook)
	var bacct store.Account
	b.call("POST", "/api/v1/accounts", map[string]any{"name": "B cash", "currency": "BAM"}, &bacct)
	b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 100, "description": "B private"}, nil)
	var aacct store.Account
	a.call("POST", "/api/v1/accounts", map[string]any{"name": "A main", "currency": "BAM"}, &aacct)
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": aacct.ID, "amount": 200, "description": "A own"}, nil)
	select {
	case v := <-got:
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), "B private") || !strings.Contains(string(raw), "A own") {
			t.Errorf("A's webhook got %s", raw)
		}
		if u, _ := v["user"].(map[string]any); u == nil || u["email"] != "admin@localhost" {
			t.Errorf("delivery does not say whose it is: %v", v["user"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
	}
	select {
	case v := <-got:
		raw, _ := json.Marshal(v)
		t.Errorf("unexpected second delivery: %s", raw)
	case <-time.After(300 * time.Millisecond):
	}
}

// Accounts created on first contact (Core tokens, PocketID / Core OIDC) are
// members; only the very first account on the box is an admin.
func TestAutoCreatedUsersAreMembers(t *testing.T) {
	s, _, _, _ := newPerUserServer(t)
	ctx := context.Background()
	for _, email := range []string{"second@example.com", "third@example.com"} {
		u, err := s.firstOrSystemUser(ctx, email, "x")
		if err != nil || u.Role != "member" {
			t.Errorf("%s: %v %+v", email, err, u)
		}
	}
	if n, _ := s.store.CountAdmins(ctx); n != 1 {
		t.Errorf("admins = %d", n)
	}
}

// A shared account books in its owner's categories, so a household account
// keeps one category list whoever enters the spending.
func TestSharedAccountUsesOwnerCategories(t *testing.T) {
	_, a, b, bu := newPerUserServer(t)
	var acct, bacct store.Account
	a.call("POST", "/api/v1/accounts", map[string]any{"name": "Household", "currency": "BAM", "kind": "cash"}, &acct)
	b.call("POST", "/api/v1/accounts", map[string]any{"name": "Moj novčanik", "currency": "BAM", "kind": "cash"}, &bacct)
	var food, bfood, bmisc store.Category
	a.call("POST", "/api/v1/categories", map[string]any{"name": "Groceries"}, &food)
	b.call("POST", "/api/v1/categories", map[string]any{"name": "groceries"}, &bfood)
	b.call("POST", "/api/v1/categories", map[string]any{"name": "Hobby"}, &bmisc)

	// nobody can read the categories of an account they cannot see
	if code := b.status("GET", "/api/v1/accounts/"+itoa(acct.ID)+"/categories", nil); code != 404 {
		t.Fatalf("unshared account categories = %d, want 404", code)
	}
	if code := a.status("PUT", "/api/v1/accounts/"+itoa(acct.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "editor"}}}); code != 200 {
		t.Fatal("share")
	}
	var cats []store.Category
	if code, _ := b.call("GET", "/api/v1/accounts/"+itoa(acct.ID)+"/categories", nil, &cats); code != 200 || len(cats) != 1 || cats[0].ID != food.ID {
		t.Fatalf("shared account categories %d %+v", code, cats)
	}

	// B books with A's category on the shared account
	var tx store.Transaction
	if code, raw := b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 900, "category_id": food.ID}, &tx); code != 201 || tx.CategoryID == nil || *tx.CategoryID != food.ID {
		t.Fatalf("owner category on shared account: %d %s", code, raw)
	}
	// B's own "groceries" maps onto A's "Groceries" (quick entry, suggestions)
	if code, raw := b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 500, "category_id": bfood.ID}, &tx); code != 201 || tx.CategoryID == nil || *tx.CategoryID != food.ID {
		t.Fatalf("same-name category not mapped: %d %s", code, raw)
	}
	// a category A has no counterpart for is refused
	if code := b.status("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 500, "category_id": bmisc.ID}); code != 400 {
		t.Fatalf("foreign category on shared account = %d, want 400", code)
	}
	// on B's own account A's category is still not usable
	if code := b.status("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 500, "category_id": food.ID}); code != 400 {
		t.Fatalf("owner's category on B's own account = %d, want 400", code)
	}
	// bulk recategorising follows the same rule
	var own store.Transaction
	b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bacct.ID, "amount": 100, "category_id": bmisc.ID}, &own)
	b.call("POST", "/api/v1/transactions/bulk", map[string]any{"ids": []int64{own.ID, tx.ID}, "action": "category", "category_id": food.ID}, nil)
	var got store.Transaction
	b.call("GET", "/api/v1/transactions/"+itoa(own.ID), nil, &got)
	if got.CategoryID == nil || *got.CategoryID != bmisc.ID {
		t.Fatalf("bulk put A's category on B's own transaction: %+v", got.CategoryID)
	}
	b.call("GET", "/api/v1/transactions/"+itoa(tx.ID), nil, &got)
	if got.CategoryID == nil || *got.CategoryID != food.ID {
		t.Fatalf("bulk on shared account: %+v", got.CategoryID)
	}
	// B's own category list is untouched by the sharing
	var bcats []store.Category
	b.call("GET", "/api/v1/categories", nil, &bcats)
	for _, c := range bcats {
		if c.UserID != bu.ID {
			t.Fatalf("A's category leaked into B's list: %+v", c)
		}
	}
}
