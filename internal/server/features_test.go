package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func TestSchedules(t *testing.T) {
	// 2nd Tuesday of the month, every month
	x := &store.Subscription{Freq: "monthly", Every: 1, Nth: 2, StartDate: "2026-10-13", NextDate: "2026-10-13"}
	if got := step(x, "2026-10-13"); got != "2026-11-10" {
		t.Errorf("2nd Tuesday after Oct 13 = %s", got)
	}
	x.Nth = 5 // last Tuesday
	if got := step(x, "2026-10-27"); got != "2026-11-24" {
		t.Errorf("last Tuesday = %s", got)
	}
	// 2026-10-17 is a Saturday
	w := &store.Subscription{Freq: "monthly", Every: 1, StartDate: "2026-10-17", NextDate: "2026-10-17", Weekend: "before"}
	if d := dueOf(w); d != "2026-10-16" {
		t.Errorf("before → %s", d)
	}
	w.Weekend = "after"
	if d := dueOf(w); d != "2026-10-19" {
		t.Errorf("after → %s", d)
	}
	w.Weekend = "skip"
	if d := dueOf(w); d != "2026-11-17" {
		t.Errorf("skip → %s", d)
	}
	// stop after 3 payments, 2 done: one more date
	m := &store.Subscription{Freq: "monthly", Every: 1, StartDate: "2026-11-02", NextDate: "2026-11-02", MaxCount: 3, DoneCount: 2}
	if occ := occurrences(m, "2027-12-31", 20); len(occ) != 1 {
		t.Errorf("limited occurrences = %v", occ)
	}
}

func TestApportion(t *testing.T) {
	got := apportion(1000, []int64{1, 1, 1})
	if got[0]+got[1]+got[2] != 1000 {
		t.Fatalf("apportion lost money: %v", got)
	}
}

func TestSplitsBalanceLiabilityWebhook(t *testing.T) {
	c := newTestServer(t)
	var acct store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "Main", "currency": "BAM", "opening_balance": 50000}, &acct)
	var food, cosm store.Category
	c.do("POST", "/api/v1/categories", map[string]any{"name": "hrana"}, &food)
	c.do("POST", "/api/v1/categories", map[string]any{"name": "kozmetika"}, &cosm)

	// a webhook receiver that checks the signature
	got := make(chan map[string]any, 10)
	var secret string
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(b)
		if r.Header.Get("X-Pocket-Signature") != "sha256="+hex.EncodeToString(m.Sum(nil)) {
			w.WriteHeader(401)
			return
		}
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		got <- v
	}))
	defer recv.Close()
	var hook store.Webhook
	if c.do("POST", "/api/v1/webhooks", map[string]any{"name": "test", "url": recv.URL, "events": []string{"transaction.created"}}, &hook) != 201 {
		t.Fatal("create webhook")
	}
	secret = hook.Secret

	// 30 KM at the supermarket: 22 food, 8 cosmetics
	var tx store.Transaction
	if c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 3000, "description": "Konzum",
		"splits": []map[string]any{{"category_id": food.ID, "amount": 2200}, {"category_id": cosm.ID, "amount": 800, "note": "šampon"}}}, &tx) != 201 {
		t.Fatal("split create")
	}
	if len(tx.Splits) != 2 || tx.Splits[0].AmountHome+tx.Splits[1].AmountHome != 3000 || tx.CategoryID == nil || *tx.CategoryID != food.ID {
		t.Errorf("splits: %+v cat %v", tx.Splits, tx.CategoryID)
	}
	// parts that do not add up are refused
	if c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct.ID, "amount": 3000,
		"splits": []map[string]any{{"category_id": food.ID, "amount": 2000}, {"category_id": cosm.ID, "amount": 800}}}, nil) != 400 {
		t.Error("mismatched parts accepted")
	}
	// the category report and the filtered totals count only the part
	var cats struct {
		Categories []store.GroupSum `json:"categories"`
	}
	c.do("GET", "/api/v1/reports/categories?from=2000-01-01&to=2999-01-01", nil, &cats)
	byName := map[string]int64{}
	for _, g := range cats.Categories {
		byName[g.Name] = g.Sum
	}
	if byName["hrana"] != 2200 || byName["kozmetika"] != 800 {
		t.Errorf("category sums %v", byName)
	}
	var list struct {
		Items  []store.Transaction `json:"items"`
		Totals store.TxTotals      `json:"totals"`
	}
	c.do("GET", "/api/v1/transactions?category="+itoa(cosm.ID), nil, &list)
	if list.Totals.Expense != 800 || len(list.Items) != 1 {
		t.Errorf("cosmetics filter: %d items, %d spent", len(list.Items), list.Totals.Expense)
	}

	// running balance on the account's list: 500 − 30 = 470
	c.do("GET", "/api/v1/transactions?account="+itoa(acct.ID), nil, &list)
	if len(list.Items) == 0 || list.Items[0].BalanceAfter == nil || *list.Items[0].BalanceAfter != 47000 {
		t.Errorf("balance after: %+v", list.Items)
	}

	// the webhook fired with the split transaction
	select {
	case v := <-got:
		data, _ := v["data"].(map[string]any)
		tr, _ := data["transaction"].(map[string]any)
		if v["event"] != "transaction.created" || tr["description"] != "Konzum" {
			t.Errorf("webhook payload %v", v)
		}
	case <-time.After(3 * time.Second):
		t.Error("webhook not delivered")
	}

	// a loan: 10 000 owed at 6 %, 500 a month
	var loan store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "Car loan", "kind": "loan", "currency": "BAM", "opening_balance": -1000000,
		"principal": 1200000, "interest_rate": 6, "payment": 50000, "payment_day": 5}, &loan)
	if loan.Everyday {
		t.Error("a loan should not be everyday money")
	}
	var p Payoff
	c.do("GET", "/api/v1/accounts/"+itoa(loan.ID)+"/payoff", nil, &p)
	if p.Outstanding != 1000000 || p.Repaid != 200000 || p.MonthInterest != 5000 || p.MonthsLeft < 21 || p.MonthsLeft > 22 || p.Never {
		t.Errorf("payoff: %+v", p)
	}
	var it store.Transaction
	if c.do("POST", "/api/v1/accounts/"+itoa(loan.ID)+"/interest", nil, &it) != 201 || it.Amount != 5000 || it.Kind != "expense" {
		t.Errorf("interest booking: %+v", it)
	}

	// Firefly clients see a split group
	var tok struct {
		Token string `json:"token"`
	}
	c.do("POST", "/api/v1/tokens", map[string]any{"name": "ff"}, &tok)
	req, _ := http.NewRequest("GET", c.ts.URL+"/firefly/api/v1/transactions/"+itoa(tx.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	res, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Count(string(b), `"transaction_journal_id"`) != 2 || !strings.Contains(string(b), `"category_name":"kozmetika"`) {
		t.Errorf("firefly split group: %s", b)
	}
	// …and can post one
	body := `{"transactions":[{"type":"withdrawal","date":"2026-10-01","amount":"10","description":"a","source_id":"` + itoa(acct.ID) + `","category_name":"hrana"},{"type":"withdrawal","date":"2026-10-01","amount":"5","description":"b","source_id":"` + itoa(acct.ID) + `","category_name":"kozmetika"}]}`
	req, _ = http.NewRequest("POST", c.ts.URL+"/firefly/api/v1/transactions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	res, _ = http.DefaultClient.Do(req)
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.Count(string(b), `"transaction_journal_id"`) != 2 || !strings.Contains(string(b), `"amount":"5.000000000000"`) {
		t.Errorf("firefly split post %d: %s", res.StatusCode, b)
	}
}

// A compat client sees each template's use count and counts a use by sending
// template_id with the transaction, so phone, web and Raycast sort alike.
func TestCompatTemplateUseCount(t *testing.T) {
	c := newTestServer(t)
	var acct store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &acct)
	var kafa, pekara store.Template
	c.do("POST", "/api/v1/templates", map[string]any{"name": "Kafa", "amount": 350, "account_id": acct.ID}, &kafa)
	c.do("POST", "/api/v1/templates", map[string]any{"name": "Pekara", "account_id": acct.ID}, &pekara)
	var tok struct {
		Token string `json:"token"`
	}
	c.do("POST", "/api/v1/tokens", map[string]any{"name": "phone"}, &tok)
	ff := func(method, path, body string) []byte {
		req, _ := http.NewRequest(method, c.ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s %s: %v %d", method, path, err, res.StatusCode)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return b
	}
	post := func(body string) { ff("POST", "/firefly/api/v1/transactions", body) }
	line := `{"type":"withdrawal","date":"2026-10-01","amount":"2","description":"x","source_id":"` + itoa(acct.ID) + `"}`
	post(`{"template_id":"` + itoa(pekara.ID) + `","transactions":[` + line + `]}`)
	post(`{"template_id":` + itoa(pekara.ID) + `,"transactions":[` + line + `,` + line + `]}`) // a split group is one use
	post(`{"transactions":[` + line + `]}`)                                                         // no template: no count
	var list struct {
		Data []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			UseCount int    `json:"use_count"`
			LastUsed any    `json:"last_used_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(ff("GET", "/firefly/api/transaction-templates", ""), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 2 || list.Data[0].Name != "Pekara" || list.Data[0].UseCount != 2 || list.Data[0].LastUsed == nil || list.Data[1].UseCount != 0 {
		t.Errorf("templates after uses: %+v", list.Data)
	}
}
