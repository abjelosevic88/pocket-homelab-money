package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// Moving in (Pocket Move, docs/move.md in pocket-homelab-core): the two
// imports below run as the person the request is for, also on a pcx_ link
// token, and only ever add. A second run of the same file adds nothing.

// moveMu runs one moving-in import at a time, so two runs of the same file
// cannot both miss the other's rows.
var moveMu sync.Mutex

// bankCSVAccount is where a statement goes when Move names no account.
const bankCSVAccount = "Moved in"

// handleImportBankCSV books a bank statement without a person mapping the
// columns: the preview's guess must be sure of the date, the amount (or
// debit/credit) and the description, else it answers 422 needs_mapping.
func (s *Server) handleImportBankCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, _, _, err := s.readUpload(r, "file")
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "no file")
		return
	}
	recs, d, err := readCSV(data, "")
	if err != nil || len(recs) < 2 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "could not read any rows from the CSV", "needs_mapping": true})
		return
	}
	m, why := autoMapping(recs)
	m.Delimiter = map[rune]string{',': "comma", ';': "semicolon", '\t': "tab"}[d]
	if why != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": why, "needs_mapping": true, "header": recs[0], "mapping": m})
		return
	}
	moveMu.Lock()
	defer moveMu.Unlock()
	name := strings.TrimSpace(r.FormValue("account"))
	if name == "" {
		name = bankCSVAccount
	}
	acct, made, err := s.bookingAccount(ctx, name, statementCurrency(recs[1:], m))
	if err != nil {
		s.fail(w, err, "import bank csv")
		return
	}
	res := s.importCSVRows(ctx, recs[1:], m, acct, false)
	var problems []csvRow
	for _, row := range res.rows {
		if row.Status == "error" && len(problems) < 20 {
			problems = append(problems, row)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": res.created, "skipped": res.dupes, "errors": res.bad,
		"account": acct.Name, "account_id": acct.ID, "account_created": made, "mapping": m, "problems": problems})
}

// autoMapping guesses the columns from the header and checks the guess on
// the rows: it says why when it is not sure enough to book anything.
func autoMapping(recs [][]string) (CSVMapping, string) {
	m := guessMapping(recs[0])
	switch {
	case m.Date < 0:
		return m, "could not tell which column holds the date"
	case m.Amount < 0 && m.Debit < 0 && m.Credit < 0:
		return m, "could not tell which column holds the amount"
	case m.Description < 0:
		return m, "could not tell which column describes the payment"
	}
	rows := recs[1:]
	dated, best, bestN, tie := 0, "", 0, false
	counts := map[string]int{}
	for _, rec := range rows {
		if cell(rec, m.Date) != "" {
			dated++
		}
	}
	for _, f := range []string{"ymd", "dmy", "mdy"} {
		for _, rec := range rows {
			if v := cell(rec, m.Date); v != "" {
				if _, ok := parseCSVDate(v, f); ok {
					counts[f]++
				}
			}
		}
		switch n := counts[f]; {
		case n > bestN:
			best, bestN, tie = f, n, false
		case n == bestN && n > 0:
			tie = true
		}
	}
	if dated == 0 || bestN*10 < dated*9 {
		return m, "the date column does not hold dates the importer understands"
	}
	if tie {
		return m, "dates like 03/04 could be the 3rd of April or the 4th of March"
	}
	m.DateFormat = best
	amounts := 0
	for _, rec := range rows {
		if v, ok := rowAmount(rec, m); ok && v != 0 {
			amounts++
		}
	}
	if amounts*10 < len(rows)*9 {
		return m, "the amount column does not hold amounts on most rows"
	}
	return m, ""
}

// statementCurrency is the first currency the statement names, if it has a
// currency column.
func statementCurrency(rows [][]string, m CSVMapping) string {
	for _, rec := range rows {
		if c := strings.ToUpper(cell(rec, m.Currency)); currency.Valid(c) {
			return c
		}
	}
	return ""
}

// bookingAccount finds the person's account with this name they can book on,
// or opens it (in cur, else their home currency).
func (s *Server) bookingAccount(ctx context.Context, name, cur string) (*store.Account, bool, error) {
	uid := actorID(ctx)
	accts, err := s.store.ListAccounts(ctx, uid, true)
	if err != nil {
		return nil, false, err
	}
	for i := range accts {
		if fold(accts[i].Name) == fold(name) && (accts[i].Role == "owner" || accts[i].Role == "editor") {
			return &accts[i], false, nil
		}
	}
	if cur == "" {
		st, _ := s.store.GetSettings(ctx, uid)
		cur = st.HomeCurrency
	}
	a := &store.Account{OwnerID: uid, Name: name, Kind: "checking", Currency: cur, IncludeNetWorth: true, Everyday: true}
	if err := s.store.CreateAccount(ctx, a); err != nil {
		return nil, false, err
	}
	s.store.LogActivity(ctx, uid, "account.create", a.Name)
	a, err = s.store.GetAccount(ctx, a.ID)
	return a, true, err
}

// moneyExport is what handleExportJSON writes, as far as it comes back.
type moneyExport struct {
	App           string               `json:"app"`
	Accounts      []store.Account      `json:"accounts"`
	Categories    []store.Category     `json:"categories"`
	Transactions  []store.Transaction  `json:"transactions"`
	Subscriptions []store.Subscription `json:"subscriptions"`
	Budgets       []store.Budget       `json:"budgets"`

	Goals         []json.RawMessage `json:"goals"`
	Templates     []json.RawMessage `json:"templates"`
	Rules         []json.RawMessage `json:"rules"`
	Receipts      []json.RawMessage `json:"receipts"`
	Merchants     []json.RawMessage `json:"merchants"`
	ProductGroups []json.RawMessage `json:"product_groups"`
}

// handleImportMoneyJSON brings back a money.json from Export: accounts and
// categories by name, then transactions, subscriptions and budgets. Nothing
// the person has is changed; what is there already is skipped.
func (s *Server) handleImportMoneyJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, _, _, err := s.readUpload(r, "file")
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "no file")
		return
	}
	var in moneyExport
	if err := json.Unmarshal(data, &in); err != nil || in.App != "pocket-money" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Money export (money.json)")
		return
	}
	moveMu.Lock()
	defer moveMu.Unlock()
	writeJSON(w, http.StatusOK, s.restoreMoney(ctx, &in))
}

func (s *Server) restoreMoney(ctx context.Context, in *moneyExport) map[string]any {
	uid := actorID(ctx)
	out := map[string]any{}
	var problems []string
	problem := func(format string, a ...any) {
		if len(problems) < 20 {
			problems = append(problems, fmt.Sprintf(format, a...))
		}
	}

	// accounts: the person's own by name, else a new one
	acctMap := map[int64]*store.Account{}
	srcAcctName := map[int64]string{}
	accountsMade := 0
	have, _ := s.store.ListAccounts(ctx, uid, true)
	for _, a := range in.Accounts {
		srcAcctName[a.ID] = a.Name
		var found *store.Account
		for i := range have {
			if fold(have[i].Name) == fold(a.Name) && (have[i].Role == "owner" || have[i].Role == "editor") {
				found = &have[i]
				break
			}
		}
		if found != nil {
			acctMap[a.ID] = found
			continue
		}
		if strings.TrimSpace(a.Name) == "" || !currency.Valid(a.Currency) {
			problem("account %q: no name or an unknown currency", a.Name)
			continue
		}
		n := &store.Account{OwnerID: uid, Name: strings.TrimSpace(a.Name), Kind: a.Kind, Currency: a.Currency, OpeningBalance: a.OpeningBalance,
			OpeningDate: a.OpeningDate, Institution: a.Institution, Number: a.Number, Color: a.Color, Icon: a.Icon, CreditLimit: a.CreditLimit,
			IncludeNetWorth: a.IncludeNetWorth, Everyday: a.Everyday, Direction: a.Direction, InterestRate: a.InterestRate, InterestAuto: a.InterestAuto,
			Principal: a.Principal, Payment: a.Payment, PaymentDay: a.PaymentDay, InterestBooked: a.InterestBooked, Archived: a.Archived, Notes: a.Notes}
		if !store.Valid(store.AccountKinds, n.Kind) {
			n.Kind = "checking"
		}
		if err := s.store.CreateAccount(ctx, n); err != nil {
			problem("account %q: %v", a.Name, err)
			continue
		}
		n.Role = "owner"
		acctMap[a.ID] = n
		have = append(have, *n)
		accountsMade++
	}

	// categories: by parent and name, parents first
	catMap := map[int64]int64{}
	srcCat := map[int64]store.Category{}
	for _, c := range in.Categories {
		srcCat[c.ID] = c
	}
	cats := append([]store.Category{}, in.Categories...)
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].ParentID == nil && cats[j].ParentID != nil })
	mine, _ := s.store.ListCategories(ctx, uid)
	catKey := func(parent int64, name string) string {
		return fmt.Sprintf("%d|%s", parent, fold(strings.TrimSpace(name)))
	}
	byKey := map[string]int64{}
	for _, c := range mine {
		var p int64
		if c.ParentID != nil {
			p = *c.ParentID
		}
		byKey[catKey(p, c.Name)] = c.ID
	}
	categoriesMade := 0
	for _, c := range cats {
		var parent int64
		if c.ParentID != nil {
			p, ok := catMap[*c.ParentID]
			if !ok {
				problem("category %q: its parent did not come over", c.Name)
				continue
			}
			parent = p
		}
		if id, ok := byKey[catKey(parent, c.Name)]; ok {
			catMap[c.ID] = id
			continue
		}
		n := &store.Category{UserID: uid, Name: strings.TrimSpace(c.Name), Kind: c.Kind, Icon: c.Icon, Color: c.Color, Archived: c.Archived, SortOrder: c.SortOrder, ShareHidden: c.ShareHidden}
		if parent > 0 {
			n.ParentID = &parent
		}
		if err := s.store.CreateCategory(ctx, n); err != nil {
			problem("category %q: %v", c.Name, err)
			continue
		}
		catMap[c.ID] = n.ID
		byKey[catKey(parent, c.Name)] = n.ID
		categoriesMade++
	}
	mapCat := func(id *int64) *int64 {
		if id == nil {
			return nil
		}
		if v, ok := catMap[*id]; ok {
			return &v
		}
		return nil
	}

	// transactions
	txMade, txSkipped, txBad := 0, 0, 0
	counts := map[int64]*importCounts{}
	for i := range in.Transactions {
		t := &in.Transactions[i]
		acct := acctMap[t.AccountID]
		if acct == nil {
			txBad++
			problem("transaction %s %q: its account did not come over", t.Date, t.Description)
			continue
		}
		h := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%d|%s", t.ID, t.Date, t.Amount, fold(srcAcctName[t.AccountID]))))
		ext := "pocket:" + hex.EncodeToString(h[:8])
		if s.hasTransaction(ctx, acct, t, ext) {
			txSkipped++
			continue
		}
		kind, date, tm, accountID := t.Kind, t.Date, t.Time, acct.ID
		desc, payee, notes := t.Description, t.Payee, t.Notes
		tags := append([]string{}, t.Tags...)
		x := txIn{Kind: &kind, Date: &date, Time: &tm, AccountID: &accountID, Description: &desc, Payee: &payee, Notes: &notes, Tags: &tags,
			CategoryID: mapCat(t.CategoryID), Lat: t.Lat, Lng: t.Lng, Source: "import", keepSource: true, ExternalID: ext, SkipRules: true}
		amount := t.Amount
		if t.Currency == "" || t.Currency == acct.Currency {
			x.AccountAmount = &amount
		} else {
			cur := t.Currency
			x.Amount, x.Currency = &amount, &cur
		}
		if t.ForeignAmount > 0 && t.ForeignCurrency != "" && x.AccountAmount != nil {
			fa, fc := t.ForeignAmount, t.ForeignCurrency
			x.ForeignAmount, x.ForeignCurrency = &fa, &fc
		}
		if t.Kind == "transfer" {
			to := acctMap[derefID(t.ToAccountID)]
			if to == nil {
				txBad++
				problem("transfer %s %q: its destination account did not come over", t.Date, t.Description)
				continue
			}
			toID := to.ID
			x.ToAccountID = &toID
			if t.ToAmount > 0 && (t.ToCurrency == "" || t.ToCurrency == to.Currency) {
				ta := t.ToAmount
				x.ToAmount = &ta
			}
		}
		if len(t.Splits) > 0 && t.Kind != "transfer" {
			parts := []splitIn{}
			for _, sp := range t.Splits {
				a := sp.Amount
				if t.ForeignAmount > 0 && x.ForeignAmount != nil {
					a = sp.ForeignAmount
				}
				parts = append(parts, splitIn{CategoryID: mapCat(sp.CategoryID), Amount: a, Note: sp.Note})
			}
			x.Splits = &parts
		}
		made, msg, err := s.createTx(withAuditQuiet(ctx), &x)
		if msg != "" || err != nil {
			txBad++
			problem("transaction %s %q: %s", t.Date, t.Description, firstNonEmpty(msg, fmt.Sprint(err)))
			continue
		}
		txMade++
		countImport(counts, made, "added")
	}

	// subscriptions: by name and kind
	subsMade := 0
	subs, _ := s.store.ListSubscriptions(ctx, uid)
	haveSub := map[string]bool{}
	for _, x := range subs {
		haveSub[x.Kind+"|"+fold(x.Name)] = true
	}
	for _, x := range in.Subscriptions {
		if strings.TrimSpace(x.Name) == "" || haveSub[x.Kind+"|"+fold(x.Name)] {
			continue
		}
		n := x
		n.ID, n.OwnerID, n.ExternalID, n.CreatedAt, n.UpdatedAt = 0, uid, "", "", ""
		n.CategoryID = mapCat(x.CategoryID)
		n.AccountID, n.ToAccountID = nil, nil
		if a := acctMap[derefID(x.AccountID)]; a != nil {
			n.AccountID = &a.ID
		}
		if a := acctMap[derefID(x.ToAccountID)]; a != nil {
			n.ToAccountID = &a.ID
		}
		if n.Tags == nil {
			n.Tags = []string{}
		}
		if !currency.Valid(n.Currency) {
			problem("subscription %q: unknown currency", x.Name)
			continue
		}
		if err := s.store.CreateSubscription(ctx, &n, s.subSaved(ctx, &n)); err != nil {
			problem("subscription %q: %v", x.Name, err)
			continue
		}
		s.relinkSubscription(ctx, n.ID)
		haveSub[x.Kind+"|"+fold(x.Name)] = true
		subsMade++
	}

	// budgets: by name
	budgetsMade := 0
	budgets, _ := s.store.ListBudgets(ctx, uid)
	haveBudget := map[string]bool{}
	for _, b := range budgets {
		haveBudget[fold(b.Name)] = true
	}
	for _, b := range in.Budgets {
		if strings.TrimSpace(b.Name) == "" || haveBudget[fold(b.Name)] {
			continue
		}
		n := b
		n.ID, n.OwnerID, n.Notified, n.CreatedAt, n.Status = 0, uid, "", "", nil
		n.CategoryIDs = []int64{}
		for _, id := range b.CategoryIDs {
			if v, ok := catMap[id]; ok {
				n.CategoryIDs = append(n.CategoryIDs, v)
			}
		}
		if n.TagNames == nil {
			n.TagNames = []string{}
		}
		if err := s.store.CreateBudget(ctx, &n); err != nil {
			problem("budget %q: %v", b.Name, err)
			continue
		}
		haveBudget[fold(b.Name)] = true
		budgetsMade++
	}

	skipped := []string{}
	for _, k := range []struct {
		name string
		n    int
	}{{"goals", len(in.Goals)}, {"templates", len(in.Templates)}, {"rules", len(in.Rules)}, {"receipts", len(in.Receipts)}, {"merchants", len(in.Merchants)}, {"product_groups", len(in.ProductGroups)}} {
		if k.n > 0 {
			skipped = append(skipped, k.name)
		}
	}
	if txMade > 0 {
		s.store.LogActivity(ctx, uid, "import.money", fmt.Sprintf("%d transactions from money.json", txMade))
		s.auditImports(withAuditSource(ctx, "import"), "money.json import", counts)
	}
	out["imported"] = txMade
	out["skipped"] = txSkipped
	out["errors"] = txBad
	out["accounts_created"] = accountsMade
	out["categories_created"] = categoriesMade
	out["transactions_created"] = txMade
	out["subscriptions_created"] = subsMade
	out["budgets_created"] = budgetsMade
	out["skipped_kinds"] = skipped
	out["problems"] = problems
	return out
}

// hasTransaction reports whether a transaction from money.json is here
// already: brought in before (ext), or the same booking made on this box
// (its own external id, or same account, day, kind, amount and text).
func (s *Server) hasTransaction(ctx context.Context, acct *store.Account, t *store.Transaction, ext string) bool {
	if _, err := s.store.TransactionByExternalID(ctx, acct.OwnerID, ext); err == nil {
		return true
	}
	if t.ExternalID != "" {
		if _, err := s.store.TransactionByExternalID(ctx, acct.OwnerID, t.ExternalID); err == nil {
			return true
		}
	}
	var n int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM transactions WHERE account_id = ? AND date = ? AND kind = ? AND amount = ?
		AND lower(description) = lower(?) AND external_id NOT LIKE 'pocket:%'`, acct.ID, t.Date, t.Kind, t.Amount, t.Description).Scan(&n)
	return n > 0
}

func derefID(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
