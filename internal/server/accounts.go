package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// visibleIDs returns the accounts the request's user may see.
func (s *Server) visibleIDs(ctx context.Context) []int64 {
	ids, _ := s.store.VisibleAccountIDs(ctx, actorID(ctx))
	if ids == nil {
		ids = []int64{}
	}
	return ids
}

// canWrite reports whether the user may book on / edit transactions of an account.
func (s *Server) canWrite(ctx context.Context, accountID int64) bool {
	r := s.store.AccountRole(ctx, accountID, actorID(ctx))
	return r == "owner" || r == "editor"
}

func (s *Server) canSee(ctx context.Context, accountID int64) bool {
	return s.store.AccountRole(ctx, accountID, actorID(ctx)) != ""
}

// accountsWithHome lists the user's accounts with balances in the home currency.
func (s *Server) accountsWithHome(ctx context.Context, withArchived bool) ([]store.Account, string, error) {
	accts, err := s.store.ListAccounts(ctx, actorID(ctx), withArchived)
	if err != nil {
		return nil, "", err
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	today := store.Today()
	for i := range accts {
		r, _ := s.rate(ctx, accts[i].Currency, st.HomeCurrency, today)
		accts[i].BalanceHome = currency.Convert(accts[i].Balance, accts[i].Currency, st.HomeCurrency, r)
	}
	return accts, st.HomeCurrency, nil
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accts, _, err := s.accountsWithHome(r.Context(), queryBool(r, "archived"))
	if err != nil {
		s.fail(w, err, "accounts")
		return
	}
	for i := range accts {
		if accts[i].Role == "owner" {
			accts[i].Members, _ = s.store.AccountMembers(r.Context(), accts[i].ID)
		}
	}
	writeJSON(w, http.StatusOK, accts)
}

type accountIn struct {
	Name            *string  `json:"name"`
	Kind            *string  `json:"kind"`
	Currency        *string  `json:"currency"`
	OpeningBalance  *int64   `json:"opening_balance"`
	OpeningDate     *string  `json:"opening_date"`
	Institution     *string  `json:"institution"`
	Number          *string  `json:"number"`
	Color           *string  `json:"color"`
	Icon            *string  `json:"icon"`
	CreditLimit     *int64   `json:"credit_limit"`
	IncludeNetWorth *bool    `json:"include_net_worth"`
	Everyday        *bool    `json:"everyday"`
	Direction       *string  `json:"direction"`
	InterestRate    *float64 `json:"interest_rate"`
	InterestAuto    *bool    `json:"interest_auto"`
	Principal       *int64   `json:"principal"`
	Payment         *int64   `json:"payment"`
	PaymentDay      *int     `json:"payment_day"`
	Archived        *bool    `json:"archived"`
	Notes           *string  `json:"notes"`
}

func (in *accountIn) apply(a *store.Account) string {
	if in.Name != nil {
		a.Name = strings.TrimSpace(*in.Name)
	}
	if in.Kind != nil {
		a.Kind = *in.Kind
	}
	if in.Currency != nil {
		a.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	if in.OpeningBalance != nil {
		a.OpeningBalance = *in.OpeningBalance
	}
	if in.OpeningDate != nil {
		a.OpeningDate = *in.OpeningDate
	}
	if in.Institution != nil {
		a.Institution = strings.TrimSpace(*in.Institution)
	}
	if in.Number != nil {
		a.Number = strings.TrimSpace(*in.Number)
	}
	if in.Color != nil {
		a.Color = *in.Color
	}
	if in.Icon != nil {
		a.Icon = *in.Icon
	}
	if in.CreditLimit != nil {
		a.CreditLimit = *in.CreditLimit
	}
	if in.IncludeNetWorth != nil {
		a.IncludeNetWorth = *in.IncludeNetWorth
	}
	if in.Everyday != nil {
		a.Everyday = *in.Everyday
	}
	if in.Direction != nil {
		a.Direction = *in.Direction
	}
	if in.InterestRate != nil {
		a.InterestRate = *in.InterestRate
	}
	if in.InterestAuto != nil {
		a.InterestAuto = *in.InterestAuto
	}
	if in.Principal != nil {
		a.Principal = *in.Principal
	}
	if in.Payment != nil {
		a.Payment = *in.Payment
	}
	if in.PaymentDay != nil {
		a.PaymentDay = *in.PaymentDay
	}
	if a.Direction != "owed" {
		a.Direction = "owe"
	}
	if a.InterestRate < 0 || a.InterestRate > 100 {
		return "interest rate must be between 0 and 100 %"
	}
	if a.PaymentDay < 0 || a.PaymentDay > 28 {
		return "payment day must be 1–28"
	}
	if in.Archived != nil {
		a.Archived = *in.Archived
	}
	if in.Notes != nil {
		a.Notes = *in.Notes
	}
	switch {
	case a.Name == "":
		return "name is required"
	case !store.Valid(store.AccountKinds, a.Kind):
		return "unknown account kind"
	case !currency.Valid(a.Currency):
		return "unknown currency"
	case !validDate(a.OpeningDate):
		return "opening_date must be YYYY-MM-DD"
	}
	return ""
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var in accountIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(r.Context(), actorID(r.Context()))
	a := &store.Account{OwnerID: actorID(r.Context()), Kind: "checking", Currency: st.HomeCurrency, IncludeNetWorth: true}
	if msg := in.apply(a); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if in.Everyday == nil {
		a.Everyday = a.Kind != "savings" && a.Kind != "investment" && !store.IsLiability(a.Kind)
	}
	if a.Kind == "cash" && a.Icon == "" {
		a.Icon = "💵"
	}
	if err := s.store.CreateAccount(r.Context(), a); err != nil {
		s.fail(w, err, "create account")
		return
	}
	s.store.LogActivity(r.Context(), actorID(r.Context()), "account.create", a.Name)
	a, _ = s.store.GetAccount(r.Context(), a.ID)
	a.Role = "owner"
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	role := s.store.AccountRole(ctx, id, actorID(ctx))
	if role == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	a.Role = role
	if role == "owner" {
		a.Members, _ = s.store.AccountMembers(ctx, id)
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	rt, _ := s.rate(ctx, a.Currency, st.HomeCurrency, store.Today())
	a.BalanceHome = currency.Convert(a.Balance, a.Currency, st.HomeCurrency, rt)
	writeJSON(w, http.StatusOK, a)
}

// accessDenied answers 404 for an account the user cannot see at all (so ids
// cannot be probed) and 403 for one shared with them that only its owner may change.
func accessDenied(w http.ResponseWriter, role, msg string) {
	if role == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeErr(w, http.StatusForbidden, msg)
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if role := s.store.AccountRole(ctx, id, actorID(ctx)); role != "owner" {
		accessDenied(w, role, "only the owner can change this account")
		return
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	oldCur := a.Currency
	beforeFields := acctFields(a)
	var in accountIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(a); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if a.Currency != oldCur && a.TxCount > 0 {
		writeErr(w, http.StatusBadRequest, "the currency of an account with transactions cannot change")
		return
	}
	if err := s.store.UpdateAccount(ctx, a); err != nil {
		s.fail(w, err, "update account")
		return
	}
	a, _ = s.store.GetAccount(ctx, id)
	s.auditAccountUpdate(ctx, beforeFields, acctFields(a), a)
	a.Role = "owner"
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if role := s.store.AccountRole(ctx, id, actorID(ctx)); role != "owner" {
		accessDenied(w, role, "only the owner can delete this account")
		return
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	// receipts of the transactions that go with it
	txs, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: []int64{id}, AccountID: id, Limit: 100000})
	for _, t := range txs {
		if t.AttachmentCount > 0 {
			for _, p := range s.store.AttachmentPaths(ctx, t.ID) {
				s.removeFile(p)
			}
		}
	}
	// this account's own log goes with it; a transfer to or from another
	// account disappears from that account too, so its log says so
	var gone []*txSnap
	for i := range txs {
		if txs[i].Kind == "transfer" && txs[i].ToAccountID != nil {
			if sn := snapTx(&txs[i]); sn != nil {
				var other []int64
				for _, x := range sn.accounts {
					if x != id {
						other = append(other, x)
					}
				}
				sn.accounts, sn.line = other, sn.line+" (account "+a.Name+" deleted)"
				gone = append(gone, sn)
			}
		}
	}
	if err := s.store.DeleteAccount(ctx, id); err != nil {
		s.fail(w, err, "delete account")
		return
	}
	for _, sn := range gone {
		if len(sn.accounts) > 0 {
			s.auditTx(ctx, sn, nil)
		}
	}
	s.store.LogActivity(ctx, actorID(ctx), "account.delete", a.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderAccounts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var mine []int64
	for _, id := range in.IDs {
		if s.store.AccountRole(r.Context(), id, actorID(r.Context())) == "owner" {
			mine = append(mine, id)
		}
	}
	if err := s.store.ReorderAccounts(r.Context(), mine); err != nil {
		s.fail(w, err, "reorder")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAccountMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if role := s.store.AccountRole(ctx, id, actorID(ctx)); role != "owner" {
		accessDenied(w, role, "only the owner can share this account")
		return
	}
	var in struct {
		Members []store.AccountMember `json:"members"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var keep []store.AccountMember
	for _, m := range in.Members {
		if m.UserID != actorID(ctx) {
			keep = append(keep, m)
		}
	}
	oldMembers, _ := s.store.AccountMembers(ctx, id)
	if err := s.store.SetAccountMembers(ctx, id, keep); err != nil {
		s.fail(w, err, "members")
		return
	}
	ms, _ := s.store.AccountMembers(ctx, id)
	if a, err := s.store.GetAccount(ctx, id); err == nil {
		s.auditMembers(ctx, a, oldMembers, ms)
	}
	writeJSON(w, http.StatusOK, ms)
}

// handleAccountHistory returns the end-of-day balance series for a range.
func (s *Server) handleAccountHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if !s.canSee(ctx, id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	to := r.URL.Query().Get("to")
	if to == "" {
		to = store.Today()
	}
	from := r.URL.Query().Get("from")
	if from == "" {
		t, _ := store.ParseDate(to)
		from = t.AddDate(-1, 0, 1).Format("2006-01-02")
	}
	start, days, err := s.store.AccountDailyBalances(ctx, id, from, to)
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	// fill every day so the chart has an even x axis
	out := []store.DayAmount{}
	f, _ := store.ParseDate(from)
	t, _ := store.ParseDate(to)
	bal, j := start, 0
	for d := f; !d.After(t) && len(out) < 3700; d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		for j < len(days) && days[j].Date <= ds {
			bal = days[j].Amount
			j++
		}
		out = append(out, store.DayAmount{Date: ds, Amount: bal})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "start": start, "days": out})
}

// handleReconcile books the difference between the stated and the computed balance.
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if role := s.store.AccountRole(ctx, id, actorID(ctx)); role != "owner" && role != "editor" {
		accessDenied(w, role, "read-only account")
		return
	}
	var in struct {
		Balance int64  `json:"balance"`
		Date    string `json:"date"`
		Note    string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Date == "" {
		in.Date = store.Today()
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	bal, _ := s.store.Balances(ctx, in.Date)
	current := a.OpeningBalance + bal[id].Delta
	diff := in.Balance - current
	if diff == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "difference": 0})
		return
	}
	t := &store.Transaction{Kind: "income", Date: in.Date, Time: time.Now().Format("15:04"), AccountID: id, Amount: diff, Description: "Balance adjustment", Notes: in.Note, Source: "adjustment"}
	if diff < 0 {
		t.Kind, t.Amount = "expense", -diff
	}
	uid := actorID(ctx)
	t.CreatedBy = &uid
	s.fillHome(ctx, t)
	if err := s.store.CreateTransaction(ctx, t); err != nil {
		s.fail(w, err, "adjustment")
		return
	}
	s.store.LogActivity(ctx, uid, "account.reconcile", a.Name)
	if full, err := s.store.GetTransaction(ctx, t.ID); err == nil {
		sign := "+"
		if diff < 0 {
			sign = "−"
		}
		adj := sign + majorString(abs64(diff), a.Currency)
		rctx := withAuditSource(ctx, "reconcile")
		s.auditWrite(rctx, []int64{id}, "account.reconciled", full.ID,
			fmt.Sprintf("%s reconciled %s to %s on %s (adjustment %s)", s.who(rctx).name, a.Name, majorString(in.Balance, a.Currency), in.Date, adj),
			map[string]change{"balance": {From: majorString(current, a.Currency), To: majorString(in.Balance, a.Currency)}, "adjustment": {To: adj}, "date": {To: in.Date}})
		s.emitTx(ctx, "transaction.created", full)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "difference": diff, "transaction_id": t.ID})
}

// handleAccountCategories lists the categories bookings on this account use:
// its owner's, so someone a household account is shared with categorises in
// the same list as the owner.
func (s *Server) handleAccountCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if !s.canSee(ctx, id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	list, err := s.store.ListCategories(ctx, a.OwnerID)
	if err != nil {
		s.fail(w, err, "list categories")
		return
	}
	out := []store.Category{}
	for _, c := range list {
		// the owner keeps categories hidden from sharing to themselves
		if c.ShareHidden && a.OwnerID != actorID(ctx) {
			continue
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
