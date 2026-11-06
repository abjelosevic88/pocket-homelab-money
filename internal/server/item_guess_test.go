package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func TestReceiptLines(t *testing.T) {
	items := receiptLines([]guessLine{{Name: "pica", Quantity: 1, UnitPrice: 6}, {Name: " Krofna ", Quantity: 2, UnitPrice: 1.5}}, 800)
	if len(items) != 2 || items[0].Name != "Pica" || items[1].Name != "Krofna" || items[1].Quantity != 2 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Total+items[1].Total != 800 || items[0].Total != 533 || items[1].UnitPrice != 134 {
		t.Fatalf("totals %+v", items)
	}
	// no prices: by quantity; odd remainder on the biggest line
	items = receiptLines([]guessLine{{Name: "kafa", Quantity: 2}, {Name: "sok", Quantity: 1}}, 1000)
	if items[0].Total != 667 || items[1].Total != 333 {
		t.Fatalf("by quantity %+v", items)
	}
	if receiptLines(nil, 500) != nil || receiptLines([]guessLine{{Name: "  "}}, 500) != nil {
		t.Fatal("nothing named: no items")
	}
	// fuel by the litre keeps its unit; an unknown unit is a piece
	items = receiptLines([]guessLine{{Name: "Gorivo", Quantity: 37.67, Unit: "Lit", UnitPrice: 3.85}, {Name: "Kafa", Quantity: 1, Unit: "kom", UnitPrice: 2}}, 14703)
	if items[0].Unit != "l" || items[0].Quantity != 37.67 || items[0].Total != 14503 || items[1].Unit != "" {
		t.Fatalf("fuel %+v", items)
	}
}

func TestGuessSources(t *testing.T) {
	for src, want := range map[string]bool{"": true, "manual": true, "web": true, "quick": true, "template": true, "api": true,
		"firefly_api": true, "garage": true, "travel": true, "subscription": true,
		"firefly": false, "receipt": false, "csv": false, "adjustment": false, "interest": false} {
		if got := onEntry(src); got != want {
			t.Errorf("on entry %q: %v, want %v", src, got, want)
		}
	}
	if !store.GuessSource("firefly") || !store.GuessSource("garage") || store.GuessSource("receipt") {
		t.Error("the retry covers Firefly rows and other apps' bookings, never receipts")
	}
}

func TestItemGuessOnEntry(t *testing.T) {
	// today: the hourly retry only looks at the last guessWindow days
	day := time.Now().Format("2006-01-02")
	c := newTestServer(t)
	var bank store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &bank)
	add := func(desc string, amount int64) store.Transaction {
		var tx store.Transaction
		c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bank.ID, "amount": amount, "description": desc, "date": day, "time": "16:45"}, &tx)
		return tx
	}
	// no AI: nothing happens, nothing is remembered
	early := add("krofna", 200)
	c.settle()

	var calls atomic.Int32
	var prompt atomic.Value
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		user := req.Messages[len(req.Messages)-1].Content
		prompt.Store(user)
		var parts []string
		for _, line := range strings.Split(user, "\n") {
			var id int64
			if _, err := fmt.Sscanf(line, "#%d", &id); err != nil {
				continue
			}
			switch {
			case strings.Contains(line, `"pica i krofna"`):
				parts = append(parts, fmt.Sprintf(`{"id":%d,"items":[{"name":"Pica margherita","quantity":1,"unit_price":6},{"name":"Krofna","quantity":1,"unit_price":2}]}`, id))
			case strings.Contains(line, `"3 krofne"`):
				parts = append(parts, fmt.Sprintf(`{"id":"%d","items":[{"name":"Krofna","quantity":3,"unit_price":2}]}`, id))
			case strings.Contains(line, `"krofna"`):
				parts = append(parts, fmt.Sprintf(`{"id":%d,"items":[{"name":"Krofna","quantity":1,"unit_price":2}]}`, id))
			default:
				parts = append(parts, fmt.Sprintf(`{"id":%d,"items":[],"note":"a place"}`, id))
			}
		}
		content, _ := json.Marshal(`{"expenses":[` + strings.Join(parts, ",") + `]}`)
		fmt.Fprintf(w, `{"model":"fake","choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, content)
	}))
	defer ai.Close()
	c.do("PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_key": "sk-test", "ai_url": ai.URL}, nil)
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"ai_review": false}, nil)

	pica := add("pica i krofna", 800)
	pekara := add("Pekara", 560)
	c.settle()
	if n := calls.Load(); n != 2 {
		t.Fatalf("one AI call per typed expense, got %d", n)
	}
	var got store.Transaction
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d", pica.ID), nil, &got)
	if got.ReceiptID == nil || !got.ReceiptAI {
		t.Fatalf("pica: %+v", got)
	}
	var rec store.Receipt
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d/receipt", pica.ID), nil, &rec)
	if rec.Provider != store.AIProvider || rec.Total != 800 || len(rec.Items) != 2 || rec.Items[0].Total != 600 || rec.Items[1].Total != 200 || rec.Guess != nil {
		t.Fatalf("receipt %+v", rec)
	}
	// the AI named nothing: one line named for the description, so it still lands on a product
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d/receipt", pekara.ID), nil, &rec)
	if len(rec.Items) != 1 || rec.Items[0].Name != "Pekara" || rec.Items[0].Total != 560 || rec.Items[0].ProductID == nil {
		t.Fatalf("every expense gets a line: %+v", rec)
	}
	// guessed items are not scanned receipts
	var list struct {
		Total int `json:"total"`
	}
	c.do("GET", "/api/v1/receipts", nil, &list)
	if list.Total != 0 {
		t.Fatalf("receipt list shows guesses: %d", list.Total)
	}
	var page struct {
		Items []store.Transaction `json:"items"`
	}
	c.do("GET", "/api/v1/transactions?q=margherita", nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != pica.ID {
		t.Fatal("item names are searchable")
	}
	if it := page.Items[0]; it.ItemCount != 2 || len(it.Items) != 2 || it.Items[0].Name != "Pica margherita" || it.Items[0].Total != 600 {
		t.Fatalf("list rows carry the items, biggest first: %+v", it.Items)
	}

	// saving again without changes asks nothing; a new description asks again
	c.do("PUT", fmt.Sprintf("/api/v1/transactions/%d", pica.ID), map[string]any{"notes": "fino"}, nil)
	c.settle()
	if n := calls.Load(); n != 2 {
		t.Fatalf("unchanged save asked again: %d calls", n)
	}
	c.do("PUT", fmt.Sprintf("/api/v1/transactions/%d", pica.ID), map[string]any{"description": "3 krofne", "amount": 600}, nil)
	c.settle()
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d/receipt", pica.ID), nil, &rec)
	if len(rec.Items) != 1 || rec.Items[0].Quantity != 3 || rec.Total != 600 || rec.Items[0].UnitPrice != 200 {
		t.Fatalf("after edit %+v", rec)
	}

	owner, err := c.srv.store.GetUser(t.Context(), got.UserID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), userKey, owner)

	// the hourly retry picks up what was typed while there was no AI
	c.srv.retryItemGuesses(ctx)
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d", early.ID), nil, &got)
	if !got.ReceiptAI {
		t.Fatal("retry did not guess the early entry")
	}
	var status struct {
		Running *guessRun `json:"running"`
		Last    *guessRun `json:"last"`
	}
	c.do("GET", "/api/v1/receipts/guess-items", nil, &status)
	if status.Running != nil || status.Last == nil || status.Last.Trigger != "retry" || status.Last.Done != status.Last.Checked {
		t.Fatalf("status after the retry %+v %+v", status.Running, status.Last)
	}
	if p, _ := prompt.Load().(string); !strings.Contains(p, "Krofna · 2.00 BAM") {
		t.Errorf("known products missing from prompt:\n%s", p)
	}

	// a scanned receipt for the same purchase replaces the guess
	if err := c.srv.store.DeleteGuessedItems(ctx, early.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := c.srv.store.UnreceiptedMatch(ctx, []int64{bank.ID}, day, 200, "BAM", "expense"); err != nil || id != early.ID {
		t.Fatalf("match %d %v", id, err)
	}

	// switched off: nothing on entry
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"guess_items": false}, nil)
	before := calls.Load()
	add("sendvic", 1000)
	c.settle()
	if calls.Load() != before {
		t.Fatal("asked while switched off")
	}
}

func TestLiveGuess(t *testing.T) {
	const uid = 987654
	waiting := &guessRun{Trigger: "manual", Checked: 99}
	setLiveGuess(uid, waiting)
	run := &guessRun{Trigger: "manual", Checked: 99, Done: 20, Next: 40}
	setLiveGuess(uid, run)
	run.Done = 40 // the snapshot is a copy: no change until published
	if got := runningGuess(uid); got == nil || !got.Running || got.Done != 20 || got.Next != 40 {
		t.Fatalf("live %+v", got)
	}
	endLiveGuess(uid, waiting) // an earlier owner ending does not hide the run
	if runningGuess(uid) == nil {
		t.Fatal("hidden by another run")
	}
	endLiveGuess(uid, run)
	if got := runningGuess(uid); got != nil {
		t.Fatalf("still live %+v", got)
	}
}
