package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
	"github.com/abjelosevic88/pocket-homelab-money/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- the daily product check ----
//
// Once a day (at the person's hour) every product without a group is
// looked at, in three passes:
//  1. keywords: the groups' keywords, as on every scan;
//  2. similar: a product whose first word only ever appears in one group's
//     products joins that group ("Jabuka zlatni" next to "Jabuka BIO" in
//     Fruit);
//  3. AI (when chosen): the rest are sent once — names only, with the
//     groups, their keywords and example products — and the AI places
//     them or suggests new groups, which the person creates with a click.
// With AI sorting of categories switched on, products without a category
// are sorted too. Every run leaves a report and, if wanted, a notification.

type checkMove struct {
	ProductID int64  `json:"product_id"`
	Product   string `json:"product"`
	GroupID   int64  `json:"group_id"`
	Group     string `json:"group"`
	Icon      string `json:"icon"`
	How       string `json:"how"` // keywords | similar | ai
}

type checkSuggestion struct {
	Name       string   `json:"name"`
	Icon       string   `json:"icon"`
	Keywords   string   `json:"keywords"`
	Products   []string `json:"products"`
	ProductIDs []int64  `json:"product_ids"`
}

type checkReport struct {
	Moves       []checkMove       `json:"moves"`
	Suggestions []checkSuggestion `json:"suggestions"`
	Left        []string          `json:"left"`
	AINote      string            `json:"ai_note,omitempty"`
}

// firstWord is a product key's first real word: letters, at least three
// of them ("ods424 pritiskivac…" → "pritiskivac").
func firstWord(key string) string {
	for _, w := range strings.Fields(key) {
		if len([]rune(w)) < 3 || strings.ContainsFunc(w, unicode.IsDigit) {
			continue
		}
		return strings.Trim(w, ".,-+/%")
	}
	return ""
}

func (s *Server) runProductCheck(ctx context.Context, trigger string) (*store.ProductCheckRun, error) {
	uid := actorID(ctx)
	rs := s.store.GetReceiptSettings(ctx, uid)
	run := &store.ProductCheckRun{Trigger: trigger, Mode: rs.GroupJob, StartedAt: store.Now()}
	if run.Mode == "off" {
		run.Mode = "keywords" // "run now" works with the job switched off
	}
	rep := checkReport{Moves: []checkMove{}, Suggestions: []checkSuggestion{}, Left: []string{}}
	groups, err := s.store.ListGroups(ctx, uid)
	if err != nil {
		return nil, err
	}
	byID := map[int64]store.ProductGroup{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	before, err := s.store.UngroupedProducts(ctx, uid)
	if err != nil {
		return nil, err
	}
	run.Checked = len(before)
	move := func(p store.UngroupedProduct, gid int64, how string) {
		g := byID[gid]
		rep.Moves = append(rep.Moves, checkMove{ProductID: p.ID, Product: p.Name, GroupID: gid, Group: g.Name, Icon: g.Icon, How: how})
	}

	// 1. keywords
	if _, err := s.store.RegroupProducts(ctx, uid); err != nil {
		return nil, err
	}
	after, _ := s.store.UngroupedProducts(ctx, uid)
	still := map[int64]bool{}
	for _, p := range after {
		still[p.ID] = true
	}
	for _, p := range before {
		if !still[p.ID] {
			if gid := store.GroupFor(p.Key, groups); gid != nil {
				move(p, *gid, "keywords")
				run.ByKeywords++
			}
		}
	}

	// 2. similar products
	grouped, _ := s.store.GroupedProducts(ctx, uid)
	wordGroups := map[string]map[int64]bool{}
	for _, g := range grouped {
		if w := firstWord(g.Key); w != "" {
			if wordGroups[w] == nil {
				wordGroups[w] = map[int64]bool{}
			}
			wordGroups[w][g.GroupID] = true
		}
	}
	var left []store.UngroupedProduct
	for _, p := range after {
		w := firstWord(p.Key)
		if gs := wordGroups[w]; w != "" && len(gs) == 1 {
			for gid := range gs {
				if err := s.store.SetProductGroup(ctx, uid, p.ID, gid, "similar"); err == nil {
					move(p, gid, "similar")
					run.BySimilar++
				}
			}
			continue
		}
		left = append(left, p)
	}

	// 3. AI
	if run.Mode == "ai" && len(left) > 0 {
		var ask []store.UngroupedProduct
		for _, p := range left {
			if !p.AIAsked {
				ask = append(ask, p)
			}
		}
		if len(ask) > 0 {
			placed, sugg, note := s.aiGroupProducts(ctx, ask, groups, grouped)
			rep.AINote = note
			done := map[int64]bool{}
			for _, p := range ask {
				if gid, ok := placed[p.ID]; ok {
					if err := s.store.SetProductGroup(ctx, uid, p.ID, gid, "ai"); err == nil {
						move(p, gid, "ai")
						run.ByAI++
						done[p.ID] = true
					}
				}
			}
			rep.Suggestions = append(rep.Suggestions, sugg...)
			var rest []store.UngroupedProduct
			for _, p := range left {
				if !done[p.ID] {
					rest = append(rest, p)
				}
			}
			left = rest
		}
		if rs.AIItems {
			if _, n, err := s.sortProducts(ctx, nil, 300); err == nil {
				run.Categorized = n
			}
		}
	}
	for i, p := range left {
		if i == 60 {
			break
		}
		rep.Left = append(rep.Left, p.Name)
	}
	run.StillUngrouped = len(left)
	run.FinishedAt = store.Now()
	b, _ := json.Marshal(rep)
	run.Report = b
	if err := s.store.SaveProductCheckRun(ctx, uid, run); err != nil {
		return nil, err
	}
	if rs.GroupJobNotify && (len(rep.Moves) > 0 || len(rep.Suggestions) > 0) {
		s.notifyProductCheck(ctx, run, rep)
	}
	return run, nil
}

// aiGroupProducts asks the AI to put products into the person's groups, or
// suggest new groups for the ones that fit none.
func (s *Server) aiGroupProducts(ctx context.Context, ask []store.UngroupedProduct, groups []store.ProductGroup, grouped []store.GroupedProduct) (map[int64]int64, []checkSuggestion, string) {
	uid := actorID(ctx)
	placed := map[int64]int64{}
	client, detail := s.receiptAI(ctx)
	if client == nil {
		return placed, nil, "AI was not available: " + detail
	}
	examples := map[int64][]string{}
	for _, g := range grouped {
		if len(examples[g.GroupID]) < 6 {
			examples[g.GroupID] = append(examples[g.GroupID], g.Name)
		}
	}
	valid := map[int64]bool{}
	var b strings.Builder
	b.WriteString("Product groups (id: name; keywords; example products):\n")
	for _, g := range groups {
		valid[g.ID] = true
		fmt.Fprintf(&b, "- %d: %s; %s", g.ID, g.Name, g.Keywords)
		if ex := examples[g.ID]; len(ex) > 0 {
			fmt.Fprintf(&b, "; e.g. %s", strings.Join(ex, ", "))
		}
		b.WriteString("\n")
	}
	var sugg []checkSuggestion
	for start := 0; start < len(ask) && start < 300; start += 60 {
		batch := ask[start:min(start+60, len(ask))]
		var u strings.Builder
		u.WriteString(b.String())
		u.WriteString("\nProducts:\n")
		for i, p := range batch {
			if p.Shop != "" {
				fmt.Fprintf(&u, "%d. %s (bought at %s)\n", i+1, p.Name, p.Shop)
			} else {
				fmt.Fprintf(&u, "%d. %s\n", i+1, p.Name)
			}
		}
		var out struct {
			Items []struct {
				N       int    `json:"n"`
				GroupID FlexID `json:"group_id"`
			} `json:"items"`
			NewGroups []struct {
				Name     string `json:"name"`
				Icon     string `json:"icon"`
				Keywords string `json:"keywords"`
				Products []int  `json:"products"`
			} `json:"new_groups"`
		}
		actx, cancel := context.WithTimeout(ctx, 60*time.Second)
		usage, err := client.JSON(actx, groupPrompt, u.String(), &out)
		cancel()
		s.store.LogAI(ctx, uid, "product-groups", usage.Model, usage.PromptTokens, usage.CompletionTokens)
		ids := make([]int64, 0, len(batch))
		for _, p := range batch {
			ids = append(ids, p.ID)
		}
		if err != nil {
			return placed, sugg, "AI failed: " + err.Error()
		}
		_ = s.store.MarkGroupAsked(ctx, ids)
		for _, it := range out.Items {
			if it.N >= 1 && it.N <= len(batch) && valid[int64(it.GroupID)] {
				placed[batch[it.N-1].ID] = int64(it.GroupID)
			}
		}
		for _, ng := range out.NewGroups {
			name := strings.TrimSpace(ng.Name)
			if name == "" {
				continue
			}
			sg := checkSuggestion{Name: name, Icon: strings.TrimSpace(ng.Icon), Keywords: strings.TrimSpace(ng.Keywords)}
			for _, n := range ng.Products {
				if n >= 1 && n <= len(batch) {
					if _, done := placed[batch[n-1].ID]; !done {
						sg.Products = append(sg.Products, batch[n-1].Name)
						sg.ProductIDs = append(sg.ProductIDs, batch[n-1].ID)
					}
				}
			}
			if len(sg.ProductIDs) > 0 {
				sugg = append(sugg, sg)
			}
		}
	}
	return placed, sugg, ""
}

const groupPrompt = `You sort products from shop receipts into a person's product groups ("Milk", "Coffee"…). Receipts are from Bosnia and Herzegovina, Serbia, Montenegro or Croatia; names are short and abbreviated ("VODA PR 0,5L" is bottled water, "KESA" a shopping bag).

Put a product in a group only when it clearly belongs there, judging by the group's name, keywords and example products. For products that fit no group, you may suggest a few new groups — only when at least one product needs it — with a short English name, one emoji and comma-separated keywords in the receipt's language that would match such product names.

Answer only JSON: {"items":[{"n":<product number>,"group_id":<id or null>}],"new_groups":[{"name":"…","icon":"…","keywords":"…","products":[<product numbers>]}]}.`

func (s *Server) notifyProductCheck(ctx context.Context, run *store.ProductCheckRun, rep checkReport) {
	c := s.clients(ctx)
	if !c.notify.Configured() {
		return
	}
	parts := []string{}
	if n := run.ByKeywords + run.BySimilar + run.ByAI; n > 0 {
		parts = append(parts, fmt.Sprintf("%d grouped (%d keywords, %d similar, %d AI)", n, run.ByKeywords, run.BySimilar, run.ByAI))
	}
	if len(rep.Suggestions) > 0 {
		names := []string{}
		for _, sg := range rep.Suggestions {
			names = append(names, sg.Name)
		}
		parts = append(parts, fmt.Sprintf("%d new groups suggested: %s", len(rep.Suggestions), strings.Join(names, ", ")))
	}
	if run.StillUngrouped > 0 {
		parts = append(parts, fmt.Sprintf("%d still without a group", run.StillUngrouped))
	}
	link := strings.TrimRight(s.cfg.BaseURL, "/") + "/products?report=1"
	if err := c.notify.Send(ctx, notify.Message{Title: "🛒 Product check", Body: strings.Join(parts, "\n"), Priority: "info", Tags: []string{"shopping_cart"}, Link: link}); err != nil {
		s.log.Warn("product check notify", "err", err)
	}
}

// maybeProductCheck runs the daily check once a day at the person's hour.
func (s *Server) maybeProductCheck(ctx context.Context) {
	uid := actorID(ctx)
	rs := s.store.GetReceiptSettings(ctx, uid)
	if rs.GroupJob == "off" {
		return
	}
	now := time.Now()
	key := fmt.Sprintf("productcheck:day:%d", uid)
	if last, _, _ := s.store.CacheGet(ctx, key); last == now.Format("2006-01-02") || now.Hour() < rs.GroupJobHour {
		return
	}
	_ = s.store.CachePut(ctx, key, now.Format("2006-01-02"))
	if ungrouped, _ := s.store.UngroupedProducts(ctx, uid); len(ungrouped) == 0 {
		return // nothing to do, no empty report
	}
	if run, err := s.runProductCheck(ctx, "daily"); err != nil {
		s.log.Warn("product check", "user", uid, "err", err)
	} else {
		s.log.Info("product check", "user", uid, "keywords", run.ByKeywords, "similar", run.BySimilar, "ai", run.ByAI, "left", run.StillUngrouped)
	}
}

// ---- handlers ----

func (s *Server) handleProductCheckRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ProductCheckRuns(r.Context(), actorID(r.Context()), 30)
	if err != nil {
		s.fail(w, err, "product check")
		return
	}
	ungrouped, _ := s.store.UngroupedProducts(r.Context(), actorID(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "ungrouped": len(ungrouped)})
}

func (s *Server) handleRunProductCheck(w http.ResponseWriter, r *http.Request) {
	run, err := s.runProductCheck(r.Context(), "manual")
	if err != nil {
		s.fail(w, err, "product check")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleCreateSuggestedGroup creates a group the check suggested and puts
// its products in it.
func (s *Server) handleCreateSuggestedGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Name       string  `json:"name"`
		Icon       string  `json:"icon"`
		Keywords   string  `json:"keywords"`
		ProductIDs []int64 `json:"product_ids"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "a group needs a name")
		return
	}
	g := &store.ProductGroup{UserID: uid, Name: strings.TrimSpace(in.Name), Icon: strings.TrimSpace(in.Icon), Keywords: strings.Join(fiscal.Keywords(in.Keywords), ", ")}
	if err := s.store.CreateGroup(ctx, g); err != nil {
		s.fail(w, err, "create group")
		return
	}
	n := 0
	for _, id := range in.ProductIDs {
		if p, err := s.store.GetProduct(ctx, uid, id); err == nil && p.GroupID == nil {
			if s.store.SetProductGroup(ctx, uid, id, g.ID, "ai") == nil {
				n++
			}
		}
	}
	moved, _ := s.store.RegroupProducts(ctx, uid)
	writeJSON(w, http.StatusCreated, map[string]any{"group": g, "placed": n, "moved": moved})
}
