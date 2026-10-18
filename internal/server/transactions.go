package server

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/quick"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// fillHome stores the transaction's value in the home currency at the day's rate.
func (s *Server) fillHome(ctx context.Context, t *store.Transaction) {
	st, _ := s.store.GetSettings(ctx)
	cur := t.Currency
	if cur == "" {
		if a, err := s.store.GetAccount(ctx, t.AccountID); err == nil {
			cur = a.Currency
			t.Currency = cur
		}
	}
	// a foreign amount already in the home currency is the exact figure
	if t.ForeignCurrency == st.HomeCurrency && t.ForeignAmount > 0 {
		t.AmountHome = t.ForeignAmount
		if t.Amount > 0 {
			t.FXRate = float64(t.ForeignAmount) / float64(t.Amount) * pow10f(currency.Decimals(cur)-currency.Decimals(st.HomeCurrency))
		}
	} else {
		r, _ := s.rate(ctx, cur, st.HomeCurrency, t.Date)
		t.FXRate = r
		t.AmountHome = currency.Convert(t.Amount, cur, st.HomeCurrency, r)
	}
	if len(t.Splits) > 0 {
		w := make([]int64, len(t.Splits))
		for i := range t.Splits {
			w[i] = t.Splits[i].Amount
		}
		for i, v := range apportion(t.AmountHome, w) {
			t.Splits[i].AmountHome = v
		}
	}
}

// apportion divides total in proportion to weights; the parts always add up
// exactly (largest remainder).
func apportion(total int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	var sum int64
	for _, w := range weights {
		sum += w
	}
	if sum == 0 || len(weights) == 0 {
		return out
	}
	type rem struct {
		i int
		r float64
	}
	var given int64
	rems := make([]rem, len(weights))
	for i, w := range weights {
		exact := float64(total) * float64(w) / float64(sum)
		out[i] = int64(exact)
		given += out[i]
		rems[i] = rem{i, exact - float64(out[i])}
	}
	sort.Slice(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
	for k := 0; given < total && k < len(rems); k++ {
		out[rems[k].i]++
		given++
	}
	return out
}

type splitIn struct {
	CategoryID *int64 `json:"category_id"`
	Amount     int64  `json:"amount"` // in the receipt currency: foreign when the transaction has one
	Note       string `json:"note"`
}

// applySplits validates the parts and fills their account-currency amounts.
// in == nil keeps the existing parts, rescaled when the total changed.
func (s *Server) applySplits(ctx context.Context, in *[]splitIn, t *store.Transaction) string {
	display := t.Amount
	if t.ForeignAmount > 0 {
		display = t.ForeignAmount
	}
	if in == nil {
		if t.Kind == "transfer" {
			t.Splits = nil
		}
		if len(t.Splits) == 0 {
			return ""
		}
		w := make([]int64, len(t.Splits))
		var sum int64
		for i, sp := range t.Splits {
			w[i] = sp.Amount
			sum += sp.Amount
		}
		if sum == t.Amount {
			return ""
		}
		for i, v := range apportion(t.Amount, w) {
			t.Splits[i].Amount = v
		}
		if t.ForeignAmount > 0 {
			for i, v := range apportion(t.ForeignAmount, w) {
				t.Splits[i].ForeignAmount = v
			}
		}
		return ""
	}
	var parts []splitIn
	for _, p := range *in {
		if p.Amount != 0 || p.CategoryID != nil {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		t.Splits = nil
		if len(parts) == 1 && parts[0].CategoryID != nil && *parts[0].CategoryID > 0 {
			t.CategoryID = parts[0].CategoryID
		}
		return ""
	}
	if t.Kind == "transfer" {
		return "a transfer cannot be split"
	}
	var sum int64
	w := make([]int64, len(parts))
	for i, p := range parts {
		if p.Amount <= 0 {
			return "every part needs an amount"
		}
		if p.CategoryID != nil && *p.CategoryID > 0 {
			if _, err := s.store.GetCategory(ctx, *p.CategoryID); err != nil {
				return "unknown category in a part"
			}
		}
		sum += p.Amount
		w[i] = p.Amount
	}
	if sum != display {
		return fmt.Sprintf("the parts add up to %s, the transaction is %s", majorString(sum, displayCur(t)), majorString(display, displayCur(t)))
	}
	t.Splits = make([]store.Split, len(parts))
	acct := w
	if t.ForeignAmount > 0 {
		acct = apportion(t.Amount, w)
	}
	var biggest int
	for i, p := range parts {
		sp := store.Split{CategoryID: idOrNil(p.CategoryID), Amount: acct[i], Note: strings.TrimSpace(p.Note)}
		if t.ForeignAmount > 0 {
			sp.ForeignAmount = p.Amount
		}
		t.Splits[i] = sp
		if p.Amount > parts[biggest].Amount {
			biggest = i
		}
	}
	// the transaction itself carries the biggest part's category (learning, old clients)
	t.CategoryID = t.Splits[biggest].CategoryID
	return ""
}

func displayCur(t *store.Transaction) string {
	if t.ForeignAmount > 0 {
		return t.ForeignCurrency
	}
	return t.Currency
}

func pow10f(n int) float64 {
	f := 1.0
	for ; n > 0; n-- {
		f *= 10
	}
	for ; n < 0; n++ {
		f /= 10
	}
	return f
}

type txIn struct {
	Kind            *string    `json:"kind"`
	Date            *string    `json:"date"`
	Time            *string    `json:"time"`
	AccountID       *int64     `json:"account_id"`
	Amount          *int64     `json:"amount"`   // in Currency when given, else the account's
	Currency        *string    `json:"currency"` // the currency Amount is in
	AccountAmount   *int64     `json:"account_amount"`
	ToAccountID     *int64     `json:"to_account_id"`
	ToAmount        *int64     `json:"to_amount"`
	ForeignAmount   *int64     `json:"foreign_amount"`
	ForeignCurrency *string    `json:"foreign_currency"`
	Description     *string    `json:"description"`
	Payee           *string    `json:"payee"`
	CategoryID      *int64     `json:"category_id"`
	Tags            *[]string  `json:"tags"`
	Notes           *string    `json:"notes"`
	SubscriptionID  *int64     `json:"subscription_id"`
	Lat             *float64   `json:"lat"`
	Lng             *float64   `json:"lng"`
	Source          string     `json:"source"`
	TemplateID      int64      `json:"template_id"`
	SkipRules       bool       `json:"skip_rules"`
	Splits          *[]splitIn `json:"splits"`
}

func idOrNil(p *int64) *int64 {
	if p == nil || *p <= 0 {
		return nil
	}
	v := *p
	return &v
}

// build applies the input onto t and resolves currencies. It returns a
// user-facing message when something is wrong.
func (s *Server) build(ctx context.Context, in *txIn, t *store.Transaction) string {
	if in.Kind != nil {
		t.Kind = *in.Kind
	}
	if in.Date != nil {
		t.Date = strings.TrimSpace(*in.Date)
	}
	if in.Time != nil {
		t.Time = strings.TrimSpace(*in.Time)
	}
	if in.AccountID != nil {
		t.AccountID = *in.AccountID
	}
	if in.ToAccountID != nil {
		t.ToAccountID = idOrNil(in.ToAccountID)
	}
	if in.Description != nil {
		t.Description = strings.TrimSpace(*in.Description)
	}
	if in.Payee != nil {
		t.Payee = strings.TrimSpace(*in.Payee)
	}
	if in.CategoryID != nil {
		t.CategoryID = idOrNil(in.CategoryID)
	}
	if in.Tags != nil {
		t.Tags = cleanTags(*in.Tags)
	}
	if in.Notes != nil {
		t.Notes = *in.Notes
	}
	if in.SubscriptionID != nil {
		t.SubscriptionID = idOrNil(in.SubscriptionID)
	}
	if in.Lat != nil && in.Lng != nil {
		t.Lat, t.Lng = in.Lat, in.Lng
	}
	if t.Date == "" {
		t.Date = store.Today()
	}
	if !store.Valid(store.TransactionKinds, t.Kind) {
		return "kind must be expense, income or transfer"
	}
	if !validDate(t.Date) {
		return "date must be YYYY-MM-DD"
	}
	if t.Time != "" {
		if _, err := time.Parse("15:04", t.Time); err != nil {
			return "time must be HH:MM"
		}
	}
	if t.AccountID == 0 {
		return "choose an account"
	}
	if !s.canWrite(ctx, t.AccountID) {
		return "you cannot book on that account"
	}
	acct, err := s.store.GetAccount(ctx, t.AccountID)
	if err != nil {
		return "unknown account"
	}
	t.Currency = acct.Currency

	// amounts
	if in.ForeignAmount != nil {
		t.ForeignAmount = *in.ForeignAmount
	}
	if in.ForeignCurrency != nil {
		t.ForeignCurrency = strings.ToUpper(strings.TrimSpace(*in.ForeignCurrency))
	}
	if in.Amount != nil {
		cur := acct.Currency
		if in.Currency != nil && strings.TrimSpace(*in.Currency) != "" {
			cur = strings.ToUpper(strings.TrimSpace(*in.Currency))
			if !currency.Valid(cur) {
				return "unknown currency"
			}
		}
		if cur == acct.Currency {
			t.Amount = *in.Amount
			if in.Currency != nil {
				t.ForeignAmount, t.ForeignCurrency = 0, ""
			}
		} else {
			t.ForeignAmount, t.ForeignCurrency = *in.Amount, cur
			if in.AccountAmount != nil && *in.AccountAmount > 0 {
				t.Amount = *in.AccountAmount
			} else {
				r, _ := s.rate(ctx, cur, acct.Currency, t.Date)
				t.Amount = currency.Convert(*in.Amount, cur, acct.Currency, r)
			}
		}
	} else if in.AccountAmount != nil {
		t.Amount = *in.AccountAmount
	}
	if t.Amount < 0 {
		// a negative amount flips direction for expense/income
		t.Amount = -t.Amount
		switch t.Kind {
		case "expense":
			t.Kind = "income"
		case "income":
			t.Kind = "expense"
		}
	}
	if t.Amount == 0 {
		return "amount is required"
	}
	if t.ForeignCurrency == acct.Currency || t.ForeignAmount <= 0 {
		t.ForeignAmount, t.ForeignCurrency = 0, ""
	}
	if t.ForeignCurrency != "" && !currency.Valid(t.ForeignCurrency) {
		return "unknown foreign currency"
	}

	if t.Kind == "transfer" {
		if t.ToAccountID == nil || *t.ToAccountID == t.AccountID {
			return "a transfer needs a different destination account"
		}
		if !s.canWrite(ctx, *t.ToAccountID) {
			return "you cannot book on the destination account"
		}
		to, err := s.store.GetAccount(ctx, *t.ToAccountID)
		if err != nil {
			return "unknown destination account"
		}
		t.ToCurrency = to.Currency
		switch {
		case in.ToAmount != nil && *in.ToAmount > 0:
			t.ToAmount = *in.ToAmount
		case to.Currency == acct.Currency:
			t.ToAmount = t.Amount
		case t.ForeignCurrency == to.Currency && t.ForeignAmount > 0:
			t.ToAmount = t.ForeignAmount
		case t.ToAmount == 0 || in.Amount != nil:
			r, _ := s.rate(ctx, acct.Currency, to.Currency, t.Date)
			t.ToAmount = currency.Convert(t.Amount, acct.Currency, to.Currency, r)
		}
		if to.Currency == acct.Currency {
			t.ToAmount = t.Amount
		}
	} else {
		t.ToAccountID, t.ToAmount = nil, 0
	}
	if t.CategoryID != nil {
		if _, err := s.store.GetCategory(ctx, *t.CategoryID); err != nil {
			return "unknown category"
		}
	}
	if t.Kind == "transfer" {
		t.Splits = nil
	} else if msg := s.applySplits(ctx, in.Splits, t); msg != "" {
		return msg
	}
	s.fillHome(ctx, t)
	return ""
}

func cleanTags(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// createTx runs the full pipeline for a new transaction: rules, trip tag,
// insert, subscription match, template counter.
func (s *Server) createTx(ctx context.Context, in *txIn) (*store.Transaction, string, error) {
	uid := actorID(ctx)
	t := &store.Transaction{Kind: "expense", Source: "manual", Tags: []string{}}
	if in.Source != "" {
		t.Source = in.Source
	}
	if uid > 0 {
		t.CreatedBy = &uid
	}
	if in.Time == nil && (in.Date == nil || *in.Date == store.Today()) {
		now := time.Now().Format("15:04")
		in.Time = &now
	}
	if msg := s.build(ctx, in, t); msg != "" {
		return nil, msg, nil
	}
	if !in.SkipRules {
		s.applyRules(ctx, t, uid)
	}
	if t.Kind == "expense" && t.Source != "firefly" {
		s.addTripTag(ctx, t)
	}
	if err := s.store.CreateTransaction(ctx, t); err != nil {
		return nil, "", err
	}
	if t.SubscriptionID == nil {
		s.matchSubscription(ctx, t)
	} else {
		s.advanceSubscription(ctx, *t.SubscriptionID, t.Date)
	}
	if in.TemplateID > 0 {
		s.store.TouchTemplate(ctx, in.TemplateID)
	}
	full, err := s.store.GetTransaction(ctx, t.ID)
	if err == nil {
		s.emitTx(ctx, "transaction.created", full)
	}
	return full, "", err
}

func (s *Server) handleCreateTransaction(w http.ResponseWriter, r *http.Request) {
	var in txIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	t, msg, err := s.createTx(r.Context(), &in)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		s.fail(w, err, "create transaction")
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) txFilter(r *http.Request) store.TxFilter {
	q := r.URL.Query()
	f := store.TxFilter{AccountIDs: s.visibleIDs(r.Context()), Kind: q.Get("kind"), Tag: q.Get("tag"), From: q.Get("from"), To: q.Get("to"),
		Q: q.Get("q"), Payee: q.Get("payee"), Source: q.Get("source"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset"), Oldest: queryBool(r, "oldest")}
	f.AccountID, _ = strconv.ParseInt(q.Get("account"), 10, 64)
	f.SubscriptionID, _ = strconv.ParseInt(q.Get("subscription"), 10, 64)
	if c := q.Get("category"); c == "none" {
		f.Uncategorized = true
	} else if c != "" {
		var ids []int64
		for _, p := range strings.Split(c, ",") {
			if id, err := strconv.ParseInt(p, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
		if queryBool(r, "children") || q.Get("children") == "" {
			ids, _ = s.store.CategoryChildren(r.Context(), ids)
		}
		f.CategoryIDs = ids
		if len(ids) == 0 {
			f.CategoryIDs = []int64{-1}
		}
	}
	if v := q.Get("min"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.MinHome = &n
		}
	}
	if v := q.Get("max"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.MaxHome = &n
		}
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	return f
}

func (s *Server) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	f := s.txFilter(r)
	if f.Limit <= 0 {
		f.Limit = 100
	}
	items, tot, err := s.store.ListTransactions(r.Context(), f)
	if err != nil {
		s.fail(w, err, "transactions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "totals": tot, "has_more": f.Offset+len(items) < tot.Count})
}

// loadTx returns a transaction the user may see; write=true also requires write access.
func (s *Server) loadTx(w http.ResponseWriter, r *http.Request, write bool) *store.Transaction {
	ctx := r.Context()
	t, err := s.store.GetTransaction(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "transaction")
		return nil
	}
	ok := s.canSee(ctx, t.AccountID) || (t.ToAccountID != nil && s.canSee(ctx, *t.ToAccountID))
	if write {
		ok = s.canWrite(ctx, t.AccountID)
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return t
}

func (s *Server) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	if t := s.loadTx(w, r, false); t != nil {
		writeJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleUpdateTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := s.loadTx(w, r, true)
	if t == nil {
		return
	}
	oldSub := t.SubscriptionID
	var in txIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := s.build(ctx, &in, t); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateTransaction(ctx, t); err != nil {
		s.fail(w, err, "update transaction")
		return
	}
	if t.SubscriptionID != nil && (oldSub == nil || *oldSub != *t.SubscriptionID) {
		s.advanceSubscription(ctx, *t.SubscriptionID, t.Date)
	}
	t, _ = s.store.GetTransaction(ctx, t.ID)
	s.emitTx(ctx, "transaction.updated", t)
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := s.loadTx(w, r, true)
	if t == nil {
		return
	}
	for _, p := range s.store.AttachmentPaths(ctx, t.ID) {
		s.removeFile(p)
	}
	if err := s.store.DeleteTransaction(ctx, t.ID); err != nil {
		s.fail(w, err, "delete transaction")
		return
	}
	s.emitTx(ctx, "transaction.deleted", t)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDuplicateTransaction(w http.ResponseWriter, r *http.Request) {
	t := s.loadTx(w, r, true)
	if t == nil {
		return
	}
	today := store.Today()
	in := txIn{Kind: &t.Kind, Date: &today, AccountID: &t.AccountID, AccountAmount: &t.Amount, ToAccountID: t.ToAccountID, Description: &t.Description,
		Payee: &t.Payee, CategoryID: t.CategoryID, Tags: &t.Tags, Notes: &t.Notes, Source: "manual", SkipRules: true}
	if t.ForeignAmount > 0 {
		in.ForeignAmount, in.ForeignCurrency = &t.ForeignAmount, &t.ForeignCurrency
	}
	if t.Kind == "transfer" {
		in.ToAmount = &t.ToAmount
	}
	nt, msg, err := s.createTx(r.Context(), &in)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		s.fail(w, err, "duplicate")
		return
	}
	writeJSON(w, http.StatusCreated, nt)
}

func (s *Server) handleBulkTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		IDs        []int64  `json:"ids"`
		Action     string   `json:"action"` // delete | category | add_tags | remove_tags
		CategoryID *int64   `json:"category_id"`
		Tags       []string `json:"tags"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var ids []int64
	before := map[int64]*store.Transaction{}
	for _, id := range in.IDs {
		if t, err := s.store.GetTransaction(ctx, id); err == nil && s.canWrite(ctx, t.AccountID) {
			ids = append(ids, id)
			before[id] = t
		}
	}
	var err error
	switch in.Action {
	case "delete":
		for _, id := range ids {
			for _, p := range s.store.AttachmentPaths(ctx, id) {
				s.removeFile(p)
			}
			if err = s.store.DeleteTransaction(ctx, id); err != nil {
				break
			}
			s.emitTx(ctx, "transaction.deleted", before[id])
		}
	case "category":
		err = s.store.BulkUpdate(ctx, ids, idOrNil(in.CategoryID), in.CategoryID == nil || *in.CategoryID <= 0, nil, nil)
	case "add_tags":
		err = s.store.BulkUpdate(ctx, ids, nil, false, cleanTags(in.Tags), nil)
	case "remove_tags":
		err = s.store.BulkUpdate(ctx, ids, nil, false, nil, in.Tags)
	default:
		writeErr(w, http.StatusBadRequest, "unknown action")
		return
	}
	if err != nil {
		s.fail(w, err, "bulk")
		return
	}
	if in.Action != "delete" {
		for _, id := range ids {
			if t, err := s.store.GetTransaction(ctx, id); err == nil {
				s.emitTx(ctx, "transaction.updated", t)
			}
		}
	}
	s.store.LogActivity(ctx, actorID(ctx), "transactions."+in.Action, fmt.Sprintf("%d transactions", len(ids)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(ids)})
}

// ---- attachments ----

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := s.loadTx(w, r, true)
	if t == nil {
		return
	}
	data, name, mt, err := s.readUpload(r, "file")
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "no file")
		return
	}
	if name == "" {
		name = "receipt"
	}
	if mt == "" || mt == "application/octet-stream" {
		if guess := mime.TypeByExtension(filepath.Ext(name)); guess != "" {
			mt = guess
		} else {
			mt = http.DetectContentType(data)
		}
	}
	rel, err := s.saveFile(t.Date[:7], filepath.Ext(name), data)
	if err != nil {
		s.fail(w, err, "save file")
		return
	}
	a := &store.Attachment{TransactionID: t.ID, Name: name, Mime: mt, Size: int64(len(data)), Path: rel}
	if err := s.store.CreateAttachment(ctx, a); err != nil {
		s.removeFile(rel)
		s.fail(w, err, "attachment")
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) attachmentFor(w http.ResponseWriter, r *http.Request, write bool) *store.Attachment {
	ctx := r.Context()
	a, err := s.store.GetAttachment(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "attachment")
		return nil
	}
	t, err := s.store.GetTransaction(ctx, a.TransactionID)
	if err != nil || !s.canSee(ctx, t.AccountID) || (write && !s.canWrite(ctx, t.AccountID)) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return a
}

func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if a := s.attachmentFor(w, r, false); a != nil {
		dl := ""
		if queryBool(r, "download") {
			dl = a.Name
		}
		s.serveFile(w, r, a.Path, a.Mime, dl)
	}
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if a := s.attachmentFor(w, r, true); a != nil {
		s.removeFile(a.Path)
		if err := s.store.DeleteAttachment(r.Context(), a.ID); err != nil {
			s.fail(w, err, "delete attachment")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- autocomplete ----

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.DescriptionSuggestions(r.Context(), s.visibleIDs(r.Context()), r.URL.Query().Get("q"), 8)
	if err != nil {
		s.fail(w, err, "suggest")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePayees(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Payees(r.Context(), s.visibleIDs(r.Context()))
	if err != nil {
		s.fail(w, err, "payees")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- quick add ----

// quickContext gathers what the parser matches against for the current user.
func (s *Server) quickContext(ctx context.Context) (quick.Context, []store.Account) {
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx)
	accts, _ := s.store.ListAccounts(ctx, uid, false)
	c := quick.Context{Today: time.Now()}
	writable := []store.Account{}
	for _, a := range accts {
		if a.Role == "viewer" {
			continue
		}
		writable = append(writable, a)
		c.Accounts = append(c.Accounts, quick.Account{ID: a.ID, Name: a.Name, Currency: a.Currency})
	}
	c.DefaultAccount = s.defaultAccount(ctx, st, writable)
	cats, _ := s.store.ListCategories(ctx)
	for _, cat := range cats {
		if !cat.Archived {
			c.Categories = append(c.Categories, quick.Named{ID: cat.ID, Name: cat.Name})
		}
	}
	tpls, _ := s.store.ListTemplates(ctx, uid)
	for _, t := range tpls {
		names := []string{t.Name}
		for _, a := range strings.Split(t.Aliases, ",") {
			if a = strings.TrimSpace(a); a != "" {
				names = append(names, a)
			}
		}
		qt := quick.Template{ID: t.ID, Names: names, Kind: t.Kind, Amount: t.Amount, Currency: t.Currency, Tags: t.Tags, Description: t.Description, Payee: t.Payee}
		if t.AccountID != nil {
			qt.AccountID = *t.AccountID
		}
		if t.ToAccountID != nil {
			qt.ToAccountID = *t.ToAccountID
		}
		if t.CategoryID != nil {
			qt.CategoryID = *t.CategoryID
		}
		c.Templates = append(c.Templates, qt)
	}
	c.Learn = func(text string) int64 {
		if id := s.store.LearnedCategory(ctx, text); id != nil {
			return *id
		}
		return 0
	}
	return c, writable
}

// defaultAccount is the configured default when the user may book on it,
// else the account they used most recently, else their first.
func (s *Server) defaultAccount(ctx context.Context, st store.Settings, writable []store.Account) int64 {
	for _, a := range writable {
		if a.ID == st.DefaultAcct {
			return a.ID
		}
	}
	best, bestDate := int64(0), ""
	for _, a := range writable {
		if a.LastTxDate > bestDate && a.Kind != "savings" {
			best, bestDate = a.ID, a.LastTxDate
		}
	}
	if best == 0 && len(writable) > 0 {
		best = writable[0].ID
	}
	return best
}

type quickPreview struct {
	quick.Draft
	AccountName   string  `json:"account_name"`
	AccountCur    string  `json:"account_currency"`
	ToAccountName string  `json:"to_account_name,omitempty"`
	CategoryName  string  `json:"category_name"`
	CategoryIcon  string  `json:"category_icon"`
	AccountAmount *int64  `json:"account_amount"` // Amount converted into the account's currency
	Rate          float64 `json:"rate"`
	Ready         bool    `json:"ready"`
	Problem       string  `json:"problem,omitempty"`
}

func (s *Server) preview(ctx context.Context, d quick.Draft, accts []store.Account) quickPreview {
	p := quickPreview{Draft: d, Rate: 1}
	for _, a := range accts {
		if a.ID == d.AccountID {
			p.AccountName, p.AccountCur = a.Name, a.Currency
		}
		if a.ID == d.ToAccountID {
			p.ToAccountName = a.Name
		}
	}
	if d.CategoryID > 0 {
		if c, err := s.store.GetCategory(ctx, d.CategoryID); err == nil {
			p.CategoryName, p.CategoryIcon = c.Name, c.Icon
		}
	}
	if p.Currency == "" {
		p.Currency = p.AccountCur
	}
	if d.Amount != nil {
		v := *d.Amount
		if p.Currency != p.AccountCur && p.AccountCur != "" {
			r, _ := s.rate(ctx, p.Currency, p.AccountCur, d.Date)
			p.Rate = r
			v = currency.Convert(v, p.Currency, p.AccountCur, r)
		}
		p.AccountAmount = &v
	}
	switch {
	case d.AccountID == 0:
		p.Problem = "no account to book on"
	case d.Amount == nil || *d.Amount == 0:
		p.Problem = "type an amount"
	default:
		p.Ready = true
	}
	return p
}

func splitLines(text string) []string {
	var out []string
	for _, l := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ';' }) {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (s *Server) handleQuickParse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, accts := s.quickContext(r.Context())
	out := []quickPreview{}
	for _, line := range splitLines(in.Text) {
		out = append(out, s.preview(r.Context(), quick.Parse(line, c), accts))
	}
	writeJSON(w, http.StatusOK, out)
}

// draftToIn turns a parsed (or AI) draft into transaction input.
func draftToIn(d quick.Draft, source string) txIn {
	in := txIn{Kind: &d.Kind, Date: &d.Date, AccountID: &d.AccountID, Amount: d.Amount, Description: &d.Description, Payee: &d.Payee, Tags: &d.Tags, Source: source, TemplateID: d.TemplateID}
	if d.Currency != "" {
		in.Currency = &d.Currency
	}
	if d.Time != "" {
		in.Time = &d.Time
	}
	if d.CategoryID > 0 {
		in.CategoryID = &d.CategoryID
	}
	if d.ToAccountID > 0 {
		in.ToAccountID = &d.ToAccountID
	}
	if d.TemplateID > 0 {
		in.Source = "template"
	}
	return in
}

// handleQuickAdd books one transaction per line of text (or the drafts the
// client already showed and maybe corrected).
func (s *Server) handleQuickAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Text   string        `json:"text"`
		Drafts []quick.Draft `json:"drafts"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	drafts := in.Drafts
	if len(drafts) == 0 {
		c, _ := s.quickContext(ctx)
		for _, line := range splitLines(in.Text) {
			drafts = append(drafts, quick.Parse(line, c))
		}
	}
	if len(drafts) == 0 {
		writeErr(w, http.StatusBadRequest, "nothing to add")
		return
	}
	for i, d := range drafts {
		if d.Amount == nil || *d.Amount == 0 {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("line %d: type an amount", i+1))
			return
		}
	}
	created := []*store.Transaction{}
	for i, d := range drafts {
		ti := draftToIn(d, "quick")
		t, msg, err := s.createTx(ctx, &ti)
		if msg != "" {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("line %d: %s", i+1, msg))
			return
		}
		if err != nil {
			s.fail(w, err, "quick add")
			return
		}
		created = append(created, t)
	}
	writeJSON(w, http.StatusCreated, created)
}
