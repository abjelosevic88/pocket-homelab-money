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
	"sync"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// moveCore introspects Pocket Move's link tokens for Alice and Bob.
func moveCore(mu *sync.Mutex, manifest map[string]any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			mu.Lock()
			for k, v := range m {
				manifest[k] = v
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"app":{},"token":"pca_money"}`))
		case "/api/v1/auth/introspect":
			var in struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			link := map[string][2]string{
				"pcx_out_alice": {"move:export", "alice@example.com"},
				"pcx_in_alice":  {"move:import", "alice@example.com"},
				"pcx_out_bob":   {"move:export", "bob@example.com"},
				"pcx_in_bob":    {"move:import", "bob@example.com"},
			}[in.Token]
			if link[0] == "" {
				_, _ = w.Write([]byte(`{"active":false}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "scope": link[0], "audience": "money",
				"user": map[string]any{"id": map[bool]int{true: 1, false: 2}[strings.HasPrefix(link[1], "alice")], "email": link[1], "name": strings.Split(link[1], "@")[0]},
				"app":  map[string]any{"id": 9, "slug": "move", "name": "Pocket Move"}})
		default:
			http.NotFound(w, r)
		}
	})
}

// upload posts a file as multipart field "file" plus form fields.
func (p *peer) upload(path, name string, body []byte, fields map[string]string, out any) (int, []byte) {
	p.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", p.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+p.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			p.t.Fatalf("%s: %v: %s", path, err, raw)
		}
	}
	return res.StatusCode, raw
}

func (p *peer) get(path string) (int, []byte) {
	p.t.Helper()
	return p.call("GET", path, nil, nil)
}

func newMoveServer(t *testing.T) (*Server, string, map[string]any) {
	t.Helper()
	manifest := map[string]any{}
	var mu sync.Mutex
	coreSrv := httptest.NewServer(moveCore(&mu, manifest))
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
	if err := s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	t.Cleanup(s.receiptWork.Wait)
	mu.Lock()
	defer mu.Unlock()
	copied := map[string]any{}
	for k, v := range manifest {
		copied[k] = v
	}
	return s, ts.URL, copied
}

const statement = `Datum;Opis;Iznos;Valuta
03.11.2026;Konzum Sarajevo;-45,20;BAM
04.11.2026;Plata;2.500,00;BAM
05.11.2026;Kafa Ministry;-4,50;BAM
`

func TestMoveLinkScopesAndBankCSV(t *testing.T) {
	_, base, manifest := newMoveServer(t)
	offers, _ := json.Marshal(manifest["offers"])
	if !strings.Contains(string(offers), `"move:export"`) || !strings.Contains(string(offers), `"move:import"`) {
		t.Fatalf("offers %s", offers)
	}
	if p, _ := json.Marshal(manifest["portable"]); !strings.Contains(string(p), "/api/v1/import/bank.csv") {
		t.Fatalf("portable %s", p)
	}
	out := &peer{t: t, base: base, token: "pcx_out_alice"}
	in := &peer{t: t, base: base, token: "pcx_in_alice"}
	bobIn := &peer{t: t, base: base, token: "pcx_in_bob"}
	bobOut := &peer{t: t, base: base, token: "pcx_out_bob"}

	var desc core.Portable
	if code, raw := out.call("GET", core.PortablePath, nil, &desc); code != 200 || len(desc.Exports) != 2 || len(desc.Imports) != 2 {
		t.Fatalf("portable %d %s", code, raw)
	}
	if code, _ := in.get(core.PortablePath); code != 200 {
		t.Fatalf("import link reads the description: %d", code)
	}

	// import: a statement in, twice
	var res map[string]any
	if code, raw := in.upload("/api/v1/import/bank.csv", "izvod.csv", []byte(statement), map[string]string{"account": "Raiffeisen"}, &res); code != 200 || res["imported"] != float64(3) || res["account_created"] != true {
		t.Fatalf("bank csv %d %s", code, raw)
	}
	if code, raw := in.upload("/api/v1/import/bank.csv", "izvod.csv", []byte(statement), map[string]string{"account": "Raiffeisen"}, &res); code != 200 || res["imported"] != float64(0) || res["skipped"] != float64(3) {
		t.Fatalf("second run %d %s", code, raw)
	}
	// no account named: "Moved in"
	if code, raw := in.upload("/api/v1/import/bank.csv", "x.csv", []byte("Date,Description,Amount\n2026-11-01,Bakery,-3.20\n"), nil, &res); code != 200 || res["account"] != bankCSVAccount {
		t.Fatalf("default account %d %s", code, raw)
	}
	// unsure guesses are refused
	for _, bad := range []string{
		"a,b,c\n1,2,3\n",
		"Date,Description,Amount\n03/04/2026,Shop,-3\n05/06/2026,Shop,-4\n",
		"Date,Description,Amount\nyesterday,Shop,-3\nlast week,Shop,-4\n",
	} {
		code, raw := in.upload("/api/v1/import/bank.csv", "x.csv", []byte(bad), nil, nil)
		if code != 422 || !strings.Contains(string(raw), `"needs_mapping":true`) {
			t.Fatalf("unsure guess %q: %d %s", bad, code, raw)
		}
	}

	// export link: GET the exports, nothing else
	for _, p := range []string{"/api/v1/export/money.json", "/api/v1/export/transactions.csv"} {
		if code, raw := out.get(p); code != 200 {
			t.Fatalf("export %s %d %s", p, code, raw)
		}
	}
	csvCode, csvRaw := out.get("/api/v1/export/transactions.csv")
	if csvCode != 200 || !strings.Contains(string(csvRaw), "Konzum Sarajevo") {
		t.Fatalf("csv %s", csvRaw)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/settings"}, {"GET", "/api/v1/transactions"}, {"GET", "/api/v1/accounts"}, {"DELETE", "/api/v1/accounts/1"},
	} {
		if code := out.status(c.method, c.path, nil); code != 403 {
			t.Fatalf("export link %s %s = %d", c.method, c.path, code)
		}
	}
	if code, _ := out.upload("/api/v1/import/bank.csv", "x.csv", []byte(statement), nil, nil); code != 403 {
		t.Fatalf("export link may not import: %d", code)
	}
	// import link: POST the imports, nothing else
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/export/money.json"}, {"GET", "/api/v1/settings"}, {"PUT", "/api/v1/settings"}, {"POST", "/api/v1/transactions"}, {"POST", "/api/v1/import/csv"},
	} {
		if code := in.status(c.method, c.path, nil); code != 403 {
			t.Fatalf("import link %s %s = %d", c.method, c.path, code)
		}
	}

	// Bob sees none of it, and his import lands with him
	var bobDump moneyExport
	if code, raw := bobOut.call("GET", "/api/v1/export/money.json", nil, &bobDump); code != 200 || len(bobDump.Transactions) != 0 || len(bobDump.Accounts) != 0 {
		t.Fatalf("bob's export %d %s", code, raw)
	}
	if code, raw := bobIn.upload("/api/v1/import/bank.csv", "izvod.csv", []byte(statement), map[string]string{"account": "Raiffeisen"}, &res); code != 200 || res["imported"] != float64(3) || res["account_created"] != true {
		t.Fatalf("bob's statement %d %s", code, raw)
	}
	var aliceDump moneyExport
	out.call("GET", "/api/v1/export/money.json", nil, &aliceDump)
	if len(aliceDump.Transactions) != 4 || len(aliceDump.Accounts) != 2 {
		t.Fatalf("alice has %d transactions, %d accounts", len(aliceDump.Transactions), len(aliceDump.Accounts))
	}
	for _, tx := range aliceDump.Transactions {
		if tx.Source != "csv" {
			t.Fatalf("a moved-in statement keeps the csv source (no AI items): %+v", tx)
		}
	}
}

func TestMoveMoneyJSONRoundTrip(t *testing.T) {
	s, base, _ := newMoveServer(t)
	ctx := context.Background()
	alice, err := s.firstOrSystemUser(ctx, "alice@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _ := s.store.CreateAPIToken(ctx, alice.ID, "test")
	me := &peer{t: t, base: base, token: tok}
	var bam, eur store.Account
	me.call("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM", "opening_balance": 100000}, &bam)
	me.call("POST", "/api/v1/accounts", map[string]any{"name": "Devizni", "currency": "EUR", "kind": "savings"}, &eur)
	var food, sub store.Category
	me.call("POST", "/api/v1/categories", map[string]any{"name": "Hrana"}, &food)
	me.call("POST", "/api/v1/categories", map[string]any{"name": "Pekara", "parent_id": food.ID}, &sub)
	for _, tx := range []map[string]any{
		{"kind": "expense", "account_id": bam.ID, "amount": 900, "description": "Kafa", "category_id": sub.ID, "date": "2026-11-01", "tags": []string{"posao"}},
		{"kind": "expense", "account_id": bam.ID, "amount": 900, "description": "Kafa", "category_id": sub.ID, "date": "2026-11-01"},
		{"kind": "income", "account_id": bam.ID, "amount": 250000, "description": "Plata", "date": "2026-11-02"},
		{"kind": "transfer", "account_id": bam.ID, "to_account_id": eur.ID, "amount": 19558, "description": "Štednja", "date": "2026-11-03"},
		{"kind": "expense", "account_id": bam.ID, "amount": 5000, "description": "Ručak", "date": "2026-11-04", "splits": []map[string]any{{"category_id": food.ID, "amount": 3000}, {"category_id": sub.ID, "amount": 2000}}},
	} {
		if code, raw := me.call("POST", "/api/v1/transactions", tx, nil); code != 201 {
			t.Fatalf("tx %d %s", code, raw)
		}
	}
	if code, raw := me.call("POST", "/api/v1/subscriptions", map[string]any{"name": "Netflix", "amount": 2500, "currency": "BAM", "account_id": bam.ID, "start_date": "2026-11-10"}, nil); code != 201 {
		t.Fatalf("sub %d %s", code, raw)
	}
	if code, raw := me.call("POST", "/api/v1/budgets", map[string]any{"name": "Hrana", "amount": 50000, "period": "monthly", "category_ids": []int64{food.ID}}, nil); code != 201 {
		t.Fatalf("budget %d %s", code, raw)
	}
	_, dump := me.get("/api/v1/export/money.json")

	// Bob brings Alice's file in, twice
	bobIn := &peer{t: t, base: base, token: "pcx_in_bob"}
	var res map[string]any
	if code, raw := bobIn.upload("/api/v1/import/money.json", "money.json", dump, nil, &res); code != 200 || res["imported"] != float64(5) ||
		res["accounts_created"] != float64(2) || res["categories_created"] != float64(2) || res["subscriptions_created"] != float64(1) || res["budgets_created"] != float64(1) {
		t.Fatalf("restore %d %s", code, raw)
	}
	if code, raw := bobIn.upload("/api/v1/import/money.json", "money.json", dump, nil, &res); code != 200 || res["imported"] != float64(0) || res["skipped"] != float64(5) ||
		res["accounts_created"] != float64(0) || res["subscriptions_created"] != float64(0) {
		t.Fatalf("second restore %d %s", code, raw)
	}
	var bob moneyExport
	(&peer{t: t, base: base, token: "pcx_out_bob"}).call("GET", "/api/v1/export/money.json", nil, &bob)
	if len(bob.Transactions) != 5 || len(bob.Accounts) != 2 || len(bob.Categories) != 2 || len(bob.Subscriptions) != 1 || len(bob.Budgets) != 1 {
		t.Fatalf("bob has %d tx, %d accounts, %d categories, %d subs, %d budgets", len(bob.Transactions), len(bob.Accounts), len(bob.Categories), len(bob.Subscriptions), len(bob.Budgets))
	}
	var transfer, split *store.Transaction
	for i := range bob.Transactions {
		t := &bob.Transactions[i]
		switch t.Description {
		case "Štednja":
			transfer = t
		case "Ručak":
			split = t
		}
	}
	if transfer == nil || transfer.ToAccountName != "Devizni" || transfer.ToAmount != 10000 || transfer.Source != "import" {
		t.Fatalf("transfer %+v", transfer)
	}
	if split == nil || len(split.Splits) != 2 || split.Splits[0].CategoryName == "" {
		t.Fatalf("split %+v", split)
	}
	if bob.Budgets[0].CategoryIDs[0] == food.ID {
		t.Fatal("budget categories are Bob's own")
	}
	// Alice is untouched, and her own file back into her own box adds nothing
	aliceIn := &peer{t: t, base: base, token: "pcx_in_alice"}
	if code, raw := aliceIn.upload("/api/v1/import/money.json", "money.json", dump, nil, &res); code != 200 || res["imported"] != float64(0) || res["accounts_created"] != float64(0) {
		t.Fatalf("same box %d %s", code, raw)
	}
	var again moneyExport
	(&peer{t: t, base: base, token: "pcx_out_alice"}).call("GET", "/api/v1/export/money.json", nil, &again)
	if len(again.Transactions) != 5 || len(again.Accounts) != 2 {
		t.Fatalf("alice now has %d tx", len(again.Transactions))
	}
	if code, _ := bobIn.upload("/api/v1/import/money.json", "x.json", []byte(`{"app":"pocket-notes"}`), nil, nil); code != 400 {
		t.Fatalf("a foreign file: %d", code)
	}
}
