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

type dayReviewOut struct {
	Settings store.DayReviewSettings `json:"settings"`
	AIReady  bool                    `json:"ai_ready"`
	Open     int                     `json:"open"`
	Run      int64                   `json:"run"`
	Reviews  []struct {
		store.DayReview
		Findings []struct {
			store.DayReviewFinding
			Transaction  *findingTx `json:"transaction"`
			Duplicate    *findingTx `json:"duplicate"`
			CategoryName string     `json:"category_name"`
		} `json:"findings"`
	} `json:"reviews"`
}

func TestDayReview(t *testing.T) {
	c := newTestServer(t)
	var bank store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &bank)
	var hrana, ostalo store.Category
	c.do("POST", "/api/v1/categories", map[string]any{"name": "hrana"}, &hrana)
	c.do("POST", "/api/v1/categories", map[string]any{"name": "ostalo"}, &ostalo)
	add := func(desc, payee, tm, day string, amount int64, cat int64) store.Transaction {
		var tx store.Transaction
		c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bank.ID, "amount": amount, "description": desc, "payee": payee, "category_id": cat, "date": day, "time": tm}, &tx)
		return tx
	}
	for i := 1; i <= 3; i++ {
		add("nabavka za kuci", "", "18:00", fmt.Sprintf("2026-10-0%d", i), 4000, hrana.ID)
	}
	konzum := add("KONZUM D.O.O.", "Konzum", "18:10", "2026-10-29", 4520, ostalo.ID)
	kafa := add("kafa", "", "08:30", "2026-10-29", 350, hrana.ID)
	kafa2 := add("Kafa", "", "08:31", "2026-10-29", 350, hrana.ID)

	var out dayReviewOut
	c.do("GET", "/api/v1/day-review", nil, &out)
	if out.Settings.Mode != "report" || out.Settings.Hour != 0 || !out.Settings.Notify || out.AIReady {
		t.Fatalf("defaults %+v ready %v", out.Settings, out.AIReady)
	}
	if code := c.do("POST", "/api/v1/day-review/run", map[string]any{"day": "2026-10-29"}, nil); code != 400 {
		t.Fatalf("without an AI: %d", code)
	}

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
		answer := `{"findings":[]}`
		if strings.Contains(user, "2026-10-29") {
			answer = fmt.Sprintf(`{"findings":[
				{"id":%d,"category_id":%d,"description":"nabavka za kuci","note":"groceries at Konzum"},
				{"id":%d,"duplicate_of":%d,"note":"same coffee a minute later"},
				{"id":%d,"description":"Kafa","note":"cosmetic"},
				{"id":99999,"category_id":%d,"note":"not from this day"}],"note":"One shop trip and two coffees."}`,
				konzum.ID, hrana.ID, kafa2.ID, kafa.ID, kafa.ID, hrana.ID)
		}
		content, _ := json.Marshal(answer)
		fmt.Fprintf(w, `{"model":"fake","choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, content)
	}))
	defer ai.Close()
	c.do("PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_key": "sk-test", "ai_url": ai.URL}, nil)
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"ai_review": false}, nil)

	// report: two findings, nothing changed
	out = dayReviewOut{}
	if code := c.do("POST", "/api/v1/day-review/run", map[string]any{"day": "2026-10-29"}, &out); code != 200 {
		t.Fatalf("run %d", code)
	}
	p, _ := prompt.Load().(string)
	for _, want := range []string{"Thursday 2026-10-29", fmt.Sprintf("#%d 18:10 expense 45.20 BAM", konzum.ID), `description "KONZUM D.O.O."`, "from typed", "nabavka za kuci ×3, around 18h"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	rv := out.Reviews[0]
	if out.Open != 2 || len(rv.Findings) != 2 || rv.Checked != 3 || rv.Note != "One shop trip and two coffees." {
		t.Fatalf("report: open %d, %+v", out.Open, rv)
	}
	groceries, dup := rv.Findings[0], rv.Findings[1]
	if groceries.CategoryName != "hrana" || groceries.Description != "Nabavka za kuci" || groceries.Transaction.Description != "KONZUM D.O.O." || groceries.Status != "open" {
		t.Fatalf("groceries finding %+v", groceries)
	}
	if dup.Duplicate == nil || dup.Duplicate.ID != kafa.ID || dup.CategoryID != nil {
		t.Fatalf("duplicate finding %+v", dup)
	}

	// apply, then undo
	c.do("POST", fmt.Sprintf("/api/v1/day-review/findings/%d/apply", groceries.ID), nil, &out)
	var tx store.Transaction
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d", konzum.ID), nil, &tx)
	if tx.CategoryID == nil || *tx.CategoryID != hrana.ID || tx.Description != "Nabavka za kuci" || out.Open != 1 {
		t.Fatalf("applied: %v %q open %d", tx.CategoryID, tx.Description, out.Open)
	}
	if code := c.do("POST", fmt.Sprintf("/api/v1/day-review/findings/%d/apply", groceries.ID), nil, nil); code != 409 {
		t.Fatalf("apply twice: %d", code)
	}
	c.do("POST", fmt.Sprintf("/api/v1/day-review/findings/%d/undo", groceries.ID), nil, &out)
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d", konzum.ID), nil, &tx)
	if tx.CategoryID == nil || *tx.CategoryID != ostalo.ID || tx.Description != "KONZUM D.O.O." {
		t.Fatalf("undone: %v %q", tx.CategoryID, tx.Description)
	}
	c.do("POST", fmt.Sprintf("/api/v1/day-review/findings/%d/ignore", dup.ID), nil, &out)
	if out.Open != 0 {
		t.Fatalf("open after ignore %d", out.Open)
	}

	// fix: category and description corrected at once, the duplicate stays open
	c.do("PUT", "/api/v1/day-review/settings", map[string]any{"mode": "fix", "hour": 3}, &out)
	if out.Settings.Mode != "fix" || out.Settings.Hour != 3 || !out.Settings.Notify {
		t.Fatalf("settings %+v", out.Settings)
	}
	c.do("POST", "/api/v1/day-review/run", map[string]any{"day": "2026-10-29"}, &out)
	c.do("GET", fmt.Sprintf("/api/v1/transactions/%d", konzum.ID), nil, &tx)
	if tx.Description != "Nabavka za kuci" || *tx.CategoryID != hrana.ID || out.Open != 1 || out.Reviews[0].Findings[0].Status != "fixed" {
		t.Fatalf("fix mode: %q open %d %+v", tx.Description, out.Open, out.Reviews[0].Findings)
	}
	var audit struct {
		Items []struct{ Source string } `json:"items"`
	}
	c.do("GET", fmt.Sprintf("/api/v1/accounts/%d/audit", bank.ID), nil, &audit)
	if len(audit.Items) == 0 || audit.Items[0].Source != "day_review" {
		t.Errorf("audit %+v", audit.Items)
	}
	if code := c.do("PUT", "/api/v1/day-review/settings", map[string]any{"mode": "maybe"}, nil); code != 400 {
		t.Fatalf("bad mode %d", code)
	}

	// the daily run: the day before, once a day, quiet when nothing was booked
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	add("racun", "", "12:00", yesterday, 1000, ostalo.ID)
	c.do("PUT", "/api/v1/day-review/settings", map[string]any{"mode": "report", "hour": 0}, nil)
	before := calls.Load()
	c.srv.runJobs(context.Background(), false)
	c.srv.runJobs(context.Background(), false)
	if calls.Load() != before+1 {
		t.Fatalf("daily run asked the AI %d times", calls.Load()-before)
	}
	c.do("GET", "/api/v1/day-review", nil, &out)
	if out.Reviews[0].Day != yesterday || out.Reviews[0].Trigger != "daily" {
		t.Fatalf("daily review %+v", out.Reviews[0].DayReview)
	}
	// the Jobs contract reports it, with its runs
	var jl struct {
		Jobs []jobOut `json:"jobs"`
		Runs []jobRun `json:"runs"`
	}
	c.do("GET", "/api/v1/scheduled-jobs?since=0", nil, &jl)
	var dr *jobOut
	for i := range jl.Jobs {
		if jl.Jobs[i].ID == "day-review" {
			dr = &jl.Jobs[i]
		}
	}
	if dr == nil || !dr.Enabled || dr.Last == nil || !dr.Last.OK || !strings.Contains(dr.Last.Summary, yesterday) || dr.Next <= time.Now().UnixMilli() {
		t.Fatalf("day-review job %+v", dr)
	}
	nRuns := 0
	for _, r := range jl.Runs {
		if r.Job == "day-review" {
			nRuns++
		}
	}
	if nRuns != 3 { // report, fix, daily
		t.Fatalf("day-review runs %d", nRuns)
	}
	// off: no run
	c.do("PUT", "/api/v1/day-review/settings", map[string]any{"mode": "off"}, nil)
	_ = c.srv.store.CachePut(context.Background(), "dayreview:day:1", "")
	c.srv.runJobs(context.Background(), false)
	if calls.Load() != before+1 {
		t.Fatal("ran while off")
	}
}
