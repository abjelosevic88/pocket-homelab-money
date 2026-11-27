package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-money/internal/quick"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// What Pocket Search, Pocket Day and Pocket Hub ask Money (the capabilities
// value in links.go): one route each, in the shape every Pocket app answers
// in. They read what the transaction list, the first page and the bills page
// read, as the request's person and with the same fence: the accounts that
// person owns or was given. Only the capture adds something, one expense,
// through the quick add's own parser and booking.

// txURL opens a transaction in the app (the list with its editor open).
func txURL(id int64) string { return "/transactions/" + strconv.FormatInt(id, 10) }

// txTitle names a transaction the way the list does: its description, else
// the payee, the category, or what kind it is.
func txTitle(t *store.Transaction) string {
	return firstNonEmpty(strings.TrimSpace(t.Description), strings.TrimSpace(t.Payee), t.CategoryName, capFirst(t.Kind))
}

// txAmount is a transaction's amount as the API's texts write it ("12.50
// EUR"), money coming in with a plus, and what was paid in another currency
// after it: "39.12 BAM (20.00 EUR)".
func txAmount(t *store.Transaction) string {
	s := majorString(t.Amount, t.Currency)
	if t.Kind == "income" {
		s = "+" + s
	}
	if t.ForeignAmount > 0 && t.ForeignCurrency != "" && t.ForeignCurrency != t.Currency {
		s += " (" + majorString(t.ForeignAmount, t.ForeignCurrency) + ")"
	}
	return s
}

// txAccounts is where a transaction was booked: the account, or both
// accounts of a transfer.
func txAccounts(t *store.Transaction) string {
	if t.Kind == "transfer" && t.ToAccountName != "" {
		return t.AccountName + " → " + t.ToAccountName
	}
	return t.AccountName
}

// dots joins the parts of a line that are there: "12.50 EUR · Cash · Food".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// ---------- search ----------

// pocketSearch answers Pocket Search with the transaction list's own search
// (description, payee, notes, category, tag and receipt items; every word
// must be found), newest first. Accounts and categories whose name has the
// words come before them, as in the app's own jump box.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	words := strings.Fields(strings.ToLower(q))
	named := func(text string) bool {
		text = strings.ToLower(text)
		for _, w := range words {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return true
	}
	out := []core.SearchItem{}
	accts, err := s.store.ListAccounts(ctx, uid, true)
	if err != nil {
		return nil, err
	}
	for _, a := range accts {
		if !named(a.Name + " " + a.Institution) {
			continue
		}
		archived := ""
		if a.Archived {
			archived = "archived"
		}
		out = append(out, core.SearchItem{ID: "account:" + strconv.FormatInt(a.ID, 10), Title: a.Name, Kind: "account", Date: a.LastTxDate,
			Snippet: dots(a.Institution, "Balance "+majorString(a.Balance, a.Currency), archived), URL: "/accounts/" + strconv.FormatInt(a.ID, 10)})
	}
	cats, err := s.store.ListCategories(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, c := range cats {
		if c.Archived || !named(c.Name) {
			continue
		}
		used := ""
		switch {
		case c.TxCount == 1:
			used = "1 transaction"
		case c.TxCount > 1:
			used = strconv.Itoa(c.TxCount) + " transactions"
		}
		out = append(out, core.SearchItem{ID: "category:" + strconv.FormatInt(c.ID, 10), Title: firstNonEmpty(c.FullName, c.Name), Kind: "category", Snippet: used,
			URL: "/transactions?category=" + strconv.FormatInt(c.ID, 10)})
	}
	txs, _, err := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), Q: q, Limit: limit})
	if err != nil {
		return nil, err
	}
	for i := range txs {
		t := &txs[i]
		title := txTitle(t)
		// the payee, when the line above does not say it already
		payee := strings.TrimSpace(t.Payee)
		if strings.EqualFold(payee, title) {
			payee = ""
		}
		category := t.CategoryName
		if category == title {
			category = ""
		}
		out = append(out, core.SearchItem{ID: "transaction:" + strconv.FormatInt(t.ID, 10), Title: title, Kind: "transaction", Date: t.Date,
			Snippet: dots(txAmount(t), txAccounts(t), category, payee), URL: txURL(t.ID)})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// sums adds up amounts per currency, in the order the currencies came up.
type sums struct {
	order []string
	by    map[string]int64
}

func (m *sums) add(cur string, v int64) {
	if m.by == nil {
		m.by = map[string]int64{}
	}
	if _, ok := m.by[cur]; !ok {
		m.order = append(m.order, cur)
	}
	m.by[cur] += v
}

// text writes the sums out, joined by sep and, before the last one, by last:
// "42.50 EUR", "42.50 EUR and 30.00 BAM". Empty when nothing was added.
func (m *sums) text(sep, last string) string {
	parts := make([]string, 0, len(m.order))
	for _, cur := range m.order {
		parts = append(parts, majorString(m.by[cur], cur))
	}
	if n := len(parts); n > 1 {
		return strings.Join(parts[:n-1], sep) + last + parts[n-1]
	}
	return strings.Join(parts, "")
}

// pocketDay answers Pocket Day: the transactions dated that day on the
// accounts the person sees, in the order they were made. Amounts are in the
// account's own currency, so nothing is converted; a transfer between
// accounts is listed but is neither spending nor income. Transactions carry
// a plain date, so the time zone is not used.
func (s *Server) pocketDay(r *http.Request, date string, _ *time.Location) (*core.Day, error) {
	ctx := r.Context()
	txs, _, err := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), From: date, To: date, Oldest: true, Limit: 1000})
	if err != nil {
		return nil, err
	}
	if len(txs) == 0 {
		return nil, nil
	}
	day := &core.Day{Date: date}
	var spent, received, moved sums
	payments := 0
	for i := range txs {
		t := &txs[i]
		it := core.DayItem{ID: strconv.FormatInt(t.ID, 10), Title: txTitle(t), Sub: firstNonEmpty(t.CategoryName, t.AccountName), Kind: t.Kind,
			Amount: t.Amount, Currency: t.Currency, URL: txURL(t.ID), Note: strings.TrimSpace(t.Notes)}
		if _, err := time.Parse("15:04", t.Time); err == nil {
			it.Time = t.Time
		}
		switch t.Kind {
		case "expense":
			spent.add(t.Currency, t.Amount)
			payments++
		case "income":
			received.add(t.Currency, t.Amount)
		default:
			it.Sub = txAccounts(t)
			moved.add(t.Currency, t.Amount)
		}
		if it.Sub == it.Title {
			it.Sub = t.AccountName
		}
		day.Items = append(day.Items, it)
	}
	var parts []string
	if payments > 0 {
		word := "payments"
		if payments == 1 {
			word = "payment"
		}
		parts = append(parts, fmt.Sprintf("spent %s in %d %s", spent.text(", ", " and "), payments, word))
		day.Metrics = append(day.Metrics, core.Metric{Label: "Spent", Value: spent.text(" + ", " + ")})
	}
	if len(received.order) > 0 {
		parts = append(parts, "received "+received.text(", ", " and "))
		day.Metrics = append(day.Metrics, core.Metric{Label: "Received", Value: received.text(" + ", " + ")})
	}
	if len(parts) == 0 {
		parts = append(parts, "moved "+moved.text(", ", " and ")+" between accounts")
	}
	day.Summary = capFirst(strings.Join(parts, ", "))
	return day, nil
}

// ---------- widget ----------

// billDue says when a bill is due, next to its amount: "3 days late",
// "today", "tomorrow", "in 12 days".
func billDue(due, today string) string {
	switch {
	case due < today:
		n := store.DaysBetween(due, today)
		if n == 1 {
			return "1 day late"
		}
		return strconv.Itoa(n) + " days late"
	case due == today:
		return "today"
	}
	n := store.DaysBetween(today, due)
	if n == 1 {
		return "tomorrow"
	}
	return "in " + strconv.Itoa(n) + " days"
}

// pocketWidget answers Pocket Hub with the month so far, in the person's
// home currency: what was spent and received (the first page's and the
// dashboard's numbers), what is left of the monthly budgets (the budgets
// page's sum), and the bills due in the next 30 days, the nearest first.
// Everything comes from the app's own tables; no exchange rate is looked up.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	ids := s.visibleIDs(ctx)
	if len(ids) == 0 {
		return nil, nil // no account yet
	}
	st, _ := s.store.GetSettings(ctx, uid)
	today := store.Today()
	from, _ := periodBounds(today, "monthly", st.MonthStartDay, st.WeekStart)
	days, err := s.store.DailySums(ctx, ids, from, today)
	if err != nil {
		return nil, err
	}
	var spent, received int64
	for _, d := range days {
		if d.Kind == "income" {
			received += d.Sum
		} else {
			spent += d.Sum
		}
	}
	w := &core.WidgetData{URL: "/", Metrics: []core.Metric{
		{Label: "Spent", Value: majorString(spent, st.HomeCurrency)},
		{Label: "Received", Value: majorString(received, st.HomeCurrency)},
	}}
	switch {
	case spent > 0:
		w.Line = "Spent " + majorString(spent, st.HomeCurrency) + " this month"
	case received > 0:
		w.Line = "Received " + majorString(received, st.HomeCurrency) + " this month"
	}

	budgets, err := s.store.ListBudgets(ctx, uid)
	if err != nil {
		return nil, err
	}
	var budget, used int64
	monthly := 0
	for i := range budgets {
		if budgets[i].Archived || budgets[i].Period != "monthly" {
			continue
		}
		bs := s.budgetStatus(ctx, &budgets[i], today, st)
		budget += bs.Available
		used += bs.Spent
		monthly++
	}
	switch left := budget - used; {
	case monthly == 0:
	case left < 0:
		w.Metrics = append(w.Metrics, core.Metric{Label: "Over budget", Value: majorString(-left, st.HomeCurrency)})
	default:
		w.Metrics = append(w.Metrics, core.Metric{Label: "Budget left", Value: majorString(left, st.HomeCurrency)})
	}

	subs, err := s.store.ListSubscriptions(ctx, uid)
	if err != nil {
		return nil, err
	}
	type bill struct {
		x   *store.Subscription
		due string
	}
	var bills []bill
	until := addDays(today, 30)
	for i := range subs {
		x := &subs[i]
		if !x.Active || x.Kind != "expense" {
			continue
		}
		// the next time it is due; one that was missed is still to pay
		if due := occurrences(x, until, 3); len(due) > 0 {
			bills = append(bills, bill{x, due[0]})
		}
	}
	sort.SliceStable(bills, func(i, j int) bool { return bills[i].due < bills[j].due })
	if len(bills) > 5 {
		bills = bills[:5]
	}
	for _, b := range bills {
		id := strconv.FormatInt(b.x.ID, 10)
		w.Items = append(w.Items, core.WidgetItem{ID: id, Title: b.x.Name, Detail: dots(majorString(b.x.Amount, b.x.Currency), billDue(b.due, today)), URL: "/subscriptions/" + id})
	}
	if spent == 0 && received == 0 && monthly == 0 && len(w.Items) == 0 {
		return nil, nil // nothing this month
	}
	return w, nil
}

// ---------- capture ----------

// pocketCapture keeps a quick line from Pocket Hub as one expense, the way
// the quick add bar books the same line: "12.50 lunch" goes on the person's
// usual account, in that account's currency, with the category this
// description usually gets; templates, "20 eur", "@account", "#tag" and day
// words work as they do there. Nothing is asked of an AI. A line with more
// lines under it keeps those as the notes. Only expenses are taken: a line
// that reads as income or a transfer is refused, so nothing unexpected moves.
// The day sent with the line is used unless the line names another day.
//
// The same request_id again answers what was booked the first time and adds
// nothing: the receipt is written in the transaction that books the expense.
func (s *Server) pocketCapture(r *http.Request, in core.CaptureInput) (*core.CaptureResult, error) {
	ctx := r.Context()
	op, err := core.NewOperation(fmt.Sprintf("money:user:%d:%s", actorID(ctx), core.CapturePath), in.RequestID, in)
	if err != nil {
		return nil, err
	}
	var res core.CaptureResult
	replay := func() (*core.CaptureResult, error) {
		found, err := op.Replay(ctx, s.store.DB, &res)
		switch {
		case errors.Is(err, core.ErrOperationConflict):
			return nil, fmt.Errorf("%w: this was already added with other words", core.ErrCapture)
		case err != nil || !found:
			return nil, err
		}
		return &res, nil
	}
	if kept, err := replay(); kept != nil || err != nil {
		return kept, err
	}

	line, notes := in.Title, in.Text
	if line == "" {
		line, notes, _ = strings.Cut(in.Text, "\n")
	}
	line, notes = strings.Join(strings.Fields(line), " "), strings.TrimSpace(notes)
	c, _ := s.quickContext(ctx)
	d := quick.Parse(line, c)
	switch {
	case d.AccountID == 0:
		return nil, fmt.Errorf("%w: add an account in Pocket Money first", core.ErrCapture)
	case d.Amount == nil || *d.Amount <= 0:
		return nil, fmt.Errorf("%w: write the amount and what it was for, like \"12.50 lunch\"", core.ErrCapture)
	case d.Kind != "expense":
		return nil, fmt.Errorf("%w: only expenses can be added this way; add income and transfers in Pocket Money", core.ErrCapture)
	}
	if in.Date != "" && d.Date == c.Today.Format("2006-01-02") {
		d.Date = in.Date
	}
	ti := draftToIn(d, "quick")
	if notes != "" {
		ti.Notes = &notes
	}
	_, msg, err := s.createTx(ctx, &ti, func(tx *sql.Tx, t *store.Transaction) error {
		// what was understood, so the person sees where it went: "Lunch · 12.50 EUR · Cash"
		account := ""
		for _, a := range c.Accounts {
			if a.ID == t.AccountID {
				account = a.Name
			}
		}
		res = core.CaptureResult{ID: strconv.FormatInt(t.ID, 10), Title: dots(firstNonEmpty(strings.TrimSpace(t.Description), strings.TrimSpace(t.Payee), "Expense"), txAmount(t), account), URL: txURL(t.ID)}
		return op.Save(ctx, tx, res)
	})
	switch {
	case msg != "":
		return nil, fmt.Errorf("%w: %s", core.ErrCapture, msg)
	case err != nil:
		// the same line sent twice at once: the other one booked it
		if kept, rerr := replay(); kept != nil || rerr != nil {
			return kept, rerr
		}
		return nil, err
	}
	return &res, nil
}
