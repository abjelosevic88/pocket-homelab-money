package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func TestProductCheck(t *testing.T) {
	fake := &fakeSUF{receipts: map[uint32]string{
		21: sufTestJSON(21, 2, [3]any{"JABUKA BIO/KG", 1.0, 3.0}, [3]any{"JABUKA ZLATNI/KG", 1.0, 2.0}, [3]any{"KAFA ZRNO 1KG", 1.0, 20.0}, [3]any{"ARIEL DETERDZENT", 1.0, 15.0}),
	}}
	receiptTransport = fake
	defer func() { receiptTransport = nil }()
	c := newTestServer(t)
	var bank store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &bank)
	var fruit, coffee struct{ Group store.ProductGroup }
	c.do("POST", "/api/v1/products/groups", map[string]any{"name": "Fruit", "keywords": "banana"}, &fruit)
	c.do("POST", "/api/v1/products/groups", map[string]any{"name": "Coffee", "keywords": "espresso"}, &coffee)

	var calls atomic.Int32
	line := regexp.MustCompile(`(?m)^(\d+)\. (.+?)( \(bought at .*\))?$`)
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		user := req.Messages[len(req.Messages)-1].Content
		answer := `{"items":[]}`
		if strings.Contains(user, "Product groups") {
			var items, newg []string
			for _, m := range line.FindAllStringSubmatch(user, -1) {
				switch {
				case strings.Contains(m[2], "Kafa"):
					items = append(items, fmt.Sprintf(`{"n":%s,"group_id":%d}`, m[1], coffee.Group.ID))
				case strings.Contains(m[2], "Ariel"):
					items = append(items, fmt.Sprintf(`{"n":%s,"group_id":null}`, m[1]))
					newg = append(newg, fmt.Sprintf(`{"name":"Cleaning","icon":"🧽","keywords":"deterdzent, ariel","products":[%s]}`, m[1]))
				}
			}
			answer = `{"items":[` + strings.Join(items, ",") + `],"new_groups":[` + strings.Join(newg, ",") + `]}`
		}
		content, _ := json.Marshal(answer)
		fmt.Fprintf(w, `{"model":"fake","choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, content)
	}))
	defer ai.Close()
	c.do("PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "ai_key": "sk-test", "ai_url": ai.URL}, nil)

	var res scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(21, 40)}, &res)
	// "Jabuka BIO" goes into Fruit by hand; its sibling should follow
	var prods []store.ItemStats
	c.do("GET", "/api/v1/products?group=none", nil, &prods)
	for _, p := range prods {
		if strings.EqualFold(p.Name, "Jabuka BIO") {
			c.do("PUT", fmt.Sprintf("/api/v1/products/%d", p.Key), map[string]any{"group_id": fruit.Group.ID}, nil)
		}
	}

	// keywords only: no AI
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"group_job": "keywords"}, nil)
	var run store.ProductCheckRun
	c.do("POST", "/api/v1/products/check", nil, &run)
	if run.BySimilar != 1 || run.ByAI != 0 || calls.Load() != 0 || run.StillUngrouped != 2 {
		t.Fatalf("keywords run %+v, %d AI calls", run, calls.Load())
	}

	// with AI: coffee placed, a cleaning group suggested
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"group_job": "ai", "group_job_hour": 3}, nil)
	c.do("POST", "/api/v1/products/check", nil, &run)
	var rep checkReport
	_ = json.Unmarshal(run.Report, &rep)
	if run.ByAI != 1 || len(rep.Suggestions) != 1 || rep.Suggestions[0].Name != "Cleaning" || run.StillUngrouped != 1 {
		t.Fatalf("ai run %+v %+v", run, rep)
	}
	var created struct {
		Placed int `json:"placed"`
	}
	sg := rep.Suggestions[0]
	c.do("POST", "/api/v1/products/check/suggestion", map[string]any{"name": sg.Name, "icon": sg.Icon, "keywords": sg.Keywords, "product_ids": sg.ProductIDs}, &created)
	if created.Placed != 1 {
		t.Fatalf("suggestion placed %d", created.Placed)
	}
	// asked once: a second run does not ask the AI again
	before := calls.Load()
	c.do("POST", "/api/v1/products/check", nil, &run)
	if calls.Load() != before || run.Checked != 0 {
		t.Fatalf("rerun asked again (%d → %d) or found %d", before, calls.Load(), run.Checked)
	}
	var runs struct {
		Runs      []store.ProductCheckRun `json:"runs"`
		Ungrouped int                     `json:"ungrouped"`
	}
	c.do("GET", "/api/v1/products/check", nil, &runs)
	if len(runs.Runs) != 3 || runs.Ungrouped != 0 {
		t.Fatalf("runs %d ungrouped %d", len(runs.Runs), runs.Ungrouped)
	}
	// a new receipt is grouped as it is scanned: a sibling of the apples joins Fruit right away
	fake.receipts[22] = sufTestJSON(22, 2, [3]any{"JABUKA CRVENA/KG", 1.0, 2.5})
	var res2 scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(22, 2.5)}, &res2)
	if res2.Receipt == nil || res2.Receipt.Items[0].GroupID == nil || *res2.Receipt.Items[0].GroupID != fruit.Group.ID {
		t.Fatalf("grouped on scan: %+v", res2.Receipt)
	}
	c.do("GET", "/api/v1/products/check", nil, &runs)
	if runs.Runs[0].Trigger != "scan" || runs.Runs[0].BySimilar != 1 {
		t.Fatalf("scan report %+v", runs.Runs[0])
	}
	// the AI placement survives keyword regrouping, and shows its source
	c.do("GET", fmt.Sprintf("/api/v1/products?group=%d", coffee.Group.ID), nil, &prods)
	if len(prods) != 1 || prods[0].GroupSource != "ai" {
		t.Fatalf("coffee products %+v", prods)
	}
}
