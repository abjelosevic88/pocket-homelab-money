package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-money/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- the day review ----
//
// Once a day (at the person's hour, 00:00 by default) the AI looks over the
// past day's expenses and income, wherever they came from (typed, the phone,
// templates, receipts, subscriptions), and flags bookings whose category or
// description does not fit how the person books, likely duplicates and
// amounts that look like typos. Mode report only lists them for the person
// to apply or ignore; fix also corrects category and description itself
// (undo is one click). Duplicates and odd amounts are always left to the
// person. It runs only when an AI is connected.

const dayReviewPrompt = `You look over one person's bookings of one day in their personal finance app and point out the ones that look wrong. Bookings come from several places: typed by hand, the phone app, templates, scanned shop receipts (with their items) and subscriptions. Receipts are from Bosnia and Herzegovina, Serbia, Montenegro or Croatia.

Flag a booking only when something is clearly off:
- category: it sits in a category where the person does not book such things (a category means what the person books in it, judged by their descriptions and how often they use it, not by its name);
- description: it is empty, a raw shop name or code, or does not say what it was, where the person would describe it in their own words (use their words, spelling and language; the time of day matters, a breakfast word does not fit an evening purchase);
- duplicate: the same purchase booked twice (same or nearly the same amount, same shop or description, close in time, e.g. a scanned receipt and a hand-typed entry);
- amount: an amount that looks like a typo (ten or a hundred times what such a thing costs).
Bookings that match the person's habits are fine: do not flag them and do not suggest cosmetic changes (capitals, synonyms, word order). An empty list is a good answer.

Answer only JSON: {"findings":[{"id":<booking id>,"category_id":<better category id, or null>,"description":"<better description, or empty>","duplicate_of":<id of the booking it repeats, or null>,"odd_amount":<true or false>,"note":"<why, under 15 words, English>"}],"note":"<one short English sentence about the day>"}.`

var sourceLabel = map[string]string{
	"receipt": "scanned receipt", "api": "phone app", "firefly_api": "phone app", "firefly": "Firefly", "manual": "typed",
	"web": "typed", "quick": "quick add", "template": "template", "subscription": "subscription", "import": "import",
}

// runDayReview looks over one day. trigger: daily | manual.
func (s *Server) runDayReview(ctx context.Context, day, trigger string) (*store.DayReview, error) {
	uid := actorID(ctx)
	ds := s.store.GetDayReviewSettings(ctx, uid)
	mode := ds.Mode
	if mode == "off" {
		mode = "report" // a review asked for by hand only reports
	}
	client, detail := s.receiptAI(ctx)
	if client == nil {
		return nil, fmt.Errorf("AI is not available: %s", detail)
	}
	run := &store.DayReview{UserID: uid, Day: day, Trigger: trigger, Mode: mode, StartedAt: store.Now(), Findings: []store.DayReviewFinding{}}
	ids, err := s.store.DayTransactionIDs(ctx, uid, day)
	if err != nil {
		return nil, err
	}
	if len(ids) > 120 {
		ids = ids[:120]
	}
	txs := map[int64]*store.Transaction{}
	var list strings.Builder
	for _, id := range ids {
		t, err := s.store.GetTransaction(ctx, id)
		if err != nil {
			continue
		}
		txs[id] = t
		fmt.Fprintf(&list, "#%d %s %s %s %s · %s", t.ID, firstNonBlank(t.Time, "--:--"), t.Kind, money(t.Amount), t.Currency, t.AccountName)
		if t.Payee != "" {
			fmt.Fprintf(&list, " · payee %q", t.Payee)
		}
		fmt.Fprintf(&list, " · description %q", t.Description)
		switch {
		case len(t.Splits) > 0:
			list.WriteString(" · split over several categories")
		case t.CategoryName != "":
			fmt.Fprintf(&list, " · category %q", t.CategoryName)
		default:
			list.WriteString(" · no category")
		}
		if len(t.Tags) > 0 {
			fmt.Fprintf(&list, " · tags %s", strings.Join(t.Tags, ", "))
		}
		fmt.Fprintf(&list, " · from %s", firstNonBlank(sourceLabel[t.Source], t.Source, "typed"))
		if t.ReceiptID != nil {
			if r, err := s.store.GetReceipt(ctx, *t.ReceiptID); err == nil && len(r.Items) > 0 {
				var items []string
				for i, it := range r.Items {
					if i == 12 {
						items = append(items, fmt.Sprintf("and %d more", len(r.Items)-12))
						break
					}
					items = append(items, fmt.Sprintf("%s ×%g %s", it.Name, it.Quantity, money(it.Total)))
				}
				fmt.Fprintf(&list, " · items: %s", strings.Join(items, "; "))
			}
		}
		list.WriteString("\n")
	}
	run.Checked = len(txs)
	if len(txs) > 0 {
		catText, valid, _ := s.categoryContext(ctx, uid)
		weekday := day
		if d, err := time.Parse("2006-01-02", day); err == nil {
			weekday = d.Weekday().String() + " " + day
		}
		prompt := fmt.Sprintf("The bookings of %s:\n%s\n%s", weekday, list.String(), catText)
		var out struct {
			Findings []struct {
				ID          FlexID `json:"id"`
				CategoryID  FlexID `json:"category_id"`
				Description string `json:"description"`
				DuplicateOf FlexID `json:"duplicate_of"`
				OddAmount   bool   `json:"odd_amount"`
				Note        string `json:"note"`
			} `json:"findings"`
			Note string `json:"note"`
		}
		actx, cancel := context.WithTimeout(ctx, 90*time.Second)
		usage, err := client.JSON(actx, dayReviewPrompt, prompt, &out)
		cancel()
		s.store.LogAI(ctx, uid, "day-review", usage.Model, usage.PromptTokens, usage.CompletionTokens)
		if err != nil {
			run.Error = err.Error()
		}
		run.Note = clip(strings.TrimSpace(out.Note), 200)
		seen := map[int64]bool{}
		for _, a := range out.Findings {
			t := txs[int64(a.ID)]
			if t == nil || seen[t.ID] {
				continue
			}
			f := store.DayReviewFinding{TransactionID: &t.ID, Note: clip(strings.TrimSpace(a.Note), 160), OddAmount: a.OddAmount}
			if id := int64(a.CategoryID); id > 0 && valid[id] && len(t.Splits) == 0 && !sameID(&id, t.CategoryID) && t.Kind == "expense" {
				f.CategoryID = &id
			}
			if d := clip(strings.Join(strings.Fields(a.Description), " "), 60); d != "" && !strings.EqualFold(d, strings.TrimSpace(t.Description)) {
				f.Description = ownCase(d)
			}
			if dup := int64(a.DuplicateOf); dup > 0 && dup != t.ID && txs[dup] != nil {
				f.DuplicateOf = &dup
			}
			if f.CategoryID == nil && f.Description == "" && f.DuplicateOf == nil && !f.OddAmount {
				continue // nothing to act on
			}
			seen[t.ID] = true
			if mode == "fix" && f.DuplicateOf == nil && !f.OddAmount {
				s.applyFinding(ctx, &f, t, "fixed")
			}
			run.Findings = append(run.Findings, f)
		}
	}
	run.FinishedAt = store.Now()
	if err := s.store.SaveDayReview(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

// applyFinding sets what the finding suggests and remembers what it replaced.
func (s *Server) applyFinding(ctx context.Context, f *store.DayReviewFinding, t *store.Transaction, status string) *store.Transaction {
	in := txIn{}
	changed := false
	f.BeforeCategoryID, f.BeforeDescription = t.CategoryID, t.Description
	if f.CategoryID != nil && len(t.Splits) == 0 {
		in.CategoryID, changed = f.CategoryID, true
	}
	if f.Description != "" {
		d := f.Description
		in.Description, changed = &d, true
	}
	if changed {
		t = s.updateTxAs(ctx, "day_review", t, &in)
	}
	f.Status = status
	return t
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// maybeDayReview runs the daily review once a day at the person's hour, for
// the day before.
func (s *Server) maybeDayReview(ctx context.Context) {
	uid := actorID(ctx)
	ds := s.store.GetDayReviewSettings(ctx, uid)
	if ds.Mode == "off" {
		return
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	key := fmt.Sprintf("dayreview:day:%d", uid)
	if last, _, _ := s.store.CacheGet(ctx, key); last == today || now.Hour() < ds.Hour {
		return
	}
	if client, _ := s.receiptAI(ctx); client == nil {
		return // no AI (yet): tried again on the next round
	}
	_ = s.store.CachePut(ctx, key, today)
	day := now.AddDate(0, 0, -1).Format("2006-01-02")
	if ids, _ := s.store.DayTransactionIDs(ctx, uid, day); len(ids) == 0 {
		return // nothing booked, no empty report
	}
	run, err := s.runDayReview(ctx, day, "daily")
	if err != nil {
		s.log.Warn("day review", "user", uid, "err", err)
		return
	}
	s.log.Info("day review", "user", uid, "day", day, "checked", run.Checked, "findings", len(run.Findings), "err", run.Error)
	if ds.Notify && len(run.Findings) > 0 {
		s.notifyDayReview(ctx, run)
	}
}

func (s *Server) notifyDayReview(ctx context.Context, run *store.DayReview) {
	c := s.clients(ctx)
	if !c.notify.Configured() {
		return
	}
	fixed, open := 0, 0
	for _, f := range run.Findings {
		if f.Status == "fixed" {
			fixed++
		} else {
			open++
		}
	}
	parts := []string{fmt.Sprintf("%d bookings checked", run.Checked)}
	if fixed > 0 {
		parts = append(parts, fmt.Sprintf("%d corrected", fixed))
	}
	if open > 0 {
		parts = append(parts, fmt.Sprintf("%d to look at", open))
	}
	body := strings.Join(parts, " · ")
	if run.Note != "" {
		body += "\n" + run.Note
	}
	day := run.Day
	if d, err := time.Parse("2006-01-02", run.Day); err == nil {
		day = d.Format("2.1.")
	}
	link := strings.TrimRight(s.cfg.BaseURL, "/") + "/review"
	if err := c.notify.Send(ctx, notify.Message{Title: "🔎 Day review · " + day, Body: body, Priority: "info", Tags: []string{"mag"}, Link: link}); err != nil {
		s.log.Warn("day review notify", "err", err)
	}
}

// ---- handlers ----

type findingOut struct {
	store.DayReviewFinding
	Transaction    *findingTx `json:"transaction"`
	Duplicate      *findingTx `json:"duplicate,omitempty"`
	CategoryName   string     `json:"category_name,omitempty"`
	CategoryIcon   string     `json:"category_icon,omitempty"`
	BeforeCategory string     `json:"before_category_name,omitempty"`
}

type findingTx struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"`
	Time         string `json:"time"`
	Kind         string `json:"kind"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	Description  string `json:"description"`
	Payee        string `json:"payee"`
	CategoryID   *int64 `json:"category_id"`
	CategoryName string `json:"category_name"`
	CategoryIcon string `json:"category_icon"`
	Source       string `json:"source"`
}

func (s *Server) findingTx(ctx context.Context, id *int64) *findingTx {
	if id == nil {
		return nil
	}
	t, err := s.store.GetTransaction(ctx, *id)
	if err != nil || t.UserID != actorID(ctx) {
		return nil
	}
	return &findingTx{ID: t.ID, Date: t.Date, Time: t.Time, Kind: t.Kind, Amount: t.Amount, Currency: t.Currency, Description: t.Description,
		Payee: t.Payee, CategoryID: t.CategoryID, CategoryName: t.CategoryName, CategoryIcon: t.CategoryIcon, Source: t.Source}
}

func (s *Server) dayReviewOut(ctx context.Context) map[string]any {
	uid := actorID(ctx)
	reviews, _ := s.store.DayReviews(ctx, uid, 30)
	cats, _ := s.store.ListCategories(ctx, uid)
	byID := map[int64]store.Category{}
	for _, c := range cats {
		byID[c.ID] = c
	}
	type reviewOut struct {
		store.DayReview
		Findings []findingOut `json:"findings"`
	}
	out := make([]reviewOut, 0, len(reviews))
	for _, r := range reviews {
		ro := reviewOut{DayReview: r, Findings: []findingOut{}}
		for _, f := range r.Findings {
			fo := findingOut{DayReviewFinding: f, Transaction: s.findingTx(ctx, f.TransactionID), Duplicate: s.findingTx(ctx, f.DuplicateOf)}
			if f.CategoryID != nil {
				c := byID[*f.CategoryID]
				fo.CategoryName, fo.CategoryIcon = firstNonBlank(c.FullName, c.Name), c.Icon
			}
			if f.BeforeCategoryID != nil {
				c := byID[*f.BeforeCategoryID]
				fo.BeforeCategory = firstNonBlank(c.FullName, c.Name)
			}
			ro.Findings = append(ro.Findings, fo)
		}
		out = append(out, ro)
	}
	client, detail := s.receiptAI(ctx)
	return map[string]any{"settings": s.store.GetDayReviewSettings(ctx, uid), "ai_ready": client != nil, "ai_detail": detail,
		"open": s.store.OpenDayFindings(ctx, uid), "reviews": out}
}

func (s *Server) handleGetDayReview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.dayReviewOut(r.Context()))
}

// handleDayReviewOpen is the sidebar's badge: findings still waiting.
func (s *Server) handleDayReviewOpen(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{"open": s.store.OpenDayFindings(r.Context(), actorID(r.Context()))})
}

func (s *Server) handlePutDayReviewSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur := s.store.GetDayReviewSettings(ctx, actorID(ctx))
	var in struct {
		Mode   *string `json:"mode"`
		Hour   *int    `json:"hour"`
		Notify *bool   `json:"notify"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Mode != nil {
		if *in.Mode != "off" && *in.Mode != "report" && *in.Mode != "fix" {
			writeErr(w, http.StatusBadRequest, "mode must be off, report or fix")
			return
		}
		cur.Mode = *in.Mode
	}
	if in.Hour != nil {
		if *in.Hour < 0 || *in.Hour > 23 {
			writeErr(w, http.StatusBadRequest, "hour must be 0–23")
			return
		}
		cur.Hour = *in.Hour
	}
	if in.Notify != nil {
		cur.Notify = *in.Notify
	}
	if err := s.store.SaveDayReviewSettings(ctx, actorID(ctx), cur); err != nil {
		s.fail(w, err, "day review settings")
		return
	}
	writeJSON(w, http.StatusOK, s.dayReviewOut(ctx))
}

func (s *Server) handleRunDayReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Day string `json:"day"`
	}
	_ = decode(r, &in)
	if in.Day == "" {
		in.Day = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", in.Day); err != nil {
		writeErr(w, http.StatusBadRequest, "day must be YYYY-MM-DD")
		return
	}
	run, err := s.runDayReview(ctx, in.Day, "manual")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := s.dayReviewOut(ctx)
	out["run"] = run.ID
	writeJSON(w, http.StatusOK, out)
}

// handleDayFinding: apply | ignore | done | undo.
func (s *Server) handleDayFinding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := s.store.GetDayFinding(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	action := chi.URLParam(r, "action")
	var t *store.Transaction
	if f.TransactionID != nil {
		if t, err = s.store.GetTransaction(ctx, *f.TransactionID); err != nil || !s.canWrite(ctx, t.AccountID) {
			t = nil
		}
	}
	switch action {
	case "apply":
		if f.Status != "open" {
			writeErr(w, http.StatusConflict, "already "+f.Status)
			return
		}
		if t == nil {
			writeErr(w, http.StatusConflict, "the transaction is gone")
			return
		}
		oldCat, oldDesc := t.CategoryID, t.Description
		t = s.applyFinding(ctx, f, t, "applied")
		s.learnFromEdit(ctx, oldCat, oldDesc, t) // the person chose it: a receipt's shop learns it
	case "ignore", "done":
		if f.Status != "open" {
			writeErr(w, http.StatusConflict, "already "+f.Status)
			return
		}
		f.Status = map[string]string{"ignore": "ignored", "done": "done"}[action]
	case "undo":
		if f.Status != "applied" && f.Status != "fixed" {
			writeErr(w, http.StatusConflict, "nothing to undo")
			return
		}
		if t == nil {
			writeErr(w, http.StatusConflict, "the transaction is gone")
			return
		}
		if (f.CategoryID != nil && !sameID(f.CategoryID, t.CategoryID)) || (f.Description != "" && t.Description != f.Description) {
			writeErr(w, http.StatusConflict, "the transaction was changed since; edit it by hand")
			return
		}
		in := txIn{}
		if f.CategoryID != nil {
			var none int64 // 0 clears the category
			in.CategoryID = &none
			if f.BeforeCategoryID != nil {
				in.CategoryID = f.BeforeCategoryID
			}
		}
		if f.Description != "" {
			d := f.BeforeDescription
			in.Description = &d
		}
		s.updateTxAs(ctx, "day_review", t, &in)
		f.Status = "undone"
	default:
		writeErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if err := s.store.SetDayFinding(ctx, f); err != nil {
		s.fail(w, err, "day review")
		return
	}
	writeJSON(w, http.StatusOK, s.dayReviewOut(ctx))
}
