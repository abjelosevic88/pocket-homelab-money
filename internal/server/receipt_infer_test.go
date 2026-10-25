package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// fakeAI answers like OpenAI's chat completions: water and oil are
// groceries, the rest is left without a category.
func fakeAI(t *testing.T, groceries int64, calls *atomic.Int32) *httptest.Server {
	line := regexp.MustCompile(`(?m)^(\d+)\. (.+)$`)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		user := req.Messages[len(req.Messages)-1].Content
		if !strings.Contains(user, `"Fasunga": `) || !strings.Contains(user, "nabavka za kuci (") || !strings.Contains(user, "(bought at ") {
			t.Errorf("the prompt lacks the person's examples: %s", user)
		}
		var items []string
		for _, m := range line.FindAllStringSubmatch(user, -1) {
			cat := "null"
			if strings.Contains(strings.ToLower(m[2]), "voda") || strings.Contains(strings.ToLower(m[2]), "ulje") {
				cat = fmt.Sprint(groceries)
			}
			items = append(items, fmt.Sprintf(`{"n":%s,"category_id":%s}`, m[1], cat))
		}
		content, _ := json.Marshal(`{"items":[` + strings.Join(items, ",") + `]}`)
		fmt.Fprintf(w, `{"model":"fake","choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, content)
	}))
}

func TestReceiptInference(t *testing.T) {
	fake := &fakeSUF{receipts: map[uint32]string{
		11: sufShopJSON(11, 2, `\"КОНЗУМ\" трговина на велико и мало д.о.о. Сарајево`, "4200000000011", [3]any{"VODA PR 0,5L VIVIA", 2.0, 0.45}, [3]any{"ULJE SUNCOKRET 1L", 1.0, 7.1}, [3]any{"KESA", 1.0, 0.1}),
		12: sufShopJSON(12, 2, `APOTEKA ZEGIN d.o.o.`, "4200000000012", [3]any{"BRUFEN 400MG", 1.0, 6.5}),
		13: sufShopJSON(13, 2, `\"КОНЗУМ\" трговина на велико и мало д.о.о. Сарајево`, "4200000000011", [3]any{"KIFLA", 2.0, 0.6}),
		14: sufShopJSON(14, 2, `PEKARA ZLATNI KLAS`, "4200000000014", [3]any{"SALAMA PILECA", 1.0, 3.2}),
	}}
	receiptTransport = fake
	defer func() { receiptTransport = nil }()

	c := newTestServer(t)
	var bank store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &bank)
	var fasunga, pekara, apoteka store.Category
	c.do("POST", "/api/v1/categories", map[string]any{"name": "Fasunga"}, &fasunga)
	c.do("POST", "/api/v1/categories", map[string]any{"name": "pekara"}, &pekara)
	c.do("POST", "/api/v1/categories", map[string]any{"name": "apoteka"}, &apoteka)
	for i := 0; i < 3; i++ { // how this person describes groceries
		c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bank.ID, "amount": 2000 + i, "description": "nabavka za kuci", "category_id": fasunga.ID, "date": "2026-09-0" + fmt.Sprint(i+1)}, nil)
	}
	var calls atomic.Int32
	ai := fakeAI(t, fasunga.ID, &calls)
	defer ai.Close()
	if code := c.do("PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_key": "sk-test", "ai_url": ai.URL}, nil); code != 200 {
		t.Fatalf("settings %d", code)
	}
	var rs map[string]any
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"ai_items": true}, &rs)
	if rs["ai_items"] != true || rs["geocode"] != true || rs["ai_ready"] != true || rs["attach_manual"] != true {
		t.Fatalf("settings merge: %+v", rs)
	}

	// Konzum: the AI sorts water and oil into groceries, which is most of the money
	var res scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(11, 8.10)}, &res)
	// booked at once; the AI and the map pin come right after, and the booking follows
	c.settle()
	var tx *store.Transaction
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d", res.Transaction.ID), nil, &tx)
	var konzum store.Receipt
	c.do("GET", fmt.Sprintf("/api/v1/receipts/%d", res.Receipt.ID), nil, &konzum)
	reason := konzum.CategoryReason
	res = scanOut{}
	if tx == nil || tx.CategoryID == nil || *tx.CategoryID != fasunga.ID {
		t.Fatalf("konzum: %+v %s", tx, res.Message)
	}
	if tx.Payee != "Konzum" || tx.Description != "Nabavka za kuci" || tx.Lat == nil || *tx.Lat != 44.7722 {
		t.Fatalf("konzum fields: payee %q desc %q lat %v", tx.Payee, tx.Description, tx.Lat)
	}
	if !strings.Contains(reason, "sorted by AI") || calls.Load() != 1 {
		t.Fatalf("reason %q, %d AI calls", reason, calls.Load())
	}

	// a pharmacy by its name
	res = scanOut{}
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(12, 6.50)}, &res)
	if res.Transaction.CategoryID == nil || *res.Transaction.CategoryID != apoteka.ID || !strings.Contains(res.Receipt.CategoryReason, "apoteka") {
		t.Fatalf("pharmacy: %+v %q", res.Transaction.CategoryID, res.Receipt.CategoryReason)
	}

	// re-categorising and renaming a Konzum booking teaches the shop
	var upd store.Transaction
	c.do("PUT", fmt.Sprintf("/api/v1/transactions/%d", tx.ID), map[string]any{"category_id": pekara.ID, "description": "Kifle i voda"}, &upd)
	res = scanOut{}
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(13, 1.20)}, &res)
	if res.Transaction.CategoryID == nil || *res.Transaction.CategoryID != pekara.ID || res.Transaction.Description != "Kifle i voda" || !strings.Contains(res.Receipt.CategoryReason, "your choice for Konzum") {
		t.Fatalf("learned: %+v %q %q", res.Transaction.CategoryID, res.Transaction.Description, res.Receipt.CategoryReason)
	}
	c.settle()
	if fake.geocoded.Load() != 2 { // once per shop: Konzum, the pharmacy
		t.Errorf("geocoded %d times", fake.geocoded.Load())
	}

	// AI off: the next new product is not sent anywhere
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"ai_items": false}, nil)
	before := calls.Load()
	res = scanOut{}
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(14, 3.20)}, &res)
	c.settle()
	if calls.Load() != before {
		t.Fatal("AI asked while switched off")
	}
	if res.Transaction.CategoryID == nil || *res.Transaction.CategoryID != pekara.ID { // the bakery's name still works
		t.Fatalf("bakery: %+v", res.Transaction.CategoryID)
	}
	// an item categorised by hand changes what the receipt suggests
	var rec store.Receipt
	c.do("GET", fmt.Sprintf("/api/v1/receipts/%d", res.Receipt.ID), nil, &rec)
	pid := *rec.Items[0].ProductID
	c.do("PUT", fmt.Sprintf("/api/v1/products/%d", pid), map[string]any{"category_id": fasunga.ID}, nil)
	c.do("GET", fmt.Sprintf("/api/v1/receipts/%d", res.Receipt.ID), nil, &rec)
	if rec.Items[0].CategorySource != "you" || rec.Items[0].CategoryName != "Fasunga" {
		t.Fatalf("item category %+v", rec.Items[0])
	}
	// the products endpoint asks the AI about the rest when switched on
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"ai_items": true}, nil)
	var sorted struct{ Asked, Sorted int }
	c.do("POST", "/api/v1/products/sort", nil, &sorted)
	if sorted.Asked != 0 { // everything was asked about or categorised already
		t.Errorf("sort asked %d", sorted.Asked)
	}
	_ = io.Discard
}
