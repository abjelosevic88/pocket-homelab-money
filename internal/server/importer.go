package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/firefly"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

type importOptions struct {
	All          bool   // accounts, categories, tags, subscriptions, goals, templates and transactions
	Transactions bool   // transactions only (mirror)
	Since        string // only transactions on or after this date ("" = all)
	Quiet        bool
}

// ImportResult says what an import did.
type ImportResult struct {
	Started       string   `json:"started"`
	Finished      string   `json:"finished"`
	Accounts      int      `json:"accounts"`
	Categories    int      `json:"categories"`
	Tags          int      `json:"tags"`
	Subscriptions int      `json:"subscriptions"`
	Goals         int      `json:"goals"`
	Templates     int      `json:"templates"`
	Created       int      `json:"created"`
	Updated       int      `json:"updated"`
	Deleted       int      `json:"deleted"`
	Skipped       int      `json:"skipped"`
	Balances      []string `json:"balances"`
	Warnings      []string `json:"warnings"`
	Error         string   `json:"error,omitempty"`
}

// Summary is a one-line description.
func (r *ImportResult) Summary() string {
	return fmt.Sprintf("%d accounts, %d categories, %d tags, %d subscriptions, %d templates; transactions +%d ~%d -%d (skipped %d)",
		r.Accounts, r.Categories, r.Tags, r.Subscriptions, r.Templates, r.Created, r.Updated, r.Deleted, r.Skipped)
}

var (
	lastImportMu sync.Mutex
	lastImport   *ImportResult
	reCurTag     = regexp.MustCompile(`\s*\[currency:[A-Za-z]{3}\]\s*`)
	reCreatedRec = regexp.MustCompile(`(?m)^\s*Created from recurring transaction .*$`)
	reNotify     = regexp.MustCompile(`(?im)^\s*notify:\s*(\d+)`)
)

func cleanFFNotes(n *string) string {
	if n == nil {
		return ""
	}
	s := reCurTag.ReplaceAllString(*n, " ")
	s = reCreatedRec.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func parseMinor(v string, code string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(math.Abs(f) * math.Pow10(currency.Decimals(code))))
}

func parseSigned(v string, code string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * math.Pow10(currency.Decimals(code))))
}

// ffDateTime turns Firefly's RFC 3339 timestamp into a local date and time.
func ffDateTime(v string) (string, string) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		if len(v) >= 10 {
			return v[:10], ""
		}
		return store.Today(), ""
	}
	t = t.In(time.Local)
	hm := t.Format("15:04")
	if hm == "00:00" && t.Second() == 0 {
		hm = ""
	}
	return t.Format("2006-01-02"), hm
}

func ffKind(role, typ string) string {
	switch {
	case typ == "liabilities" || typ == "liability" || typ == "loan" || typ == "debt" || typ == "mortgage":
		return "loan"
	case role == "savingAsset":
		return "savings"
	case role == "cashWalletAsset":
		return "cash"
	case role == "ccAsset":
		return "credit"
	}
	return "checking"
}

// importFirefly pulls a Firefly III (and optionally Firefly Pico) instance in.
// Everything is keyed by the Firefly ids, so running it again updates
// instead of duplicating.
func (s *Server) importFirefly(ctx context.Context, ownerID int64, opt importOptions) *ImportResult {
	res := &ImportResult{Started: store.Now(), Balances: []string{}, Warnings: []string{}}
	if !s.importing.CompareAndSwap(false, true) {
		res.Error = "an import is already running"
		return res
	}
	defer s.importing.Store(false)
	defer func() {
		res.Finished = store.Now()
		if !opt.Quiet || res.Error != "" || res.Created+res.Updated+res.Deleted > 0 {
			lastImportMu.Lock()
			lastImport = res
			lastImportMu.Unlock()
		}
	}()
	owner, err := s.store.GetUser(ctx, ownerID)
	if err != nil {
		res.Error = "no owner"
		return res
	}
	ctx = context.WithValue(ctx, userKey, owner)
	c := s.clients(ctx)
	ff := c.firefly
	if !ff.Configured() {
		res.Error = "Firefly III URL and token are not set"
		return res
	}

	// ---- accounts ----
	ffAccts, err := ff.Accounts(ctx, "2037-12-31") // Firefly refuses dates past 2038-01-17
	if err != nil {
		res.Error = "accounts: " + err.Error()
		return res
	}
	acctByFF := map[string]*store.Account{}
	all, _ := s.store.AllAccounts(ctx)
	for i := range all {
		if strings.HasPrefix(all[i].ExternalID, "firefly:") {
			acctByFF[strings.TrimPrefix(all[i].ExternalID, "firefly:")] = &all[i]
		}
	}
	ffBalance := map[int64]int64{}
	for _, fa := range ffAccts {
		a := acctByFF[fa.ID]
		if a == nil {
			if !opt.All {
				continue
			}
			a = &store.Account{OwnerID: ownerID, Name: fa.Attr.Name, Kind: ffKind(fa.Attr.AccountRole, fa.Attr.Type), Currency: strings.ToUpper(fa.Attr.CurrencyCode),
				IncludeNetWorth: fa.Attr.IncludeNetWorth || fa.Attr.Type == "asset", Archived: !fa.Attr.Active, SortOrder: fa.Attr.Order + 1,
				Number: firstNonEmpty(fa.Attr.IBAN, fa.Attr.AccountNumber), Notes: fa.Attr.Notes, ExternalID: "firefly:" + fa.ID}
			if a.Kind == "cash" {
				a.Icon = "💵"
			}
			if !currency.Valid(a.Currency) {
				a.Currency = c.st.HomeCurrency
			}
			if err := s.store.CreateAccount(ctx, a); err != nil {
				res.Warnings = append(res.Warnings, "account "+fa.Attr.Name+": "+err.Error())
				continue
			}
			acctByFF[fa.ID] = a
			res.Accounts++
		}
		ffBalance[a.ID] = parseSigned(fa.Attr.CurrentBalance, a.Currency)
	}

	// ---- categories & tags ----
	catByName := map[string]int64{}
	catByFF := map[string]int64{}
	if opt.All || opt.Transactions {
		cats, _ := s.store.ListCategories(ctx)
		for _, cat := range cats {
			catByName[strings.ToLower(cat.Name)] = cat.ID
			if strings.HasPrefix(cat.ExternalID, "firefly:") {
				catByFF[strings.TrimPrefix(cat.ExternalID, "firefly:")] = cat.ID
			}
		}
		if ffc, err := ff.Categories(ctx); err == nil {
			for _, fc := range ffc {
				if id, ok := catByName[strings.ToLower(fc.Attr.Name)]; ok {
					catByFF[fc.ID] = id
					continue
				}
				cat := &store.Category{Name: fc.Attr.Name, Kind: "expense", Icon: guessEmoji(fc.Attr.Name), ExternalID: "firefly:" + fc.ID}
				if err := s.store.CreateCategory(ctx, cat); err != nil {
					res.Warnings = append(res.Warnings, "category "+fc.Attr.Name+": "+err.Error())
					continue
				}
				catByName[strings.ToLower(cat.Name)] = cat.ID
				catByFF[fc.ID] = cat.ID
				res.Categories++
			}
		} else {
			res.Warnings = append(res.Warnings, "categories: "+err.Error())
		}
	}
	tagByFF := map[string]string{}
	if fft, err := ff.Tags(ctx); err == nil {
		for _, t := range fft {
			tagByFF[t.ID] = t.Attr.Tag
			if _, err := s.store.GetTagByName(ctx, t.Attr.Tag); err != nil {
				if _, err := s.store.EnsureTag(ctx, t.Attr.Tag); err == nil {
					res.Tags++
				}
			}
		}
	}

	// ---- transactions first pass needs subscriptions to link to ----
	groups, err := ff.Transactions(ctx, opt.Since, "")
	if err != nil {
		res.Error = "transactions: " + err.Error()
		return res
	}
	if opt.All {
		s.importSubscriptions(ctx, ff, ownerID, acctByFF, catByName, groups, res)
		s.importGoals(ctx, ff, ownerID, acctByFF, res)
		if c.st.PicoURL != "" {
			s.importPico(ctx, ff, c.st.PicoURL, ownerID, acctByFF, catByFF, tagByFF, res)
		}
	}
	subByExt := func(prefix string, id *string) *int64 {
		if id == nil || *id == "" || *id == "0" {
			return nil
		}
		if sid, err := s.store.SubscriptionByExternalID(ctx, prefix+*id); err == nil {
			return &sid
		}
		return nil
	}

	seen := map[string]bool{}
	for _, g := range groups {
		for _, sp := range g.Attr.Transactions {
			ext := "firefly:" + sp.JournalID
			seen[ext] = true
			t, why := s.ffSplitToTx(ctx, sp, acctByFF, catByName)
			if t == nil {
				res.Skipped++
				if why != "" && len(res.Warnings) < 40 {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s: %s", sp.Date[:min(10, len(sp.Date))], sp.Description, why))
				}
				continue
			}
			t.ExternalID, t.Source = ext, "firefly"
			uid := ownerID
			t.CreatedBy = &uid
			if sid := subByExt("firefly-rec:", sp.RecurrenceID); sid != nil {
				t.SubscriptionID = sid
			} else if sid := subByExt("firefly-bill:", sp.BillID); sid != nil {
				t.SubscriptionID = sid
			}
			if id, err := s.store.TransactionByExternalID(ctx, ext); err == nil {
				old, err := s.store.GetTransaction(ctx, id)
				if err != nil {
					continue
				}
				t.ID, t.CreatedBy, t.CreatedAt = old.ID, old.CreatedBy, old.CreatedAt
				if t.SubscriptionID == nil {
					t.SubscriptionID = old.SubscriptionID
				}
				if txChanged(old, t) {
					if err := s.store.UpdateTransaction(ctx, t); err == nil {
						res.Updated++
					}
				}
				continue
			}
			if err := s.store.CreateTransaction(ctx, t); err != nil {
				res.Warnings = append(res.Warnings, sp.Description+": "+err.Error())
				continue
			}
			res.Created++
			if !opt.All && t.SubscriptionID != nil {
				s.advanceSubscription(ctx, *t.SubscriptionID, t.Date)
			}
		}
	}
	// gone from Firefly → gone here (only inside the window we looked at)
	local, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: idsOf(acctByFF), From: opt.Since, ExternalPrefix: "firefly:", Limit: 1000000})
	for _, t := range local {
		if !seen[t.ExternalID] && t.Source == "firefly" {
			if err := s.store.DeleteTransaction(ctx, t.ID); err == nil {
				res.Deleted++
			}
		}
	}

	// ---- opening balances: make every balance equal Firefly's ----
	bal, _ := s.store.Balances(ctx, "")
	for _, a := range acctByFF {
		target, ok := ffBalance[a.ID]
		if !ok {
			continue
		}
		native := s.nativeDelta(ctx, a.ID)
		opening := target - (bal[a.ID].Delta - native)
		if opening != a.OpeningBalance {
			a.OpeningBalance = opening
			if a.OpeningDate == "" {
				a.OpeningDate = s.store.FirstTransactionDate(ctx, []int64{a.ID})
			}
			_ = s.store.UpdateAccount(ctx, a)
		}
		res.Balances = append(res.Balances, fmt.Sprintf("%s: %s", a.Name, majorString(target+native, a.Currency)))
	}
	if !opt.Quiet {
		s.store.LogActivity(ctx, ownerID, "import.firefly", res.Summary())
	}
	return res
}

// nativeDelta is the movement on an account from transactions made in Pocket Money itself.
func (s *Server) nativeDelta(ctx context.Context, accountID int64) int64 {
	var d int64
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(x), 0) FROM (
		SELECT CASE kind WHEN 'income' THEN amount ELSE -amount END AS x FROM transactions WHERE account_id = ? AND source != 'firefly'
		UNION ALL SELECT to_amount FROM transactions WHERE to_account_id = ? AND kind = 'transfer' AND source != 'firefly')`, accountID, accountID).Scan(&d)
	return d
}

func idsOf(m map[string]*store.Account) []int64 {
	out := []int64{}
	for _, a := range m {
		out = append(out, a.ID)
	}
	return out
}

func txChanged(a, b *store.Transaction) bool {
	if a.Kind != b.Kind || a.Date != b.Date || a.Time != b.Time || a.AccountID != b.AccountID || a.Amount != b.Amount || a.ToAmount != b.ToAmount ||
		a.ForeignAmount != b.ForeignAmount || a.ForeignCurrency != b.ForeignCurrency || a.Description != b.Description || a.Payee != b.Payee ||
		a.Notes != b.Notes || a.AmountHome != b.AmountHome {
		return true
	}
	eq := func(x, y *int64) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	if !eq(a.ToAccountID, b.ToAccountID) || !eq(a.CategoryID, b.CategoryID) || !eq(a.SubscriptionID, b.SubscriptionID) {
		return true
	}
	return strings.Join(a.Tags, "|") != strings.Join(cleanTags(b.Tags), "|")
}

// ffSplitToTx maps one Firefly split onto a transaction (nil + reason when it cannot).
func (s *Server) ffSplitToTx(ctx context.Context, sp firefly.Split, acctByFF map[string]*store.Account, catByName map[string]int64) (*store.Transaction, string) {
	t := &store.Transaction{Tags: cleanTags(sp.Tags), Description: strings.TrimSpace(sp.Description), Notes: cleanFFNotes(sp.Notes), Lat: sp.Latitude, Lng: sp.Longitude}
	t.Date, t.Time = ffDateTime(sp.Date)
	src, dst := acctByFF[sp.SourceID], acctByFF[sp.DestinationID]
	counter := func(name, typ string) string {
		if name == "" || strings.Contains(strings.ToLower(typ), "cash") || name == "(cash)" || strings.HasPrefix(name, "Initial balance") {
			return ""
		}
		return name
	}
	cur := strings.ToUpper(sp.CurrencyCode)
	switch sp.Type {
	case "withdrawal":
		if src == nil {
			return nil, "source account not imported"
		}
		t.Kind, t.AccountID, t.Payee = "expense", src.ID, counter(sp.DestinationName, sp.DestinationType)
	case "deposit":
		if dst == nil {
			return nil, "destination account not imported"
		}
		t.Kind, t.AccountID, t.Payee = "income", dst.ID, counter(sp.SourceName, sp.SourceType)
	case "transfer":
		if src == nil || dst == nil {
			return nil, "transfer between accounts that were not imported"
		}
		t.Kind, t.AccountID = "transfer", src.ID
		id := dst.ID
		t.ToAccountID = &id
	case "opening balance":
		return nil, "" // folded into the opening balance
	case "reconciliation":
		a := src
		t.Kind = "expense"
		if a == nil {
			a, t.Kind = dst, "income"
		}
		if a == nil {
			return nil, "reconciliation on an unknown account"
		}
		t.AccountID, t.Description, t.Source = a.ID, firstNonEmpty(t.Description, "Reconciliation"), "adjustment"
	default:
		return nil, "unsupported type " + sp.Type
	}
	acct := src
	if t.Kind == "income" {
		acct = dst
	}
	if t.Kind == "expense" && acct == nil {
		acct = dst
	}
	t.Currency = acct.Currency
	t.Amount = parseMinor(sp.Amount, cur)
	if cur != acct.Currency {
		// Firefly booked it in another currency than the account's; keep it as foreign
		r, _ := s.rate(ctx, cur, acct.Currency, t.Date)
		t.ForeignAmount, t.ForeignCurrency = t.Amount, cur
		t.Amount = currency.Convert(t.Amount, cur, acct.Currency, r)
	}
	if sp.ForeignAmount != nil && sp.ForeignCurrency != nil && *sp.ForeignCurrency != "" {
		fc := strings.ToUpper(*sp.ForeignCurrency)
		fa := parseMinor(*sp.ForeignAmount, fc)
		if t.Kind == "transfer" && dst != nil && fc == dst.Currency {
			t.ToAmount = fa
		} else if fc != acct.Currency && fa > 0 {
			t.ForeignAmount, t.ForeignCurrency = fa, fc
		}
	}
	if t.Kind == "transfer" && t.ToAmount == 0 {
		if dst.Currency == acct.Currency {
			t.ToAmount = t.Amount
		} else {
			r, _ := s.rate(ctx, acct.Currency, dst.Currency, t.Date)
			t.ToAmount = currency.Convert(t.Amount, acct.Currency, dst.Currency, r)
		}
	}
	if sp.CategoryName != nil && *sp.CategoryName != "" {
		if id, ok := catByName[strings.ToLower(*sp.CategoryName)]; ok {
			t.CategoryID = &id
		}
	}
	if t.Amount == 0 {
		return nil, "zero amount"
	}
	s.fillHome(ctx, t)
	return t, ""
}

var ffFreq = map[string]struct {
	freq  string
	every int
}{"daily": {"daily", 1}, "weekly": {"weekly", 1}, "monthly": {"monthly", 1}, "quarterly": {"monthly", 3}, "half-year": {"monthly", 6}, "yearly": {"yearly", 1}}

// importSubscriptions merges Firefly bills and recurring transactions with
// the same name into one subscription each.
func (s *Server) importSubscriptions(ctx context.Context, ff *firefly.Client, ownerID int64, acctByFF map[string]*store.Account, catByName map[string]int64, groups []firefly.Item[firefly.Group], res *ImportResult) {
	bills, err := ff.Bills(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "bills: "+err.Error())
	}
	recs, err := ff.Recurrences(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "recurring transactions: "+err.Error())
	}
	st, _ := s.store.GetSettings(ctx)
	type merged struct {
		x   *store.Subscription
		ext []string
	}
	byName := map[string]*merged{}
	var order []string
	get := func(name string) *merged {
		k := strings.ToLower(strings.TrimSpace(name))
		m := byName[k]
		if m == nil {
			m = &merged{x: &store.Subscription{OwnerID: ownerID, Name: strings.TrimSpace(name), Kind: "expense", Currency: st.HomeCurrency, Freq: "monthly", Every: 1, Mode: "track", NotifyDays: -1, Active: true, Tags: []string{}}}
			byName[k] = m
			order = append(order, k)
		}
		return m
	}
	// the latest Firefly transaction of each bill/recurrence fills account/category/tags when the bill alone does not say
	latest := map[string]firefly.Split{}
	for _, g := range groups {
		for _, sp := range g.Attr.Transactions {
			for _, key := range []string{"bill:" + deref(sp.BillID), "rec:" + deref(sp.RecurrenceID)} {
				if strings.HasSuffix(key, ":") {
					continue
				}
				if cur, ok := latest[key]; !ok || sp.Date > cur.Date {
					latest[key] = sp
				}
			}
		}
	}
	for _, b := range bills {
		m := get(b.Attr.Name)
		x := m.x
		m.ext = append(m.ext, "firefly-bill:"+b.ID)
		x.Currency = strings.ToUpper(b.Attr.CurrencyCode)
		x.Amount = parseMinor(b.Attr.AmountMin, x.Currency)
		if mx := parseMinor(b.Attr.AmountMax, x.Currency); mx > x.Amount {
			x.AmountMax = mx
		}
		if f, ok := ffFreq[b.Attr.RepeatFreq]; ok {
			x.Freq, x.Every = f.freq, f.every*(b.Attr.Skip+1)
		}
		x.StartDate, _ = ffDateTime(b.Attr.Date)
		if b.Attr.EndDate != nil {
			x.EndDate, _ = ffDateTime(*b.Attr.EndDate)
		}
		if b.Attr.NextExpected != nil && *b.Attr.NextExpected != "" {
			x.NextDate, _ = ffDateTime(*b.Attr.NextExpected)
		} else if len(b.Attr.PayDates) > 0 {
			x.NextDate, _ = ffDateTime(b.Attr.PayDates[0])
		}
		x.Active = b.Attr.Active
		notes := deref(b.Attr.Notes)
		if mm := reNotify.FindStringSubmatch(notes); mm != nil {
			x.NotifyDays, _ = strconv.Atoi(mm[1])
			x.Mode = "remind"
		}
		x.Notes = strings.TrimSpace(reNotify.ReplaceAllString(notes, ""))
		if sp, ok := latest["bill:"+b.ID]; ok {
			s.fillSubFromSplit(x, sp, acctByFF, catByName)
		}
	}
	for _, rc := range recs {
		m := get(rc.Attr.Title)
		x := m.x
		m.ext = append(m.ext, "firefly-rec:"+rc.ID)
		x.Mode = "auto"
		x.Active = x.Active && rc.Attr.Active
		switch rc.Attr.Type {
		case "deposit":
			x.Kind = "income"
		case "transfer":
			x.Kind = "transfer"
		}
		if len(rc.Attr.Repetitions) > 0 {
			rp := rc.Attr.Repetitions[0]
			switch rp.Type {
			case "daily":
				x.Freq = "daily"
			case "weekly":
				x.Freq = "weekly"
			case "yearly":
				x.Freq = "yearly"
			default:
				x.Freq = "monthly"
			}
			x.Every = rp.Skip + 1
			if len(rp.Upcoming) > 0 {
				x.NextDate, _ = ffDateTime(rp.Upcoming[0])
			}
		}
		first, _ := ffDateTime(rc.Attr.FirstDate)
		if x.StartDate == "" {
			x.StartDate = first
		}
		if rc.Attr.RepeatUntil != nil && *rc.Attr.RepeatUntil != "" {
			x.EndDate, _ = ffDateTime(*rc.Attr.RepeatUntil)
		}
		if len(rc.Attr.Transactions) > 0 {
			rt := rc.Attr.Transactions[0]
			x.Currency = strings.ToUpper(rt.CurrencyCode)
			x.Amount = parseMinor(rt.Amount, x.Currency)
			src, dst := acctByFF[rt.SourceID], acctByFF[rt.DestinationID]
			switch x.Kind {
			case "income":
				if dst != nil {
					x.AccountID = &dst.ID
				}
			case "transfer":
				if src != nil {
					x.AccountID = &src.ID
				}
				if dst != nil {
					x.ToAccountID = &dst.ID
				}
			default:
				if src != nil {
					x.AccountID = &src.ID
				}
				if dst == nil && rt.DestinationName != "" {
					x.Payee = rt.DestinationName
				}
			}
			if rt.CategoryName != nil {
				if id, ok := catByName[strings.ToLower(*rt.CategoryName)]; ok {
					x.CategoryID = &id
				}
			}
			if len(rt.Tags) > 0 {
				x.Tags = cleanTags(rt.Tags)
			}
			if rt.Description != "" && !strings.EqualFold(rt.Description, x.Name) {
				x.Description = rt.Description
			}
		}
		if n := deref(rc.Attr.Notes); n != "" && x.Notes == "" {
			x.Notes = n
		}
	}
	for _, k := range order {
		m := byName[k]
		x := m.x
		ext := strings.Join(m.ext, ",")
		if x.NextDate == "" {
			x.NextDate = x.StartDate
		}
		if x.NextDate == "" || x.Amount == 0 {
			res.Warnings = append(res.Warnings, "subscription "+x.Name+": no amount or date, skipped")
			continue
		}
		x.ExternalID = ext
		if x.Mode == "auto" && x.AccountID == nil {
			x.Mode = "remind"
		}
		if x.Icon == "" && x.CategoryID != nil {
			x.Icon = ""
		}
		found := false
		for _, e := range m.ext {
			if _, err := s.store.SubscriptionByExternalID(ctx, e); err == nil {
				found = true
			}
		}
		if found {
			continue
		}
		if err := s.store.CreateSubscription(ctx, x); err != nil {
			res.Warnings = append(res.Warnings, "subscription "+x.Name+": "+err.Error())
			continue
		}
		res.Subscriptions++
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) fillSubFromSplit(x *store.Subscription, sp firefly.Split, acctByFF map[string]*store.Account, catByName map[string]int64) {
	if a := acctByFF[sp.SourceID]; a != nil && x.AccountID == nil && sp.Type == "withdrawal" {
		x.AccountID = &a.ID
	}
	if a := acctByFF[sp.DestinationID]; a != nil && x.AccountID == nil && sp.Type == "deposit" {
		x.AccountID = &a.ID
		x.Kind = "income"
	}
	if x.CategoryID == nil && sp.CategoryName != nil {
		if id, ok := catByName[strings.ToLower(*sp.CategoryName)]; ok {
			x.CategoryID = &id
		}
	}
	if len(x.Tags) == 0 && len(sp.Tags) > 0 {
		x.Tags = cleanTags(sp.Tags)
	}
}

func (s *Server) importGoals(ctx context.Context, ff *firefly.Client, ownerID int64, acctByFF map[string]*store.Account, res *ImportResult) {
	pbs, err := ff.PiggyBanks(ctx)
	if err != nil {
		return
	}
	existing, _ := s.store.ListGoals(ctx, ownerID)
	have := map[string]bool{}
	for _, g := range existing {
		have[g.ExternalID] = true
	}
	for _, pb := range pbs {
		ext := "firefly-piggy:" + pb.ID
		if have[ext] {
			continue
		}
		cur := strings.ToUpper(pb.Attr.CurrencyCode)
		g := &store.Goal{OwnerID: ownerID, Name: pb.Attr.Name, Target: parseMinor(pb.Attr.TargetAmount, cur), Currency: cur, Icon: "🐷", Notes: deref(pb.Attr.Notes), ExternalID: ext}
		if pb.Attr.TargetDate != nil {
			g.TargetDate, _ = ffDateTime(*pb.Attr.TargetDate)
		}
		if g.Target <= 0 {
			g.Target = 1
		}
		if err := s.store.CreateGoal(ctx, g); err != nil {
			continue
		}
		if saved := parseMinor(pb.Attr.CurrentAmount, cur); saved > 0 {
			_ = s.store.AddGoalEntry(ctx, &store.GoalEntry{GoalID: g.ID, Date: store.Today(), Amount: saved, Note: "Imported from Firefly III"})
		}
		res.Goals++
	}
}

func (s *Server) importPico(ctx context.Context, ff *firefly.Client, picoURL string, ownerID int64, acctByFF map[string]*store.Account, catByFF map[string]int64, tagByFF map[string]string, res *ImportResult) {
	tpls, err := ff.PicoTemplates(ctx, picoURL)
	if err != nil {
		res.Warnings = append(res.Warnings, "Firefly Pico templates: "+err.Error())
		return
	}
	for i, p := range tpls {
		ext := "pico:" + strconv.FormatInt(p.ID, 10)
		if _, err := s.store.TemplateByExternalID(ctx, ext); err == nil {
			continue
		}
		t := &store.Template{OwnerID: ownerID, Name: strings.TrimSpace(p.Name), Kind: "expense", Tags: []string{}, ExternalID: ext, SortOrder: i + 1}
		acct := func(id *int64) *store.Account {
			if id == nil {
				return nil
			}
			return acctByFF[strconv.FormatInt(*id, 10)]
		}
		src, dst := acct(p.AccountSourceID), acct(p.AccountDestinationID)
		switch p.Type {
		case "deposit":
			t.Kind = "income"
			if dst != nil {
				t.AccountID = &dst.ID
			}
		case "transfer":
			t.Kind = "transfer"
			if src != nil {
				t.AccountID = &src.ID
			}
			if dst != nil {
				t.ToAccountID = &dst.ID
			}
		default:
			if src != nil {
				t.AccountID = &src.ID
			}
		}
		if t.Kind == "transfer" && t.ToAccountID == nil {
			t.Kind = "expense"
		}
		notes := deref(p.Notes)
		if mm := regexp.MustCompile(`\[currency:([A-Za-z]{3})\]`).FindStringSubmatch(notes); mm != nil {
			t.Currency = strings.ToUpper(mm[1])
		}
		t.Notes = strings.TrimSpace(reCurTag.ReplaceAllString(notes, " "))
		cur := t.Currency
		if cur == "" {
			if a := acct(p.AccountSourceID); a != nil {
				cur = a.Currency
			} else if a := acct(p.AccountDestinationID); a != nil {
				cur = a.Currency
			}
		}
		if p.Amount != nil && *p.Amount > 0 {
			v := int64(math.Round(*p.Amount * math.Pow10(currency.Decimals(cur))))
			t.Amount = &v
		}
		if p.CategoryID != nil {
			if id, ok := catByFF[strconv.FormatInt(*p.CategoryID, 10)]; ok {
				t.CategoryID = &id
			}
		}
		for _, tg := range p.Tags {
			if name, ok := tagByFF[strconv.FormatInt(tg.TagID, 10)]; ok {
				t.Tags = append(t.Tags, name)
			}
		}
		t.Description = strings.TrimSpace(deref(p.Description))
		if strings.EqualFold(t.Description, t.Name) {
			t.Description = ""
		}
		var aliases []string
		for _, e := range p.ExtraNames {
			aliases = append(aliases, e.Name)
		}
		t.Aliases = strings.Join(aliases, ", ")
		if t.Name == "" {
			continue
		}
		if err := s.store.CreateTemplate(ctx, t); err == nil {
			res.Templates++
		}
	}
}

// ---- handlers ----

func (s *Server) handleImportFirefly(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Mode  string `json:"mode"` // all | transactions
		Since string `json:"since"`
		Wait  bool   `json:"wait"`
	}
	_ = decode(r, &in)
	opt := importOptions{All: in.Mode != "transactions", Transactions: true, Since: in.Since}
	uid := actorID(r.Context())
	if s.importing.Load() {
		writeErr(w, http.StatusConflict, "an import is already running")
		return
	}
	if in.Wait {
		s.jobMu.Lock()
		res := s.importFirefly(context.Background(), uid, opt)
		s.jobMu.Unlock()
		writeJSON(w, http.StatusOK, res)
		return
	}
	go func() {
		s.jobMu.Lock()
		defer s.jobMu.Unlock()
		s.importFirefly(context.Background(), uid, opt)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *Server) handleImportStatus(w http.ResponseWriter, r *http.Request) {
	lastImportMu.Lock()
	defer lastImportMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"running": s.importing.Load(), "last": lastImport})
}
