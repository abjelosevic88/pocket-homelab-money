package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- filling a scanned receipt in ----
//
// The category, in order:
//  1. the shop's category the person chose (by hand, or by changing the
//     category of one of its transactions);
//  2. the shop's category from its registered name (APOTEKA → apoteka);
//  3. where most of the money went among the items: each product's own
//     category (chosen by the person, or sorted once by the AI) or its
//     group's;
//  4. what this payee was booked as before.
// The description is the person's own words for the shop, else their most
// used description for that category, else the shop's name.

// ---- shop rules ----

// shopRules are the person's rules, or the defaults matched to their
// categories by name.
func (s *Server) shopRules(ctx context.Context, rs store.ReceiptSettings) ([]store.ShopRule, bool) {
	if rs.ShopRules != nil {
		return rs.ShopRules, false
	}
	cats, _ := s.store.ListCategories(ctx, actorID(ctx))
	byName := map[string]int64{}
	for _, c := range cats {
		if !c.Archived && c.Kind != "income" {
			if _, ok := byName[fiscal.Fold(c.Name)]; !ok {
				byName[fiscal.Fold(c.Name)] = c.ID
			}
		}
	}
	out := []store.ShopRule{}
	for _, r := range fiscal.DefaultShopRules {
		for _, cand := range r.Categories {
			if id, ok := byName[fiscal.Fold(cand)]; ok {
				out = append(out, store.ShopRule{Keywords: r.Keywords, CategoryID: id})
				break
			}
		}
	}
	return out, true
}

// matchShopRule finds the rule whose longest keyword is in the shop's
// registered name or branch.
func matchShopRule(rules []store.ShopRule, text string) (int64, string) {
	key := fiscal.Fold(text)
	best, bestKw := int64(0), ""
	for _, r := range rules {
		for _, kw := range fiscal.Keywords(r.Keywords) {
			if len(kw) > len(bestKw) && fiscal.MatchesKeyword(key, kw) {
				best, bestKw = r.CategoryID, kw
			}
		}
	}
	return best, bestKw
}

// categorizeShop gives a new (or still uncategorised) shop a category from
// its name.
func (s *Server) categorizeShop(ctx context.Context, m *store.Merchant, location string) {
	if m == nil || m.CategoryID != nil {
		return
	}
	rules, _ := s.shopRules(ctx, s.store.GetReceiptSettings(ctx, actorID(ctx)))
	if id, _ := matchShopRule(rules, m.Name+" "+location); id > 0 {
		if err := s.store.SetMerchantCategory(ctx, m.ID, &id, "rule"); err == nil {
			m.CategoryID, m.CategorySource = &id, "rule"
		}
	}
}

// ---- AI sorting of products ----

// receiptAI is the person's AI the way the rest of the app picks it:
// Pocket AI when this app's Core link is on, else their own OpenAI key.
func (s *Server) receiptAI(ctx context.Context) (*ai.Client, string) {
	c := s.clients(ctx)
	pocket, status := s.inferenceFor(ctx, actorID(ctx), c.st.AIProvider, c.st.AIKey != "")
	c.ai.Pocket = pocket.WithModel(c.st.AIPocketModel)
	if !c.ai.Configured() || !status.Ready {
		return nil, status.Detail
	}
	return c.ai, status.Detail
}

// sortProducts asks the AI, once per product, which of the person's
// categories each product without one belongs to. Only product names are
// sent, with the category list and examples of how the person books.
func (s *Server) sortProducts(ctx context.Context, ids []int64, limit int) (asked, sorted int, err error) {
	uid := actorID(ctx)
	todo, err := s.store.ProductsToSort(ctx, uid, ids, limit)
	if err != nil || len(todo) == 0 {
		return 0, 0, err
	}
	client, detail := s.receiptAI(ctx)
	if client == nil {
		return 0, 0, fmt.Errorf("AI is not available: %s", detail)
	}
	cats, _ := s.store.ListCategories(ctx, uid)
	valid := map[int64]bool{}
	var b strings.Builder
	b.WriteString("Categories (id: name):\n")
	for _, c := range cats {
		if !c.Archived && c.Kind != "income" {
			valid[c.ID] = true
			fmt.Fprintf(&b, "- %d: %s\n", c.ID, firstNonBlank(c.FullName, c.Name))
		}
	}
	if own, _ := s.store.OwnDescriptions(ctx, uid); len(own) > 0 {
		names := map[int64]string{}
		for _, c := range cats {
			names[c.ID] = c.Name
		}
		b.WriteString("\nHow this person books things (description → category):\n")
		for i, d := range own {
			if i == 30 {
				break
			}
			fmt.Fprintf(&b, "- %s → %s\n", d.Description, names[d.CategoryID])
		}
	}
	for start := 0; start < len(todo); start += 60 {
		batch := todo[start:min(start+60, len(todo))]
		var u strings.Builder
		u.WriteString(b.String())
		u.WriteString("\nProducts:\n")
		for i, p := range batch {
			fmt.Fprintf(&u, "%d. %s\n", i+1, p.Name)
		}
		var out struct {
			Items []struct {
				N          int    `json:"n"`
				CategoryID FlexID `json:"category_id"`
			} `json:"items"`
		}
		actx, cancel := context.WithTimeout(ctx, 45*time.Second)
		usage, err := client.JSON(actx, `You sort the items of shop receipts into a person's spending categories. Receipts are from Bosnia and Herzegovina, Serbia, Montenegro or Croatia; item names are short and abbreviated ("VODA PR 0,5L VIVIA" is bottled water, "KESA" a shopping bag). Use the person's examples to learn what their categories mean. Answer only JSON: {"items":[{"n":<product number>,"category_id":<id from the list, or null when nothing fits>}]}.`, u.String(), &out)
		cancel()
		s.store.LogAI(ctx, uid, "receipt-items", usage.Model, usage.PromptTokens, usage.CompletionTokens)
		if err != nil {
			return asked, sorted, err
		}
		done := make([]int64, 0, len(batch))
		for _, it := range out.Items {
			if it.N < 1 || it.N > len(batch) {
				continue
			}
			p := batch[it.N-1]
			if id := int64(it.CategoryID); id > 0 && valid[id] {
				if err := s.store.SetProductCategory(ctx, uid, p.ID, &id, "ai"); err == nil {
					sorted++
				}
			}
		}
		for _, p := range batch {
			done = append(done, p.ID)
		}
		_ = s.store.MarkProductsAsked(ctx, done)
		asked += len(batch)
	}
	return asked, sorted, nil
}

// ---- category and description ----

type receiptGuess struct {
	CategoryID  *int64
	Reason      string
	Description string
}

func (s *Server) guessReceipt(ctx context.Context, r *store.Receipt) receiptGuess {
	var g receiptGuess
	m := r.Merchant
	shop := payeeFor(r)
	switch {
	case m != nil && m.CategoryID != nil && m.CategorySource == "you":
		g.CategoryID, g.Reason = m.CategoryID, "your choice for "+shop
	case m != nil && m.CategoryID != nil && m.CategorySource == "rule":
		rules, _ := s.shopRules(ctx, s.store.GetReceiptSettings(ctx, actorID(ctx)))
		_, kw := matchShopRule(rules, m.Name+" "+r.Location)
		g.CategoryID, g.Reason = m.CategoryID, "the shop's name"
		if kw != "" {
			g.Reason = fmt.Sprintf("the shop's name has “%s”", kw)
		}
	case m != nil && m.CategoryID != nil:
		g.CategoryID, g.Reason = m.CategoryID, "the category of "+shop
	}
	if g.CategoryID == nil {
		g.CategoryID, g.Reason = itemVote(r)
	}
	if g.CategoryID == nil && shop != "" {
		if id := s.store.LearnedCategory(ctx, actorID(ctx), shop); id != nil {
			g.CategoryID, g.Reason = id, "how you booked "+shop+" before"
		}
	}
	g.Description = shop
	if g.Description == "" {
		g.Description = placeholderDescription(r)
	}
	if m != nil && m.Description != "" {
		g.Description = m.Description
	} else if g.CategoryID != nil && s.store.GetReceiptSettings(ctx, actorID(ctx)).OwnWords {
		if own, _ := s.store.OwnDescriptions(ctx, actorID(ctx)); len(own) > 0 {
			for _, d := range own { // most used first
				if d.CategoryID == *g.CategoryID {
					g.Description = ownCase(d.Description)
					break
				}
			}
		}
	}
	return g
}

// ownCase keeps the person's spelling but starts with a capital ("nabavka za kuci" → "Nabavka za kuci").
func ownCase(s string) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) == 0 {
		return ""
	}
	return strings.ToUpper(string(rs[0])) + string(rs[1:])
}

// itemVote is the category most of the receipt's money went to, when it is
// at least 40 % of the total.
func itemVote(r *store.Receipt) (*int64, string) {
	if len(r.Items) == 0 || r.Total <= 0 {
		return nil, ""
	}
	type vote struct {
		id    int64
		amt   int64
		n     int
		ai    bool
		named string
	}
	votes := map[int64]*vote{}
	for _, it := range r.Items {
		if it.CategoryID == nil || it.Total <= 0 {
			continue
		}
		v := votes[*it.CategoryID]
		if v == nil {
			v = &vote{id: *it.CategoryID, named: it.CategoryName}
			votes[*it.CategoryID] = v
		}
		v.amt += it.Total
		v.n++
		v.ai = v.ai || it.CategorySource == "ai"
	}
	list := make([]*vote, 0, len(votes))
	for _, v := range votes {
		list = append(list, v)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].amt > list[b].amt })
	if len(list) == 0 || list[0].amt*10 < r.Total*4 {
		return nil, ""
	}
	best := list[0]
	id := best.id
	reason := fmt.Sprintf("%d of %d items (%d %% of the money)", best.n, len(r.Items), best.amt*100/r.Total)
	if best.ai {
		reason += ", sorted by AI"
	}
	return &id, reason
}

// ---- shop locations ----

var (
	geoMu   sync.Mutex
	geoLast time.Time
	noNum   = regexp.MustCompile(`(?i)\s*\b(bb|b\.\s?b\.?|бб)\s*$`)
)

var geoCountry = map[string]string{"BA-SRP": "ba", "BA": "ba", "RS": "rs", "ME": "me", "HR": "hr"}

// placeShop looks the shop's address up once on OpenStreetMap (Nominatim,
// at most one request a second) and stores the pin, or that there is none.
func (s *Server) placeShop(ctx context.Context, m *store.Merchant) {
	if m == nil || m.Geocoded || !s.store.GetReceiptSettings(ctx, actorID(ctx)).Geocode {
		return
	}
	city := fiscal.Latinize(m.City)
	queries := []string{}
	if addr := strings.TrimSpace(noNum.ReplaceAllString(fiscal.Latinize(m.Address), "")); addr != "" && city != "" {
		queries = append(queries, addr+", "+city)
	}
	if city != "" {
		queries = append(queries, m.DisplayName+", "+city)
	}
	var lat, lng *float64
	for _, q := range queries {
		if la, ln, ok := s.nominatim(ctx, q, geoCountry[m.Country]); ok {
			lat, lng = &la, &ln
			break
		}
	}
	if err := s.store.SetMerchantPlace(ctx, m.ID, lat, lng); err == nil {
		m.Lat, m.Lng, m.Geocoded = lat, lng, true
	}
}

func (s *Server) nominatim(ctx context.Context, q, country string) (float64, float64, bool) {
	geoMu.Lock()
	if wait := time.Second - time.Since(geoLast); wait > 0 {
		time.Sleep(wait)
	}
	geoLast = time.Now()
	geoMu.Unlock()
	v := url.Values{"q": {q}, "format": {"json"}, "limit": {"1"}}
	if country != "" {
		v.Set("countrycodes", country)
	}
	gctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(gctx, http.MethodGet, "https://nominatim.openstreetmap.org/search?"+v.Encode(), nil)
	if err != nil {
		return 0, 0, false
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	res, err := s.receiptHTTP().Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer res.Body.Close()
	var out []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out) != nil || len(out) == 0 {
		return 0, 0, false
	}
	la, e1 := strconv.ParseFloat(out[0].Lat, 64)
	ln, e2 := strconv.ParseFloat(out[0].Lon, 64)
	return la, ln, e1 == nil && e2 == nil
}

// ---- learning from the person's edits ----

// learnFromEdit: changing the category or the description of a transaction
// booked from a receipt teaches the shop.
func (s *Server) learnFromEdit(ctx context.Context, oldCat *int64, oldDesc string, t *store.Transaction) {
	if t.ReceiptID == nil {
		return
	}
	r, err := s.store.GetReceipt(ctx, *t.ReceiptID)
	if err != nil || r.UserID != actorID(ctx) || r.MerchantID == nil {
		return
	}
	if t.CategoryID != nil && len(t.Splits) == 0 && (oldCat == nil || *oldCat != *t.CategoryID) {
		_ = s.store.SetMerchantCategory(ctx, *r.MerchantID, t.CategoryID, "you")
	}
	if d := strings.TrimSpace(t.Description); d != "" && d != oldDesc {
		_ = s.store.SetMerchantDescription(ctx, *r.MerchantID, d)
	}
}

// ---- handlers ----

// handleSortProducts asks the AI about every product still without a category.
func (s *Server) handleSortProducts(w http.ResponseWriter, r *http.Request) {
	asked, sorted, err := s.sortProducts(r.Context(), nil, 600)
	if err != nil && asked == 0 {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asked": asked, "sorted": sorted})
}

// handleApplyGuess sets the receipt's transaction to what the receipt says now
// (after items were given categories), without teaching the shop.
func (s *Server) handleApplyGuess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec := s.receiptFor(w, r, false)
	if rec == nil {
		return
	}
	if rec.TransactionID == nil {
		writeErr(w, http.StatusBadRequest, "this receipt is not booked")
		return
	}
	t, err := s.store.GetTransaction(ctx, *rec.TransactionID)
	if err != nil || !s.canWrite(ctx, t.AccountID) {
		writeErr(w, http.StatusForbidden, "you cannot change that transaction")
		return
	}
	g := s.guessReceipt(ctx, rec)
	in := txIn{}
	if g.CategoryID != nil && len(t.Splits) == 0 {
		in.CategoryID = g.CategoryID
	}
	if t.Source == "receipt" {
		d := g.Description
		if t.Kind == "income" {
			d = "Refund · " + d
		}
		in.Description = &d
	}
	updated := s.updateTxFrom(ctx, t, &in)
	_ = s.store.SetReceiptReason(ctx, rec.ID, g.Reason)
	writeJSON(w, http.StatusOK, map[string]any{"transaction": updated, "reason": g.Reason})
}
