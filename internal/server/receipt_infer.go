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
// With the AI review on (and an AI connected), the AI then looks at the whole
// receipt, this guess included, and has the last word on both.

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
	uses, _ := s.store.CategoryUses(ctx, uid, 8)
	valid := map[int64]bool{}
	var b strings.Builder
	b.WriteString("The person's categories, with how many times they used each in the last year and what they wrote in it:\n")
	for _, c := range cats {
		if c.Archived || c.Kind == "income" {
			continue
		}
		valid[c.ID] = true
		u := uses[c.ID]
		if u == nil {
			u = &store.CategoryUse{}
		}
		fmt.Fprintf(&b, "- id %d \"%s\": %d bookings", c.ID, firstNonBlank(c.FullName, c.Name), u.Count)
		if len(u.Descriptions) > 0 {
			fmt.Fprintf(&b, "; e.g. %s", strings.Join(u.Descriptions, ", "))
		}
		b.WriteString("\n")
	}
	for start := 0; start < len(todo); start += 60 {
		batch := todo[start:min(start+60, len(todo))]
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
				N          int    `json:"n"`
				CategoryID FlexID `json:"category_id"`
			} `json:"items"`
		}
		actx, cancel := context.WithTimeout(ctx, 45*time.Second)
		usage, err := client.JSON(actx, sortPrompt, u.String(), &out)
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

const sortPrompt = `You sort the items of shop receipts into one person's spending categories.

Receipts are from Bosnia and Herzegovina, Serbia, Montenegro or Croatia; item names are short and abbreviated ("VODA PR 0,5L VIVIA" is bottled water, "KESA" a shopping bag, "ULJE SUNCOKRET" sunflower oil).

A category means what the person books in it, not what its name suggests: judge every category by its examples and by how often it is used. A name can mislead ("alimentacija" can be child support, not food); a category with few bookings and no similar example is almost never right. When the person writes the item itself as a description in some category (they book "vode" in one category), use that category. Groceries and household goods bought in a supermarket go where the person books their regular shopping trips, unless their examples put that kind of item elsewhere. Use null when no category fits.

Answer only JSON: {"items":[{"n":<product number>,"category_id":<id from the list, or null>}]}.`

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

// ---- the AI's look at the whole receipt ----

// reviewReceipt lets the AI pick the receipt's category and description from
// everything the app knows: the shop, the day and time, the items and their
// categories, what the rules above guessed, the person's earlier bookings at
// the shop and how they describe each category at which time of day. With the
// review off, without an AI or when it fails, the rules' guess stands.
func (s *Server) reviewReceipt(ctx context.Context, r *store.Receipt, g receiptGuess) receiptGuess {
	rs := s.store.GetReceiptSettings(ctx, actorID(ctx))
	if !rs.AIReview || r == nil || !r.Complete {
		return g
	}
	client, _ := s.receiptAI(ctx)
	if client == nil {
		return g
	}
	uid := actorID(ctx)
	cats, _ := s.store.ListCategories(ctx, uid)
	counts, _ := s.store.CategoryUses(ctx, uid, 0)
	words, _ := s.store.DescriptionUses(ctx, uid)
	perCat := map[int64][]string{}
	for _, w := range words {
		if len(perCat[w.CategoryID]) < 10 {
			perCat[w.CategoryID] = append(perCat[w.CategoryID], fmt.Sprintf("%s ×%d%s", w.Description, w.Count, usualHours(w.Hours)))
		}
	}
	valid, names := map[int64]bool{}, map[int64]string{}
	var b strings.Builder
	shop := payeeFor(r)
	b.WriteString("Receipt:\n")
	if m := r.Merchant; m != nil {
		fmt.Fprintf(&b, "- shop: %s (registered as %q)\n", shop, m.Name)
	}
	if r.Location != "" {
		fmt.Fprintf(&b, "- branch: %s\n", r.Location)
	}
	when := r.Date
	if d, err := time.Parse("2006-01-02", r.Date); err == nil {
		when = d.Weekday().String() + " " + r.Date
	}
	if r.Time != "" {
		when += " at " + r.Time
	}
	fmt.Fprintf(&b, "- when: %s\n- total: %s %s", when, money(r.Total), r.Currency)
	if r.Method != "" {
		fmt.Fprintf(&b, ", paid %s", r.Method)
	}
	b.WriteString("\n- items:\n")
	for i, it := range r.Items {
		fmt.Fprintf(&b, "  %d. %s (quantity %g) — %s", i+1, it.Name, it.Quantity, money(it.Total))
		var tags []string
		if it.GroupName != "" {
			tags = append(tags, "product group "+it.GroupName)
		}
		if it.CategoryName != "" {
			tags = append(tags, "category "+it.CategoryName)
		}
		if len(tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(tags, "; "))
		}
		b.WriteString("\n")
	}
	for _, c := range cats {
		names[c.ID] = firstNonBlank(c.FullName, c.Name)
	}
	if g.CategoryID != nil {
		fmt.Fprintf(&b, "\nThe app's rules suggest category %q (%s) and description %q.\n", names[*g.CategoryID], g.Reason, g.Description)
	} else {
		fmt.Fprintf(&b, "\nThe app's rules found no category; description %q.\n", g.Description)
	}
	if m := r.Merchant; m != nil && (m.CategorySource == "you" && m.CategoryID != nil || m.Description != "") {
		b.WriteString("What the person set for this shop:")
		if m.CategorySource == "you" && m.CategoryID != nil {
			fmt.Fprintf(&b, " category %q (their own choice)", names[*m.CategoryID])
		}
		if m.Description != "" {
			fmt.Fprintf(&b, " description %q (the last one they typed for it)", m.Description)
		}
		b.WriteString("\n")
	}
	if visits, _ := s.store.ShopVisits(ctx, uid, shop, 12); len(visits) > 0 {
		b.WriteString("Their earlier bookings at this shop, newest first (\"app\" = filled in by the app and never corrected):\n")
		for _, v := range visits {
			if r.TransactionID != nil && v.ID == *r.TransactionID {
				continue // this very receipt
			}
			by := ""
			if v.Source == "receipt" {
				by = " (app)"
			}
			fmt.Fprintf(&b, "- %s %s %q in %s%s\n", v.Date, v.Time, v.Description, firstNonBlank(v.Category, "no category"), by)
		}
	}
	b.WriteString("\nThe person's categories (id, name, bookings in the last year, their own descriptions with how often and usually when):\n")
	for _, c := range cats {
		if c.Archived || c.Kind == "income" {
			continue
		}
		valid[c.ID] = true
		n := 0
		if u := counts[c.ID]; u != nil {
			n = u.Count
		}
		fmt.Fprintf(&b, "- id %d %q: %d bookings", c.ID, names[c.ID], n)
		if ws := perCat[c.ID]; len(ws) > 0 {
			fmt.Fprintf(&b, "; %s", strings.Join(ws, ", "))
		}
		b.WriteString("\n")
	}
	var out struct {
		CategoryID  FlexID `json:"category_id"`
		Description string `json:"description"`
		Reason      string `json:"reason"`
	}
	actx, cancel := context.WithTimeout(ctx, 45*time.Second)
	usage, err := client.JSON(actx, reviewPrompt, b.String(), &out)
	cancel()
	s.store.LogAI(ctx, uid, "receipt-review", usage.Model, usage.PromptTokens, usage.CompletionTokens)
	if err != nil {
		s.log.Warn("receipt: AI review", "receipt", r.ID, "err", err)
		return g
	}
	res := g
	if id := int64(out.CategoryID); id > 0 && valid[id] {
		res.CategoryID = &id
	}
	if d := strings.Join(strings.Fields(out.Description), " "); d != "" && rs.OwnWords {
		if rr := []rune(d); len(rr) > 60 {
			d = string(rr[:60])
		}
		res.Description = ownCase(d)
	}
	why := strings.TrimSpace(out.Reason)
	if rr := []rune(why); len(rr) > 140 {
		why = string(rr[:140])
	}
	if why == "" {
		why = "the whole receipt"
	}
	res.Reason = "AI: " + why
	return res
}

const reviewPrompt = `You fill in one person's expense booking for a shop receipt they just scanned: its category and its short description, the way this person would have typed it themselves.

Receipts are from Bosnia and Herzegovina, Serbia, Montenegro or Croatia; item names are short and abbreviated.

Category: a category means what the person books in it, not what its name suggests; judge it by their descriptions and how often they use it. Go by where most of the money went, the kind of shop and their earlier bookings at this shop. When they chose a category for this shop themselves, keep it unless the items clearly belong elsewhere.

Description: the person's own words. Prefer one of their descriptions from the chosen category, spelled their way, and pick it by what was bought and by the time of day: a description they book in the morning (a breakfast) does not fit an evening purchase, and the other way round. Their last description for this shop is a strong hint, but the items and the time of day can overrule it. When none of their descriptions fits, write a short one (1–4 words) in their language and style, not the shop's name.

Answer only JSON: {"category_id": <id from the list, or null to keep the suggestion>, "description": "<text>", "reason": "<why, under 12 words, English>"}.`

// usualHours says when in the day something is usually booked (", usually 08–11h").
func usualHours(hours []int) string {
	if len(hours) == 0 {
		return ""
	}
	hs := append([]int(nil), hours...)
	sort.Ints(hs)
	lo, hi := hs[len(hs)/4], hs[(len(hs)*3)/4]
	if len(hs) < 4 {
		lo, hi = hs[0], hs[len(hs)-1]
	}
	if lo == hi {
		return fmt.Sprintf(", around %02dh", lo)
	}
	return fmt.Sprintf(", usually %02d–%02dh", lo, hi)
}

func money(minor int64) string {
	return strconv.FormatFloat(float64(minor)/100, 'f', 2, 64)
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
	street  = regexp.MustCompile(`(?i)^(ulica|ul\.)\s+`)
	houseNo = regexp.MustCompile(`(\d+)\s+([A-Za-z])\b`)
	numbers = regexp.MustCompile(`\s*\d+\s*[A-Za-z]?\s*$`)
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
	// "Ulica Marije Dimić 7 A" → "Marije Dimić 7a", then the street alone, then the shop by name
	addr := strings.TrimSpace(noNum.ReplaceAllString(fiscal.Latinize(m.Address), ""))
	addr = houseNo.ReplaceAllString(street.ReplaceAllString(addr, ""), "$1$2")
	if addr != "" && city != "" {
		queries = append(queries, addr+", "+city)
		if bare := strings.TrimSpace(numbers.ReplaceAllString(addr, "")); bare != "" && bare != addr {
			queries = append(queries, bare+", "+city)
		}
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
// With ?redo=1 the AI's earlier answers are dropped first (the person's
// own choices stay).
func (s *Server) handleSortProducts(w http.ResponseWriter, r *http.Request) {
	if queryBool(r, "redo") {
		if !s.store.GetReceiptSettings(r.Context(), actorID(r.Context())).AIItems {
			writeErr(w, http.StatusBadRequest, "switch on AI sorting first")
			return
		}
		if _, err := s.store.ForgetAISorting(r.Context(), actorID(r.Context())); err != nil {
			s.fail(w, err, "sort products")
			return
		}
	}
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
	g := s.reviewReceipt(ctx, rec, s.guessReceipt(ctx, rec))
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

// ---- preview before booking ----

type peekItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"`
	Total    int64   `json:"total"`
	Category string  `json:"category,omitempty"`
	// where the category comes from, so a preview can mark the AI's: you | ai | group
	CategoryID     *int64 `json:"category_id,omitempty"`
	CategoryIcon   string `json:"category_icon,omitempty"`
	CategorySource string `json:"category_source,omitempty"`
}

type peekOut struct {
	Supported    bool       `json:"supported"`
	Provider     string     `json:"provider"`
	Country      string     `json:"country"`
	Message      string     `json:"message,omitempty"`
	Complete     bool       `json:"complete"` // the tax office answered
	Shop         string     `json:"shop"`
	Location     string     `json:"location"`
	Date         string     `json:"date"`
	Time         string     `json:"time"`
	Total        int64      `json:"total"`
	Currency     string     `json:"currency"`
	Method       string     `json:"method"`
	Kind         string     `json:"kind"`
	Number       string     `json:"invoice_number"`
	Items        []peekItem `json:"items"`
	AccountName  string     `json:"account_name"`
	CategoryID   *int64     `json:"category_id"`
	Category     string     `json:"category"`
	CategoryIcon string     `json:"category_icon,omitempty"`
	Reason       string     `json:"reason"`
	Description  string     `json:"description"`
	Existing     *struct {
		ReceiptID     int64  `json:"receipt_id"`
		TransactionID *int64 `json:"transaction_id"`
		Description   string `json:"description"`
		Date          string `json:"date"`
		ScannedAt     string `json:"scanned_at"`
	} `json:"existing,omitempty"`
}

// handlePeekReceipt reads a receipt without booking or logging anything:
// what the scanner shows for the person to confirm.
func (s *Server) handlePeekReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		URL string `json:"url"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.URL) == "" {
		writeErr(w, http.StatusBadRequest, "send the receipt link as url")
		return
	}
	p, u, err := fiscal.Find(in.URL)
	if err != nil {
		writeJSON(w, http.StatusOK, peekOut{Message: err.Error(), Items: []peekItem{}})
		return
	}
	out := peekOut{Supported: true, Provider: p.ID(), Country: p.Country(), Items: []peekItem{}}
	existing := func(key string) bool {
		rec := s.existingReceipt(ctx, uid, p.ID(), key)
		if rec == nil {
			return false
		}
		out.Existing = &struct {
			ReceiptID     int64  `json:"receipt_id"`
			TransactionID *int64 `json:"transaction_id"`
			Description   string `json:"description"`
			Date          string `json:"date"`
			ScannedAt     string `json:"scanned_at"`
		}{ReceiptID: rec.ID, TransactionID: rec.TransactionID, Date: rec.Date, ScannedAt: rec.CreatedAt[:10]}
		if rec.TransactionID != nil {
			if t, err := s.store.GetTransaction(ctx, *rec.TransactionID); err == nil && s.canSee(ctx, t.AccountID) {
				out.Existing.Description = firstNonBlank(t.Description, t.Payee)
			}
		}
		out.Shop, out.Total, out.Currency, out.Date, out.Time, out.Number, out.Complete = payeeFor(rec), rec.Total, rec.Currency, rec.Date, rec.Time, rec.InvoiceNumber, rec.Complete
		for _, it := range rec.Items {
			out.Items = append(out.Items, peekItem{Name: firstNonBlank(it.ProductName, it.Name), Quantity: it.Quantity, Unit: it.Unit, Total: it.Total, Category: it.CategoryName,
				CategoryID: it.CategoryID, CategoryIcon: it.CategoryIcon, CategorySource: it.CategorySource})
		}
		return true
	}
	decoded, _ := p.Decode(u)
	if decoded != nil && decoded.Key != "" && existing(decoded.Key) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	rec, _, ferr := p.Fetch(fctx, s.receiptHTTP(), u)
	cancel()
	if ferr == nil && existing(rec.Key) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	switch {
	case ferr == nil:
		out.Complete = true
	case decoded != nil && decoded.Total > 0:
		rec = decoded
		out.Message = "The tax office did not answer (" + ferr.Error() + "). Booking now takes the total and time from the QR code; the shop and items follow later."
	default:
		out.Supported = false
		out.Message = ferr.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	if rec.InvoiceType == "proforma" || rec.InvoiceType == "training" {
		out.Message = fmt.Sprintf("This is a %s receipt: nothing was paid, nothing will be booked.", rec.InvoiceType)
	}
	out.Total, out.Currency, out.Method, out.Kind, out.Number = rec.Total, rec.Currency, rec.Method(), rec.Kind, rec.InvoiceNumber
	if !rec.IssuedAt.IsZero() {
		out.Date, out.Time = rec.IssuedAt.Format("2006-01-02"), rec.IssuedAt.Format("15:04")
	}
	out.Location = fiscal.Latinize(fiscal.ShortLocation(rec.Merchant.Location, rec.Merchant.Name))
	// what booking would do, from what is known already (no AI here)
	tmp := &store.Receipt{UserID: uid, Provider: rec.Provider, Total: rec.Total, Currency: rec.Currency, Method: rec.Method(), Location: out.Location, InvoiceNumber: rec.InvoiceNumber}
	if rec.Merchant.TaxID != "" {
		if m, err := s.store.MerchantByTaxID(ctx, uid, rec.Country, rec.Merchant.TaxID); err == nil {
			tmp.Merchant = m
		} else if rec.Merchant.Name != "" {
			nm := &store.Merchant{Name: rec.Merchant.Name, DisplayName: fiscal.MerchantName(rec.Merchant.Name)}
			rules, _ := s.shopRules(ctx, s.store.GetReceiptSettings(ctx, uid))
			if id, _ := matchShopRule(rules, rec.Merchant.Name+" "+out.Location); id > 0 {
				nm.CategoryID, nm.CategorySource = &id, "rule"
			}
			tmp.Merchant = nm
		}
	} else if m, err := s.store.MerchantForDevice(ctx, uid, rec.Provider, rec.Device); err == nil {
		tmp.Merchant = m
	}
	cats, _ := s.store.ListCategories(ctx, uid)
	catName, catIcon := map[int64]string{}, map[int64]string{}
	for _, c := range cats {
		catName[c.ID], catIcon[c.ID] = c.Name, c.Icon
	}
	groups, _ := s.store.ListGroups(ctx, uid)
	for _, it := range rec.Items {
		cat, src := s.store.ProductCategory(ctx, uid, fiscal.ProductKey(it.Name), groups)
		ri := store.ReceiptItem{Name: it.Name, Total: it.Total, CategoryID: cat, CategorySource: src}
		if cat != nil {
			ri.CategoryName = catName[*cat]
		}
		tmp.Items = append(tmp.Items, ri)
		pi := peekItem{Name: fiscal.ProductName(it.Name), Quantity: it.Quantity, Unit: it.Unit, Total: it.Total, Category: ri.CategoryName}
		if cat != nil {
			pi.CategoryID, pi.CategoryIcon, pi.CategorySource = cat, catIcon[*cat], src
		}
		out.Items = append(out.Items, pi)
	}
	out.Shop = payeeFor(tmp)
	g := s.guessReceipt(ctx, tmp)
	out.CategoryID, out.Reason, out.Description = g.CategoryID, g.Reason, g.Description
	if g.CategoryID != nil {
		out.Category, out.CategoryIcon = catName[*g.CategoryID], catIcon[*g.CategoryID]
	}
	if acct, writable := s.receiptAccount(ctx, tmp, nil); acct > 0 {
		for _, a := range writable {
			if a.ID == acct {
				out.AccountName = a.Name
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// locateReceipts gives transactions booked from receipts before shops were
// put on the map their shop's pin (a few per run: the map service allows
// one lookup a second). Goes through the normal update, so webhooks (Pocket
// Day) see the change.
func (s *Server) locateReceipts(ctx context.Context) {
	if !s.store.GetReceiptSettings(ctx, actorID(ctx)).Geocode {
		return
	}
	recs, err := s.store.ReceiptsWithoutPlace(ctx, actorID(ctx), 10)
	if err != nil {
		return
	}
	for i := range recs {
		m, err := s.store.GetMerchant(ctx, recs[i].UserID, *recs[i].MerchantID)
		if err != nil {
			continue
		}
		s.placeShop(ctx, m)
		if m.Lat == nil || m.Lng == nil || recs[i].TransactionID == nil {
			continue
		}
		t, err := s.store.GetTransaction(ctx, *recs[i].TransactionID)
		if err != nil || t.Lat != nil || !s.canWrite(ctx, t.AccountID) {
			continue
		}
		s.updateTxFrom(ctx, t, &txIn{Lat: m.Lat, Lng: m.Lng})
	}
}
