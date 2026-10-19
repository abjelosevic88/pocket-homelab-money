package server

// The per-account audit log: who added, changed or deleted what on an
// account — the owner, someone it is shared with, an API token, the phone app
// through the Firefly-compatible API or a background job. Only the account's
// owner reads it. Entries are written through audit* helpers here, called at
// every write path; they carry names, not ids, so the log stays readable
// after categories or accounts are renamed or deleted.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// principal is who made a request and how they came in.
type principal struct {
	User   *store.User
	Kind   string // session | token | core | proxy | none
	Via    string // "API token 'Homepage widget'", "Pocket Core device 'iPhone'"
	Source string // web | api | firefly_api
}

const (
	principalKey   ctxKey = 2
	auditJobKey    ctxKey = 3
	auditSourceKey ctxKey = 4
	auditQuietKey  ctxKey = 5
)

type auditJob struct{ label, source string }

func withPrincipal(ctx context.Context, p *principal) context.Context {
	return context.WithValue(context.WithValue(ctx, userKey, p.User), principalKey, p)
}

// withAuditJob marks background work: entries are written in the job's name.
func withAuditJob(ctx context.Context, label, source string) context.Context {
	return context.WithValue(ctx, auditJobKey, auditJob{label, source})
}

// withAuditSource overrides where an entry says the change came from (bulk,
// import, reconcile…) while keeping who made it.
func withAuditSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, auditSourceKey, source)
}

// withAuditQuiet suppresses per-transaction entries (imports write one summary).
func withAuditQuiet(ctx context.Context) context.Context {
	return context.WithValue(ctx, auditQuietKey, true)
}

func auditQuiet(ctx context.Context) bool {
	q, _ := ctx.Value(auditQuietKey).(bool)
	return q
}

type auditWho struct {
	userID *int64
	name   string // short, for summaries: "Sani", "Firefly mirror"
	label  string // full: "Sani via API token 'phone'"
	source string
}

func personName(u *store.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// who resolves the actor of a change from the context: a background job's
// label, else the person who made the request (not the ledger owner a job
// may act as), else the user in the context.
func (s *Server) who(ctx context.Context) auditWho {
	var w auditWho
	if j, ok := ctx.Value(auditJobKey).(auditJob); ok {
		w = auditWho{name: j.label, label: j.label, source: j.source}
	} else if p, ok := ctx.Value(principalKey).(*principal); ok && p.User != nil {
		id := p.User.ID
		w = auditWho{userID: &id, name: personName(p.User), source: p.Source}
		w.label = w.name
		if p.Via != "" {
			w.label += " via " + p.Via
		}
	} else if u := userFrom(ctx); u != nil {
		id := u.ID
		w = auditWho{userID: &id, name: personName(u), label: personName(u), source: "web"}
	} else {
		w = auditWho{name: "System", label: "System", source: "schedule"}
	}
	if w.source == "" {
		w.source = "web"
	}
	if src, ok := ctx.Value(auditSourceKey).(string); ok && src != "" {
		w.source = src
	}
	return w
}

// change is one field's before and after ("" = none).
type change struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// auditWrite stores one entry per account (best effort: a failed audit
// write is logged, never fails the change itself).
func (s *Server) auditWrite(ctx context.Context, accounts []int64, action string, txID int64, summary string, changes map[string]change) {
	w := s.who(ctx)
	if changes == nil {
		changes = map[string]change{}
	}
	raw, _ := json.Marshal(changes)
	var tid *int64
	if txID > 0 {
		tid = &txID
	}
	seen := map[int64]bool{}
	for _, a := range accounts {
		if a <= 0 || seen[a] {
			continue
		}
		seen[a] = true
		e := &store.AuditEntry{AccountID: a, ActorUserID: w.userID, ActorLabel: w.label, Source: w.source, Action: action, TransactionID: tid, Summary: summary, Changes: raw}
		if err := s.store.AddAudit(ctx, e); err != nil {
			s.log.Warn("audit", "action", action, "account", a, "err", err)
		}
	}
}

// ---- transactions ----

// txSnap is what the log keeps of a transaction at one moment, formatted
// right away so later changes to the struct cannot leak in.
type txSnap struct {
	id       int64
	accounts []int64
	fields   map[string]string
	line     string // "−25.00 BAM Groceries 'Konzum'"
}

var txFieldOrder = []string{"date", "kind", "amount", "foreign_amount", "account", "to_account", "to_amount", "category", "description", "payee", "tags", "notes", "splits"}

var fieldLabel = map[string]string{
	"date": "date", "kind": "kind", "amount": "amount", "foreign_amount": "foreign amount", "account": "account", "to_account": "to account",
	"to_amount": "received", "category": "category", "description": "description", "payee": "payee", "tags": "tags", "notes": "notes", "splits": "splits",
}

func snapTx(t *store.Transaction) *txSnap {
	if t == nil {
		return nil
	}
	f := map[string]string{
		"date": t.Date, "kind": t.Kind, "amount": majorString(t.Amount, t.Currency), "account": t.AccountName,
		"description": t.Description, "payee": t.Payee, "tags": strings.Join(t.Tags, ", "), "notes": t.Notes,
	}
	if t.ForeignAmount > 0 && t.ForeignCurrency != "" {
		f["foreign_amount"] = majorString(t.ForeignAmount, t.ForeignCurrency)
	}
	if t.Kind == "transfer" {
		f["to_account"] = t.ToAccountName
		if t.ToCurrency != "" && t.ToCurrency != t.Currency {
			f["to_amount"] = majorString(t.ToAmount, t.ToCurrency)
		}
	} else if len(t.Splits) == 0 {
		f["category"] = t.CategoryName
	}
	if len(t.Splits) > 0 {
		parts := make([]string, 0, len(t.Splits))
		for _, sp := range t.Splits {
			amt, cur := sp.Amount, t.Currency
			if sp.ForeignAmount > 0 && t.ForeignCurrency != "" {
				amt, cur = sp.ForeignAmount, t.ForeignCurrency
			}
			p := firstNonEmpty(sp.CategoryName, "Uncategorised") + " " + majorPlain(amt, cur)
			if sp.Note != "" {
				p += " (" + sp.Note + ")"
			}
			parts = append(parts, p)
		}
		f["splits"] = strings.Join(parts, "; ")
	}
	for k, v := range f {
		if v == "" {
			delete(f, k)
		}
	}
	accts := []int64{t.AccountID}
	if t.ToAccountID != nil {
		accts = append(accts, *t.ToAccountID)
	}
	return &txSnap{id: t.ID, accounts: accts, fields: f, line: txLine(t)}
}

// txLine is the short human description of a transaction.
func txLine(t *store.Transaction) string {
	var b strings.Builder
	switch t.Kind {
	case "expense":
		b.WriteString("−" + majorString(t.Amount, t.Currency))
	case "income":
		b.WriteString("+" + majorString(t.Amount, t.Currency))
	default:
		b.WriteString(majorString(t.Amount, t.Currency))
		if t.AccountName != "" || t.ToAccountName != "" {
			b.WriteString(" " + firstNonEmpty(t.AccountName, "?") + " → " + firstNonEmpty(t.ToAccountName, "?"))
		}
	}
	if t.Kind != "transfer" {
		if len(t.Splits) > 0 {
			b.WriteString(" split")
		} else if t.CategoryName != "" {
			b.WriteString(" " + t.CategoryName)
		}
	}
	if text := firstNonEmpty(t.Description, t.Payee); text != "" {
		b.WriteString(" '" + text + "'")
	}
	return b.String()
}

// auditTx records a created (before nil), deleted (after nil) or updated
// transaction on every account it touches, before and after. An update that
// changed nothing meaningful writes nothing.
func (s *Server) auditTx(ctx context.Context, before, after *txSnap) {
	if auditQuiet(ctx) || (before == nil && after == nil) {
		return
	}
	who := s.who(ctx)
	changes := map[string]change{}
	var action, summary string
	var accounts []int64
	var id int64
	switch {
	case before == nil:
		action, id, accounts = "transaction.created", after.id, after.accounts
		for k, v := range after.fields {
			changes[k] = change{To: v}
		}
		summary = who.name + " added " + after.line
	case after == nil:
		action, id, accounts = "transaction.deleted", before.id, before.accounts
		for k, v := range before.fields {
			changes[k] = change{From: v}
		}
		summary = who.name + " deleted " + before.line
	default:
		action, id = "transaction.updated", after.id
		accounts = append(append([]int64{}, before.accounts...), after.accounts...)
		var what []string
		for _, k := range txFieldOrder {
			if before.fields[k] != after.fields[k] {
				changes[k] = change{From: before.fields[k], To: after.fields[k]}
				what = append(what, fieldLabel[k])
			}
		}
		if len(changes) == 0 {
			return
		}
		summary = who.name + " edited " + after.line + ": " + strings.Join(what, ", ")
	}
	s.auditWrite(ctx, accounts, action, id, summary, changes)
}

// auditAttachment records a receipt added to or removed from a transaction
// (the file name only, never its contents).
func (s *Server) auditAttachment(ctx context.Context, t *store.Transaction, name string, added bool) {
	sn := snapTx(t)
	if sn == nil {
		return
	}
	who := s.who(ctx)
	if added {
		s.auditWrite(ctx, sn.accounts, "attachment.added", t.ID, fmt.Sprintf("%s attached '%s' to %s", who.name, name, sn.line), map[string]change{"file": {To: name}})
		return
	}
	s.auditWrite(ctx, sn.accounts, "attachment.deleted", t.ID, fmt.Sprintf("%s removed '%s' from %s", who.name, name, sn.line), map[string]change{"file": {From: name}})
}

// importCounts tallies an import or mirror run per account.
type importCounts struct{ added, updated, deleted int }

// countImport adds one row's outcome to every account the transaction touches.
func countImport(m map[int64]*importCounts, t *store.Transaction, outcome string) {
	accts := []int64{t.AccountID}
	if t.ToAccountID != nil {
		accts = append(accts, *t.ToAccountID)
	}
	for _, a := range accts {
		c := m[a]
		if c == nil {
			c = &importCounts{}
			m[a] = c
		}
		switch outcome {
		case "added":
			c.added++
		case "updated":
			c.updated++
		case "deleted":
			c.deleted++
		}
	}
}

// auditImports writes ONE summary entry per account for an import or mirror
// run instead of one per row.
func (s *Server) auditImports(ctx context.Context, what string, counts map[int64]*importCounts) {
	who := s.who(ctx)
	for acct, c := range counts {
		if c == nil || c.added+c.updated+c.deleted == 0 {
			continue
		}
		var parts []string
		ch := map[string]change{}
		if c.added > 0 {
			parts = append(parts, fmt.Sprintf("%d added", c.added))
			ch["added"] = change{To: strconv.Itoa(c.added)}
		}
		if c.updated > 0 {
			parts = append(parts, fmt.Sprintf("%d updated", c.updated))
			ch["updated"] = change{To: strconv.Itoa(c.updated)}
		}
		if c.deleted > 0 {
			parts = append(parts, fmt.Sprintf("%d deleted", c.deleted))
			ch["deleted"] = change{To: strconv.Itoa(c.deleted)}
		}
		summary := who.name + ": " + strings.Join(parts, ", ")
		if what != "" && what != who.name {
			summary = who.name + " · " + what + ": " + strings.Join(parts, ", ")
		}
		s.auditWrite(ctx, []int64{acct}, "import.summary", 0, summary, ch)
	}
}

// ---- accounts ----

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var acctFieldOrder = []string{"name", "kind", "currency", "opening_balance", "opening_date", "institution", "number", "credit_limit", "include_net_worth", "everyday",
	"direction", "interest_rate", "interest_auto", "principal", "payment", "payment_day", "icon", "color", "archived", "notes"}

var acctFieldLabel = map[string]string{"opening_balance": "opening balance", "opening_date": "opening date", "institution": "bank", "credit_limit": "credit limit",
	"include_net_worth": "net worth", "interest_rate": "interest rate", "interest_auto": "auto interest", "payment_day": "payment day"}

func acctFields(a *store.Account) map[string]string {
	f := map[string]string{
		"name": a.Name, "kind": a.Kind, "currency": a.Currency, "opening_balance": majorString(a.OpeningBalance, a.Currency), "opening_date": a.OpeningDate,
		"institution": a.Institution, "number": a.Number, "include_net_worth": yesNo(a.IncludeNetWorth), "everyday": yesNo(a.Everyday),
		"icon": a.Icon, "color": a.Color, "archived": yesNo(a.Archived), "notes": a.Notes,
	}
	if a.Kind == "credit" || a.CreditLimit != 0 {
		f["credit_limit"] = majorString(a.CreditLimit, a.Currency)
	}
	if store.IsLiability(a.Kind) {
		f["direction"] = a.Direction
		f["interest_rate"] = strconv.FormatFloat(a.InterestRate, 'f', -1, 64) + " %"
		f["interest_auto"] = yesNo(a.InterestAuto)
		f["principal"] = majorString(a.Principal, a.Currency)
		f["payment"] = majorString(a.Payment, a.Currency)
		f["payment_day"] = strconv.Itoa(a.PaymentDay)
	}
	return f
}

// auditAccountUpdate records what the owner changed on an account.
func (s *Server) auditAccountUpdate(ctx context.Context, before, after map[string]string, a *store.Account) {
	changes := map[string]change{}
	var what []string
	for _, k := range acctFieldOrder {
		if before[k] != after[k] {
			changes[k] = change{From: before[k], To: after[k]}
			what = append(what, firstNonEmpty(acctFieldLabel[k], k))
		}
	}
	if len(changes) == 0 {
		return
	}
	s.auditWrite(ctx, []int64{a.ID}, "account.updated", 0, fmt.Sprintf("%s changed %s: %s", s.who(ctx).name, a.Name, strings.Join(what, ", ")), changes)
}

func roleWords(r string) string {
	if r == "viewer" {
		return "can see"
	}
	return "can book"
}

func memberName(m store.AccountMember) string { return firstNonEmpty(m.Name, m.Email) }

// auditMembers records who an account was shared with, unshared from, or
// whose access changed.
func (s *Server) auditMembers(ctx context.Context, a *store.Account, before, after []store.AccountMember) {
	who := s.who(ctx).name
	old := map[int64]store.AccountMember{}
	for _, m := range before {
		old[m.UserID] = m
	}
	now := map[int64]bool{}
	for _, m := range after {
		now[m.UserID] = true
		o, had := old[m.UserID]
		switch {
		case !had:
			s.auditWrite(ctx, []int64{a.ID}, "account.shared", 0, fmt.Sprintf("%s shared %s with %s (%s)", who, a.Name, memberName(m), roleWords(m.Role)),
				map[string]change{"member": {To: memberName(m) + " (" + roleWords(m.Role) + ")"}})
		case o.Role != m.Role:
			s.auditWrite(ctx, []int64{a.ID}, "account.shared", 0, fmt.Sprintf("%s changed %s's access to %s: %s", who, memberName(m), a.Name, roleWords(m.Role)),
				map[string]change{"member": {From: memberName(o) + " (" + roleWords(o.Role) + ")", To: memberName(m) + " (" + roleWords(m.Role) + ")"}})
		}
	}
	for _, m := range before {
		if !now[m.UserID] {
			s.auditWrite(ctx, []int64{a.ID}, "account.unshared", 0, fmt.Sprintf("%s stopped sharing %s with %s", who, a.Name, memberName(m)),
				map[string]change{"member": {From: memberName(m) + " (" + roleWords(m.Role) + ")"}})
		}
	}
}

// ---- read API (owner only) ----

func (s *Server) auditFilter(r *http.Request, ids []int64) store.AuditFilter {
	q := r.URL.Query()
	f := store.AuditFilter{AccountIDs: ids, Limit: queryInt(r, "limit"), Action: strings.TrimSpace(q.Get("action"))}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	if a := strings.TrimSpace(q.Get("actor")); a != "" {
		if id, err := strconv.ParseInt(a, 10, 64); err == nil {
			f.ActorUserID = id
		} else {
			f.ActorLabel = a
		}
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	return f
}

func (s *Server) writeAudit(w http.ResponseWriter, r *http.Request, ids []int64) {
	f := s.auditFilter(r, ids)
	items, err := s.store.ListAudit(r.Context(), f)
	if err != nil {
		s.fail(w, err, "audit")
		return
	}
	actors, _ := s.store.AuditActors(r.Context(), ids)
	var next *int64
	if len(items) == f.Limit {
		id := items[len(items)-1].ID
		next = &id
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_before": next, "actors": actors})
}

// handleAccountAudit is one account's log: its owner only. Someone it is
// shared with gets 403, anyone else (admins included) 404.
func (s *Server) handleAccountAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if role := s.store.AccountRole(ctx, id, actorID(ctx)); role != "owner" {
		accessDenied(w, role, "only the owner can read this account's audit log")
		return
	}
	s.writeAudit(w, r, []int64{id})
}

// handleAudit merges the logs of every account the caller owns.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	ids, err := s.store.OwnedAccountIDs(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "audit")
		return
	}
	if a := r.URL.Query().Get("account"); a != "" {
		want, _ := strconv.ParseInt(a, 10, 64)
		var keep []int64
		for _, id := range ids {
			if id == want {
				keep = append(keep, id)
			}
		}
		ids = keep
	}
	s.writeAudit(w, r, ids)
}
