package server

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// conditionHolds tests one rule condition against a transaction.
func conditionHolds(c store.Condition, t *store.Transaction) bool {
	var field string
	switch c.Field {
	case "description":
		field = t.Description
	case "payee":
		field = t.Payee
	case "notes":
		field = t.Notes
	case "text":
		field = t.Description + " " + t.Payee + " " + t.Notes
	case "kind":
		return t.Kind == c.Value
	case "account":
		id, _ := strconv.ParseInt(c.Value, 10, 64)
		return t.AccountID == id
	case "currency":
		return strings.EqualFold(t.ForeignCurrency, c.Value) || (t.ForeignCurrency == "" && strings.EqualFold(t.Currency, c.Value))
	case "amount":
		dec := currency.Decimals(t.Currency)
		v := float64(t.Amount)
		parse := func(s string) float64 {
			f, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
			return f * pow10f(dec)
		}
		switch c.Op {
		case "gt":
			return v > parse(c.Value)
		case "lt":
			return v < parse(c.Value)
		case "between":
			return v >= parse(c.Value) && v <= parse(c.Value2)
		case "equals":
			return v == parse(c.Value)
		}
		return false
	default:
		return false
	}
	h, n := fold(field), fold(strings.TrimSpace(c.Value))
	switch c.Op {
	case "equals":
		return strings.TrimSpace(h) == n
	case "starts":
		return strings.HasPrefix(strings.TrimSpace(h), n)
	case "ends":
		return strings.HasSuffix(strings.TrimSpace(h), n)
	case "regex":
		re, err := regexp.Compile("(?i)" + c.Value)
		return err == nil && re.MatchString(field)
	case "not_contains":
		return !strings.Contains(h, n)
	default: // contains
		return n != "" && strings.Contains(h, n)
	}
}

func ruleMatches(r *store.Rule, t *store.Transaction) bool {
	if len(r.Conditions) == 0 {
		return false
	}
	any := r.Match == "any"
	for _, c := range r.Conditions {
		ok := conditionHolds(c, t)
		if any && ok {
			return true
		}
		if !any && !ok {
			return false
		}
	}
	return !any
}

// applyActions changes t; fill-only for category and payee unless force.
func applyActions(a store.RuleActions, t *store.Transaction, force bool) bool {
	changed := false
	if a.CategoryID != nil && t.Kind != "transfer" && len(t.Splits) == 0 && (t.CategoryID == nil || force) {
		if t.CategoryID == nil || *t.CategoryID != *a.CategoryID {
			id := *a.CategoryID
			t.CategoryID = &id
			changed = true
		}
	}
	if len(a.AddTags) > 0 {
		before := len(t.Tags)
		t.Tags = cleanTags(append(t.Tags, a.AddTags...))
		changed = changed || len(t.Tags) != before
	}
	if a.Payee != "" && (t.Payee == "" || force) && t.Payee != a.Payee {
		t.Payee = a.Payee
		changed = true
	}
	if a.Description != "" && t.Description != a.Description {
		t.Description = a.Description
		changed = true
	}
	if a.AppendNote != "" && !strings.Contains(t.Notes, a.AppendNote) {
		t.Notes = strings.TrimSpace(t.Notes + "\n" + a.AppendNote)
		changed = true
	}
	return changed
}

// applyRules runs the user's active rules over a new transaction.
func (s *Server) applyRules(ctx context.Context, t *store.Transaction, userID int64) {
	rules, err := s.store.ListRules(ctx, userID)
	if err != nil {
		return
	}
	for i := range rules {
		r := &rules[i]
		if !r.Active || !ruleMatches(r, t) {
			continue
		}
		if applyActions(r.Actions, t, false) {
			s.store.RuleHit(ctx, r.ID, 1)
		}
		if r.Stop {
			break
		}
	}
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListRules(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "rules")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func validRule(rule *store.Rule) string {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Match != "any" {
		rule.Match = "all"
	}
	if len(rule.Conditions) == 0 {
		return "add at least one condition"
	}
	for _, c := range rule.Conditions {
		if c.Op == "regex" {
			if _, err := regexp.Compile(c.Value); err != nil {
				return "invalid regular expression: " + err.Error()
			}
		}
	}
	a := rule.Actions
	if a.CategoryID == nil && len(a.AddTags) == 0 && a.Payee == "" && a.Description == "" && a.AppendNote == "" {
		return "add at least one action"
	}
	rule.Actions.AddTags = cleanTags(a.AddTags)
	if rule.Name == "" {
		rule.Name = rule.Conditions[0].Field + " " + rule.Conditions[0].Op + " " + rule.Conditions[0].Value
	}
	return ""
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var rule store.Rule
	if err := decode(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	rule.OwnerID = actorID(r.Context())
	rule.ID = 0
	if msg := validRule(&rule); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if c := rule.Actions.CategoryID; c != nil && !s.store.OwnsCategories(r.Context(), rule.OwnerID, []int64{*c}) {
		writeErr(w, http.StatusBadRequest, "unknown category")
		return
	}
	if err := s.store.CreateRule(r.Context(), &rule); err != nil {
		s.fail(w, err, "create rule")
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) ownRule(w http.ResponseWriter, r *http.Request) *store.Rule {
	rule, err := s.store.GetRule(r.Context(), idParam(r, "id"))
	if err != nil || rule.OwnerID != actorID(r.Context()) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return rule
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	cur := s.ownRule(w, r)
	if cur == nil {
		return
	}
	var rule store.Rule
	if err := decode(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	rule.ID, rule.OwnerID, rule.Hits, rule.LastHitAt = cur.ID, cur.OwnerID, cur.Hits, cur.LastHitAt
	if rule.SortOrder == 0 {
		rule.SortOrder = cur.SortOrder
	}
	if msg := validRule(&rule); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if c := rule.Actions.CategoryID; c != nil && !s.store.OwnsCategories(r.Context(), rule.OwnerID, []int64{*c}) {
		writeErr(w, http.StatusBadRequest, "unknown category")
		return
	}
	if err := s.store.UpdateRule(r.Context(), &rule); err != nil {
		s.fail(w, err, "update rule")
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	rule := s.ownRule(w, r)
	if rule == nil {
		return
	}
	if err := s.store.DeleteRule(r.Context(), rule.ID); err != nil {
		s.fail(w, err, "delete rule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderRules(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	for i, id := range in.IDs {
		if rule, err := s.store.GetRule(r.Context(), id); err == nil && rule.OwnerID == actorID(r.Context()) {
			rule.SortOrder = i + 1
			_ = s.store.UpdateRule(r.Context(), rule)
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ruleTargets lists recent transactions a rule would touch.
func (s *Server) ruleTargets(ctx context.Context, rule *store.Rule, since string) ([]store.Transaction, error) {
	txs, _, err := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), From: since, Limit: 20000})
	if err != nil {
		return nil, err
	}
	var out []store.Transaction
	for i := range txs {
		if ruleMatches(rule, &txs[i]) && s.canWrite(ctx, txs[i].AccountID) {
			out = append(out, txs[i])
		}
	}
	return out, nil
}

// handleTestRule shows which existing transactions an (unsaved) rule matches.
func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	var rule store.Rule
	if err := decode(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	since := time.Now().AddDate(-1, 0, 0).Format("2006-01-02")
	txs, err := s.ruleTargets(r.Context(), &rule, since)
	if err != nil {
		s.fail(w, err, "test rule")
		return
	}
	n := len(txs)
	if len(txs) > 50 {
		txs = txs[:50]
	}
	if txs == nil {
		txs = []store.Transaction{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n, "items": txs})
}

// handleApplyRule runs a saved rule over existing transactions.
func (s *Server) handleApplyRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rule := s.ownRule(w, r)
	if rule == nil {
		return
	}
	var in struct {
		Since string `json:"since"`
		Force bool   `json:"force"` // overwrite categories already set
	}
	_ = decode(r, &in)
	if in.Since == "" {
		in.Since = "1900-01-01"
	}
	txs, err := s.ruleTargets(ctx, rule, in.Since)
	if err != nil {
		s.fail(w, err, "apply rule")
		return
	}
	n := 0
	for i := range txs {
		t := &txs[i]
		if applyActions(rule.Actions, t, in.Force) {
			if err := s.store.UpdateTransaction(ctx, actorID(ctx), t); err == nil {
				n++
				if full, err := s.store.GetTransaction(ctx, t.ID); err == nil {
					s.emitTx(ctx, "transaction.updated", full)
				}
			}
		}
	}
	if n > 0 {
		s.store.RuleHit(ctx, rule.ID, n)
	}
	writeJSON(w, http.StatusOK, map[string]int{"changed": n, "matched": len(txs)})
}
