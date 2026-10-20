package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
		return "loan" // refined from liability_type by the caller
	case role == "savingAsset":
		return "savings"
	case role == "cashWalletAsset":
		return "cash"
	case role == "ccAsset":
		return "credit"
	}
	return "checking"
}

// importFirefly pulls a person's Firefly III (and optionally Firefly Pico)
// in, using that person's own Firefly settings, into that person's ledger
// only: their accounts, categories, tags, subscriptions, goals and templates.
// Everything is keyed by the Firefly ids, so running it again updates
// instead of duplicating.
func (s *Server) importFirefly(ctx context.Context, ownerID int64, opt importOptions) *ImportResult {
	res := &ImportResult{Started: store.Now(), Balances: []string{}, Warnings: []string{}}
	if !s.importing.CompareAndSwap(false, true) {
		res.Error = "an import is already running"
		return res
	}
	s.importingUser.Store(ownerID)
	defer s.importing.Store(false)
	defer s.importingUser.Store(0)
	settings, err := s.store.GetSettings(ctx, ownerID)
	if err != nil {
		res.Error = "could not load Firefly connection"
		return res
	}
	defer func() {
		res.Finished = store.Now()
		// A result belongs to the connection that started it. Disconnecting or
		// replacing that connection must not resurrect an old error later.
		s.lastImportMu.Lock()
		defer s.lastImportMu.Unlock()
		current, err := s.store.GetSettings(ctx, ownerID)
		if err != nil || current.FireflyURL == "" || current.FireflyToken == "" ||
			current.FireflyURL != settings.FireflyURL || current.FireflyToken != settings.FireflyToken {
			return
		}
		if !opt.Quiet || res.Error != "" || res.Created+res.Updated+res.Deleted > 0 {
			s.lastImport[ownerID] = res
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
	all, _ := s.store.ListAccounts(ctx, ownerID, true)
	for i := range all {
		if all[i].OwnerID == ownerID && strings.HasPrefix(all[i].ExternalID, "firefly:") {
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
			if store.IsLiability(a.Kind) {
				if lt := fa.Attr.LiabilityType; lt == "mortgage" || lt == "debt" || lt == "loan" {
					a.Kind = lt
				}
				if fa.Attr.LiabilityDir == "credit" {
					a.Direction = "owed"
				}
				if r, err := strconv.ParseFloat(fa.Attr.Interest, 64); err == nil {
					switch fa.Attr.InterestPeriod {
					case "monthly":
						r *= 12
					case "daily":
						r *= 365
					}
					a.InterestRate = r
				}
				if ob := parseSigned(fa.Attr.OpeningBalance, a.Currency); ob != 0 {
					if ob < 0 {
						ob = -ob
					}
					a.Principal = ob
				}
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
		cats, _ := s.store.ListCategories(ctx, actorID(ctx))
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
				cat := &store.Category{UserID: ownerID, Name: fc.Attr.Name, Kind: "expense", Icon: guessEmoji(fc.Attr.Name), ExternalID: "firefly:" + fc.ID}
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
			if _, err := s.store.GetTagByName(ctx, ownerID, t.Attr.Tag); err != nil {
				if _, err := s.store.EnsureTag(ctx, ownerID, t.Attr.Tag); err == nil {
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
		if sid, err := s.store.SubscriptionByExternalID(ctx, ownerID, prefix+*id); err == nil {
			return &sid
		}
		return nil
	}

	seen := map[string]bool{}
	counts := map[int64]*importCounts{} // the audit log gets one summary per account
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
			if id, err := s.store.TransactionByExternalID(ctx, ownerID, ext); err == nil {
				old, err := s.store.GetTransaction(ctx, id)
				if err != nil {
					continue
				}
				t.ID, t.CreatedBy, t.CreatedAt = old.ID, old.CreatedBy, old.CreatedAt
				// parts made here survive a mirror pass (Firefly only has the total)
				if len(old.Splits) > 0 && old.Amount == t.Amount {
					t.Splits, t.CategoryID = old.Splits, old.CategoryID
				}
				if t.SubscriptionID == nil {
					t.SubscriptionID = old.SubscriptionID
				}
				if txChanged(old, t) {
					if err := s.store.UpdateTransaction(ctx, ownerID, t); err == nil {
						res.Updated++
						countImport(counts, old, "updated")
						if old.AccountID != t.AccountID || (t.ToAccountID != nil && (old.ToAccountID == nil || *old.ToAccountID != *t.ToAccountID)) {
							countImport(counts, t, "updated")
						}
						if !opt.All {
							if full, err := s.store.GetTransaction(ctx, t.ID); err == nil {
								s.emitTx(ctx, "transaction.updated", full)
							}
						}
					}
				}
				continue
			}
			if err := s.store.CreateTransaction(ctx, t); err != nil {
				res.Warnings = append(res.Warnings, sp.Description+": "+err.Error())
				continue
			}
			res.Created++
			countImport(counts, t, "added")
			if !opt.All && t.SubscriptionID != nil {
				s.advanceSubscription(ctx, *t.SubscriptionID, t.Date)
			}
			if !opt.All {
				if full, err := s.store.GetTransaction(ctx, t.ID); err == nil {
					s.emitTx(ctx, "transaction.created", full)
				}
			}
		}
	}
	// gone from Firefly → gone here (only inside the window we looked at)
	local, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: idsOf(acctByFF), From: opt.Since, ExternalPrefix: "firefly:", Limit: 1000000})
	for _, t := range local {
		if !seen[t.ExternalID] && t.Source == "firefly" {
			if err := s.store.DeleteTransaction(ctx, t.ID); err == nil {
				res.Deleted++
				countImport(counts, &t, "deleted")
				s.emitTx(ctx, "transaction.deleted", &t)
			}
		}
	}

	s.auditImports(ctx, "Firefly import", counts)

	if opt.All && res.Accounts > 0 {
		s.guessEveryday(ctx, acctByFF)
	}
	if opt.All {
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE subscriptions SET done_count = (SELECT COUNT(1) FROM transactions t WHERE t.subscription_id = subscriptions.id)
			WHERE max_count > 0 AND done_count = 0 AND owner_id = ?`, ownerID)
	}
	if opt.All && res.Templates > 0 {
		s.seedTemplateUse(ctx, ownerID)
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
	return tagKey(a.Tags) != tagKey(cleanTags(b.Tags))
}

// tagKey compares tag sets regardless of order and case.
func tagKey(tags []string) string {
	l := make([]string, len(tags))
	for i, t := range tags {
		l[i] = strings.ToLower(t)
	}
	sort.Strings(l)
	return strings.Join(l, "|")
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
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
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
			case "ndom":
				// "2,3" = the 2nd Wednesday of every month
				x.Freq = "monthly"
				if parts := strings.Split(rp.Moment, ","); len(parts) == 2 {
					x.Nth, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
				}
			default:
				x.Freq = "monthly"
			}
			x.Every = rp.Skip + 1
			x.Weekend = map[int]string{2: "skip", 3: "before", 4: "after"}[rp.Weekend]
			if len(rp.Upcoming) > 0 {
				x.NextDate, _ = ffDateTime(rp.Upcoming[0])
			}
		}
		first, _ := ffDateTime(rc.Attr.FirstDate)
		if x.StartDate == "" {
			x.StartDate = first
		}
		if rc.Attr.Repeats != nil && *rc.Attr.Repeats > 0 {
			x.MaxCount = *rc.Attr.Repeats
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
			if _, err := s.store.SubscriptionByExternalID(ctx, ownerID, e); err == nil {
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
		if _, err := s.store.TemplateByExternalID(ctx, ownerID, ext); err == nil {
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

// handleImportFirefly imports the caller's own Firefly III (their settings)
// into their own ledger.
func (s *Server) handleImportFirefly(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode  string `json:"mode"` // all | transactions
		Since string `json:"since"`
		Wait  bool   `json:"wait"`
	}
	_ = decode(r, &in)
	opt := importOptions{All: in.Mode != "transactions", Transactions: true, Since: in.Since}
	uid := actorID(r.Context())
	st, err := s.store.GetSettings(r.Context(), uid)
	if err != nil {
		s.fail(w, err, "Firefly connection")
		return
	}
	if st.FireflyURL == "" || st.FireflyToken == "" {
		writeErr(w, http.StatusBadRequest, "Connect Firefly III before importing")
		return
	}
	// outlives the request, keeps who started it for the audit log
	bg := withAuditSource(context.WithoutCancel(r.Context()), "import")
	if s.importing.Load() {
		writeErr(w, http.StatusConflict, "an import is already running")
		return
	}
	if in.Wait {
		s.jobMu.Lock()
		res := s.importFirefly(bg, uid, opt)
		s.jobMu.Unlock()
		writeJSON(w, http.StatusOK, res)
		return
	}
	go func() {
		s.jobMu.Lock()
		defer s.jobMu.Unlock()
		s.importFirefly(bg, uid, opt)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *Server) handleImportStatus(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	s.lastImportMu.Lock()
	defer s.lastImportMu.Unlock()
	st, err := s.store.GetSettings(r.Context(), uid)
	if err != nil {
		s.fail(w, err, "Firefly connection")
		return
	}
	if st.FireflyURL == "" || st.FireflyToken == "" {
		delete(s.lastImport, uid)
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "last": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": s.importing.Load() && s.importingUser.Load() == uid, "last": s.lastImport[uid]})
}

// guessEveryday marks as "everyday money" only the accounts that carry a real
// share of the spending; savings, business or foreign-currency accounts that
// are rarely paid from stay out of safe-to-spend.
func (s *Server) guessEveryday(ctx context.Context, acctByFF map[string]*store.Account) {
	counts := map[int64]int{}
	total := 0
	from := addDays(store.Today(), -120)
	for _, a := range acctByFF {
		var n int
		_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM transactions WHERE account_id = ? AND kind = 'expense' AND date >= ?`, a.ID, from).Scan(&n)
		counts[a.ID] = n
		total += n
	}
	if total == 0 {
		return
	}
	for _, a := range acctByFF {
		every := a.Kind != "savings" && a.Kind != "investment" && !store.IsLiability(a.Kind) && float64(counts[a.ID]) >= 0.2*float64(total)
		if a.Everyday != every {
			a.Everyday = every
			_ = s.store.UpdateAccount(ctx, a)
		}
	}
}

// seedTemplateUse gives a person's imported templates a use count from the
// transactions they would have produced, so the ones used most come first.
func (s *Server) seedTemplateUse(ctx context.Context, ownerID int64) {
	_, _ = s.store.DB.ExecContext(ctx, `UPDATE templates SET use_count = (
		SELECT COUNT(1) FROM transactions t WHERE t.user_id = templates.owner_id AND t.date >= date('now', '-180 days')
		AND lower(t.description) = lower(CASE WHEN templates.description != '' THEN templates.description ELSE templates.name END))
		WHERE use_count = 0 AND owner_id = ?`, ownerID)
}
