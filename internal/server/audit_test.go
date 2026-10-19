package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

type auditPage struct {
	Items      []store.AuditEntry `json:"items"`
	NextBefore *int64             `json:"next_before"`
	Actors     []store.AuditActor `json:"actors"`
}

func changesOf(t *testing.T, e store.AuditEntry) map[string]change {
	t.Helper()
	m := map[string]change{}
	if err := json.Unmarshal(e.Changes, &m); err != nil {
		t.Fatalf("changes %s: %v", e.Changes, err)
	}
	return m
}

func (p *peer) audit(path string) auditPage {
	p.t.Helper()
	var out auditPage
	if code, raw := p.call("GET", path, nil, &out); code != 200 {
		p.t.Fatalf("GET %s: %d %s", path, code, raw)
	}
	return out
}

// Who added, edited or deleted what on an account: written at every write
// path, readable by the account's owner only.
func TestAccountAuditLog(t *testing.T) {
	s, a, b, bu := newPerUserServer(t)
	ctx := context.Background()
	cu, err := s.firstOrSystemUser(ctx, "viewer@example.com", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	ctok, _, _ := s.store.CreateAPIToken(ctx, cu.ID, "tablet")
	c := &peer{t: t, base: a.base, token: ctok}

	// ---- A's household account, shared with B (editor) and C (viewer) ----
	var house store.Account
	a.call("POST", "/api/v1/accounts", map[string]any{"name": "Household", "currency": "BAM"}, &house)
	var groceries, restaurants store.Category
	a.call("POST", "/api/v1/categories", map[string]any{"name": "Groceries"}, &groceries)
	a.call("POST", "/api/v1/categories", map[string]any{"name": "Restaurants"}, &restaurants)
	if code, raw := a.call("PUT", "/api/v1/accounts/"+itoa(house.ID)+"/members", map[string]any{"members": []map[string]any{{"user_id": bu.ID, "role": "editor"}, {"user_id": cu.ID, "role": "viewer"}}}, nil); code != 200 {
		t.Fatalf("share: %d %s", code, raw)
	}

	// ---- B adds, edits (amount + category) and deletes ----
	var tx store.Transaction
	if code, raw := b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": house.ID, "amount": 2000, "description": "Konzum", "category_id": groceries.ID}, &tx); code != 201 {
		t.Fatalf("B create: %d %s", code, raw)
	}
	if code, raw := b.call("PUT", "/api/v1/transactions/"+itoa(tx.ID), map[string]any{"amount": 2500, "category_id": restaurants.ID}, nil); code != 200 {
		t.Fatalf("B update: %d %s", code, raw)
	}
	if code := b.status("DELETE", "/api/v1/transactions/"+itoa(tx.ID), nil); code != 204 {
		t.Fatalf("B delete: %d", code)
	}

	log := a.audit("/api/v1/accounts/" + itoa(house.ID) + "/audit?action=transaction")
	if len(log.Items) != 3 {
		t.Fatalf("want 3 transaction entries, got %d: %+v", len(log.Items), log.Items)
	}
	want := []string{"transaction.deleted", "transaction.updated", "transaction.created"} // newest first
	for i, e := range log.Items {
		if e.Action != want[i] {
			t.Errorf("entry %d: action %s, want %s", i, e.Action, want[i])
		}
		if e.ActorUserID == nil || *e.ActorUserID != bu.ID || e.ActorName != "Member" {
			t.Errorf("entry %d: actor %v %q, want B", i, e.ActorUserID, e.ActorName)
		}
		if e.Source != "api" || !strings.Contains(e.ActorLabel, "API token 'phone'") {
			t.Errorf("entry %d: source %q label %q", i, e.Source, e.ActorLabel)
		}
		if e.TransactionID == nil || *e.TransactionID != tx.ID {
			t.Errorf("entry %d: transaction id %v", i, e.TransactionID)
		}
	}
	upd := changesOf(t, log.Items[1])
	if upd["amount"] != (change{From: "20.00 BAM", To: "25.00 BAM"}) || upd["category"] != (change{From: "Groceries", To: "Restaurants"}) {
		t.Errorf("update diff: %+v", upd)
	}
	if _, ok := upd["description"]; ok || len(upd) != 2 {
		t.Errorf("update diff has unchanged fields: %+v", upd)
	}
	if !strings.HasPrefix(log.Items[1].Summary, "Member edited −25.00 BAM Restaurants 'Konzum'") {
		t.Errorf("update summary: %q", log.Items[1].Summary)
	}
	del := changesOf(t, log.Items[0])
	if del["amount"].From != "25.00 BAM" || del["category"].From != "Restaurants" || del["description"].From != "Konzum" || del["account"].From != "Household" || del["amount"].To != "" {
		t.Errorf("delete snapshot: %+v", del)
	}
	if log.Items[0].Summary != "Member deleted −25.00 BAM Restaurants 'Konzum'" {
		t.Errorf("delete summary: %q", log.Items[0].Summary)
	}
	if cr := changesOf(t, log.Items[2]); cr["amount"].To != "20.00 BAM" || cr["category"].To != "Groceries" {
		t.Errorf("create fields: %+v", cr)
	}
	// sharing itself is logged (by A)
	if sh := a.audit("/api/v1/accounts/" + itoa(house.ID) + "/audit?action=account.shared"); len(sh.Items) != 2 || sh.Items[0].Source != "web" {
		t.Errorf("share entries: %+v", sh.Items)
	}
	// filter by person
	if byB := a.audit("/api/v1/accounts/" + itoa(house.ID) + "/audit?actor=" + itoa(bu.ID)); len(byB.Items) != 3 {
		t.Errorf("actor filter: %d", len(byB.Items))
	}

	// ---- only the owner reads it ----
	if code := b.status("GET", "/api/v1/accounts/"+itoa(house.ID)+"/audit", nil); code != http.StatusForbidden {
		t.Errorf("editor reads the owner's log: %d", code)
	}
	if code := c.status("GET", "/api/v1/accounts/"+itoa(house.ID)+"/audit", nil); code != http.StatusForbidden {
		t.Errorf("viewer reads the owner's log: %d", code)
	}
	var wallet store.Account
	b.call("POST", "/api/v1/accounts", map[string]any{"name": "B wallet", "currency": "BAM"}, &wallet)
	b.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": wallet.ID, "amount": 500, "description": "Kafa"}, nil)
	if own := b.audit("/api/v1/accounts/" + itoa(wallet.ID) + "/audit"); len(own.Items) != 1 || own.Items[0].Action != "transaction.created" {
		t.Errorf("B's own log: %+v", own.Items)
	}
	// A is an admin, but admins see nothing of anyone else's
	if code := a.status("GET", "/api/v1/accounts/"+itoa(wallet.ID)+"/audit", nil); code != http.StatusNotFound {
		t.Errorf("admin reads someone else's log: %d", code)
	}

	// ---- a transfer is logged on both accounts ----
	var cash store.Account
	a.call("POST", "/api/v1/accounts", map[string]any{"name": "Cash", "currency": "BAM"}, &cash)
	var tr store.Transaction
	if code, raw := a.call("POST", "/api/v1/transactions", map[string]any{"kind": "transfer", "account_id": house.ID, "to_account_id": cash.ID, "amount": 10000}, &tr); code != 201 {
		t.Fatalf("transfer: %d %s", code, raw)
	}
	for _, id := range []int64{house.ID, cash.ID} {
		l := a.audit("/api/v1/accounts/" + itoa(id) + "/audit?limit=1")
		if len(l.Items) != 1 || l.Items[0].TransactionID == nil || *l.Items[0].TransactionID != tr.ID || l.Items[0].Source != "web" {
			t.Errorf("transfer entry on account %d: %+v", id, l.Items)
		}
		if l.NextBefore == nil {
			t.Errorf("limit=1 should offer a next cursor")
		}
	}
	if l := a.audit("/api/v1/accounts/" + itoa(cash.ID) + "/audit"); !strings.Contains(l.Items[0].Summary, "Household → Cash") {
		t.Errorf("transfer summary: %q", l.Items[0].Summary)
	}

	// ---- the Firefly-compatible API (the phone app) ----
	var ffOut struct {
		Data ffItem `json:"data"`
	}
	if code, raw := b.call("POST", "/firefly/api/v1/transactions", map[string]any{"transactions": []map[string]any{{"type": "withdrawal", "date": store.Today(), "amount": "7.50", "description": "Pekara", "source_id": itoa(house.ID)}}}, &ffOut); code != 200 {
		t.Fatalf("ff create: %d %s", code, raw)
	}
	l := a.audit("/api/v1/accounts/" + itoa(house.ID) + "/audit?limit=1")
	if len(l.Items) != 1 || l.Items[0].Source != "firefly_api" || l.Items[0].Action != "transaction.created" || *l.Items[0].ActorUserID != bu.ID {
		t.Errorf("ff create entry: %+v", l.Items)
	}
	if code := b.status("DELETE", "/firefly/api/v1/transactions/"+ffOut.Data.ID, nil); code != 204 {
		t.Fatalf("ff delete: %d", code)
	}
	l = a.audit("/api/v1/accounts/" + itoa(house.ID) + "/audit?limit=1")
	if l.Items[0].Source != "firefly_api" || l.Items[0].Action != "transaction.deleted" || !strings.Contains(l.Items[0].Summary, "Pekara") {
		t.Errorf("ff delete entry: %+v", l.Items[0])
	}

	// ---- bulk recategorise and delete: one entry per transaction ----
	var t1, t2 store.Transaction
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": cash.ID, "amount": 100, "description": "one"}, &t1)
	a.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": cash.ID, "amount": 200, "description": "two"}, &t2)
	if code, raw := a.call("POST", "/api/v1/transactions/bulk", map[string]any{"ids": []int64{t1.ID, t2.ID}, "action": "category", "category_id": groceries.ID}, nil); code != 200 {
		t.Fatalf("bulk category: %d %s", code, raw)
	}
	l = a.audit("/api/v1/accounts/" + itoa(cash.ID) + "/audit?action=transaction.updated")
	if len(l.Items) != 2 || l.Items[0].Source != "bulk" || changesOf(t, l.Items[0])["category"].To != "Groceries" {
		t.Errorf("bulk category entries: %+v", l.Items)
	}
	a.call("POST", "/api/v1/transactions/bulk", map[string]any{"ids": []int64{t1.ID, t2.ID}, "action": "delete"}, nil)
	l = a.audit("/api/v1/accounts/" + itoa(cash.ID) + "/audit?action=transaction.deleted")
	if len(l.Items) != 2 || l.Items[0].Source != "bulk" || l.Items[1].Source != "bulk" {
		t.Errorf("bulk delete entries: %+v", l.Items)
	}

	// ---- an import run writes one summary ----
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "bank.csv")
	_, _ = io.WriteString(fw, "date,amount,text\n2026-01-02,-10.00,Bakery\n2026-01-03,-20.00,Fuel\n2026-01-04,50.00,Refund\n")
	_ = mw.WriteField("mapping", `{"date":0,"amount":1,"debit":-1,"credit":-1,"description":2,"payee":-1,"category":-1,"notes":-1,"currency":-1,"date_format":"ymd","decimal":"dot","has_header":true,"delimiter":"comma"}`)
	_ = mw.WriteField("account_id", itoa(cash.ID))
	mw.Close()
	req, _ := http.NewRequest("POST", a.base+"/api/v1/import/csv", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(raw), `"created":3`) {
		t.Fatalf("csv import: %d %s", res.StatusCode, raw)
	}
	l = a.audit("/api/v1/accounts/" + itoa(cash.ID) + "/audit?limit=5")
	if l.Items[0].Action != "import.summary" || l.Items[0].Source != "import" || !strings.Contains(l.Items[0].Summary, "3 added") {
		t.Errorf("import summary: %+v", l.Items[0])
	}
	if l.Items[1].Action == "transaction.created" && strings.Contains(l.Items[1].Summary, "Bakery") {
		t.Errorf("import wrote per-row entries")
	}

	// ---- /audit merges the caller's own accounts only ----
	all := a.audit("/api/v1/audit?limit=200")
	for _, e := range all.Items {
		if e.AccountID == wallet.ID {
			t.Errorf("A's merged feed shows B's account: %+v", e)
		}
		if e.AccountName == "" {
			t.Errorf("merged entry without account name: %+v", e)
		}
	}
	seen := map[int64]bool{}
	for _, e := range all.Items {
		seen[e.AccountID] = true
	}
	if !seen[house.ID] || !seen[cash.ID] {
		t.Errorf("merged feed misses an owned account: %v", seen)
	}
	mine := b.audit("/api/v1/audit")
	for _, e := range mine.Items {
		if e.AccountID != wallet.ID {
			t.Errorf("B's merged feed shows someone else's account: %+v", e)
		}
	}
	if len(mine.Items) != 1 {
		t.Errorf("B's merged feed: %d entries", len(mine.Items))
	}
	if code := c.status("GET", "/api/v1/audit", nil); code != 200 {
		t.Errorf("viewer's own (empty) feed: %d", code)
	}
	// filters on the merged feed: everything B did on A's accounts
	byB := a.audit("/api/v1/audit?actor=" + itoa(bu.ID))
	if len(byB.Items) != 5 { // create, update, delete + phone-app create and delete
		t.Errorf("A's feed filtered to B: %d entries", len(byB.Items))
	}
	foundB := false
	for _, ac := range all.Actors {
		if ac.UserID != nil && *ac.UserID == bu.ID && ac.Name == "Member" {
			foundB = true
		}
	}
	if !foundB {
		t.Errorf("actors list: %+v", all.Actors)
	}

	// ---- deleting an account takes its log with it ----
	if code := a.status("DELETE", "/api/v1/accounts/"+itoa(cash.ID), nil); code != 204 {
		t.Fatalf("delete account: %d", code)
	}
	var n int
	_ = s.store.DB.QueryRow(`SELECT COUNT(1) FROM account_audit WHERE account_id = ?`, cash.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d audit entries outlived their account", n)
	}
	// the household side of the deleted transfer says so
	l = a.audit("/api/v1/accounts/" + itoa(house.ID) + "/audit?limit=1")
	if l.Items[0].Action != "transaction.deleted" || !strings.Contains(l.Items[0].Summary, "account Cash deleted") {
		t.Errorf("transfer side of the deleted account: %+v", l.Items[0])
	}
}
