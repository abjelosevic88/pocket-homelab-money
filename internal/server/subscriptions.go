package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- schedule math ----

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// addPeriod moves a date one period forward, keeping the anchor day of the
// month (a subscription on the 31st is paid on the 30th in April).
func addPeriod(date, freq string, every, anchorDay int) string {
	t, err := store.ParseDate(date)
	if err != nil {
		return date
	}
	if every < 1 {
		every = 1
	}
	switch freq {
	case "daily":
		t = t.AddDate(0, 0, every)
	case "weekly":
		t = t.AddDate(0, 0, 7*every)
	case "yearly":
		y, m := t.Year()+every, t.Month()
		d := anchorDay
		if d <= 0 {
			d = t.Day()
		}
		t = time.Date(y, m, min(d, daysIn(y, m)), 0, 0, 0, 0, time.UTC)
	default: // monthly
		y, m := t.Year(), int(t.Month())-1+every
		y += m / 12
		m = m % 12
		d := anchorDay
		if d <= 0 {
			d = t.Day()
		}
		t = time.Date(y, time.Month(m+1), min(d, daysIn(y, time.Month(m+1))), 0, 0, 0, 0, time.UTC)
	}
	return t.Format("2006-01-02")
}

func subPeriodDays(freq string, every int) float64 {
	if every < 1 {
		every = 1
	}
	switch freq {
	case "daily":
		return float64(every)
	case "weekly":
		return 7 * float64(every)
	case "yearly":
		return 365.25 * float64(every)
	}
	return 30.44 * float64(every)
}

func anchorOf(x *store.Subscription) int {
	d := x.StartDate
	if d == "" {
		d = x.NextDate
	}
	if t, err := store.ParseDate(d); err == nil {
		return t.Day()
	}
	return 0
}

// nthWeekday is the nth (1..4, 5 = last) weekday of a month.
func nthWeekday(y int, m time.Month, nth int, wd time.Weekday) time.Time {
	if nth >= 5 {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC)
		return last.AddDate(0, 0, -((int(last.Weekday()) - int(wd) + 7) % 7))
	}
	first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 0, (int(wd)-int(first.Weekday())+7)%7+7*(nth-1))
}

// anchorWeekday is the weekday a "2nd Tuesday" subscription falls on (its start date's).
func anchorWeekday(x *store.Subscription) time.Weekday {
	d := firstNonEmpty(x.StartDate, x.NextDate)
	if t, err := store.ParseDate(d); err == nil {
		return t.Weekday()
	}
	return time.Monday
}

// step moves a nominal due date one period forward, honouring "nth weekday".
func step(x *store.Subscription, d string) string {
	if x.Freq == "monthly" && x.Nth > 0 {
		t, err := store.ParseDate(d)
		if err != nil {
			return d
		}
		every := max(1, x.Every)
		m := time.Date(t.Year(), t.Month()+time.Month(every), 1, 0, 0, 0, 0, time.UTC)
		return nthWeekday(m.Year(), m.Month(), x.Nth, anchorWeekday(x)).Format("2006-01-02")
	}
	return addPeriod(d, x.Freq, x.Every, anchorOf(x))
}

func isWeekend(d string) bool {
	t, err := store.ParseDate(d)
	return err == nil && (t.Weekday() == time.Saturday || t.Weekday() == time.Sunday)
}

// shiftWeekend moves a date off the weekend: before = the Friday, after = the Monday.
func shiftWeekend(d, mode string) string {
	t, err := store.ParseDate(d)
	if err != nil || !isWeekend(d) {
		return d
	}
	switch mode {
	case "before":
		for t.Weekday() != time.Friday {
			t = t.AddDate(0, 0, -1)
		}
	case "after":
		for t.Weekday() != time.Monday {
			t = t.AddDate(0, 0, 1)
		}
	}
	return t.Format("2006-01-02")
}

// dueOf is when the next payment is actually due: the nominal date moved off
// the weekend, or — with "skip" — the next nominal date that is a weekday.
func dueOf(x *store.Subscription) string {
	d := x.NextDate
	for i := 0; x.Weekend == "skip" && isWeekend(d) && i < 30; i++ {
		d = step(x, d)
	}
	return shiftWeekend(d, x.Weekend)
}

// limitReached reports whether a subscription with "stop after N" is done.
func limitReached(x *store.Subscription) bool { return x.MaxCount > 0 && x.DoneCount >= x.MaxCount }

// tolerance is how far from the due date a payment still counts.
func tolerance(x *store.Subscription) int {
	return int(math.Min(10, math.Max(2, subPeriodDays(x.Freq, x.Every)/3)))
}

// decorate fills the computed fields.
func (s *Server) decorate(ctx context.Context, x *store.Subscription, home string) {
	today := store.Today()
	x.DueDate = dueOf(x)
	x.DaysLeft = store.DaysBetween(today, x.DueDate)
	if x.DueDate < today {
		x.DaysLeft = -store.DaysBetween(x.DueDate, today)
	}
	switch {
	case !x.Active:
		x.Status = "paused"
	case limitReached(x) || (x.EndDate != "" && x.NextDate > x.EndDate):
		x.Status = "ended"
	case x.DaysLeft < 0:
		x.Status = "overdue"
	case x.DaysLeft == 0:
		x.Status = "today"
	case x.DaysLeft <= 7:
		x.Status = "soon"
	default:
		x.Status = "upcoming"
	}
	amt := x.Amount
	if x.AmountMax > amt {
		amt = (x.Amount + x.AmountMax) / 2
	}
	r, _ := s.rate(ctx, x.Currency, home, today)
	perMonth := float64(currency.Convert(amt, x.Currency, home, r)) * 30.44 / subPeriodDays(x.Freq, x.Every)
	x.MonthlyHome = int64(math.Round(perMonth))
	x.YearlyHome = int64(math.Round(perMonth * 12))
	if x.Kind == "income" {
		x.MonthlyHome, x.YearlyHome = -x.MonthlyHome, -x.YearlyHome
	}
}

// occurrences lists the actual due dates (weekend rule applied) from the next
// one up to `until`, stopping at the end date or the payment limit.
func occurrences(x *store.Subscription, until string, limit int) []string {
	var out []string
	d := x.NextDate
	left := -1
	if x.MaxCount > 0 {
		left = max(0, x.MaxCount-x.DoneCount)
	}
	for i := 0; d != "" && i < limit*3 && len(out) < limit && left != 0; i++ {
		if x.EndDate != "" && d > x.EndDate {
			break
		}
		if x.Weekend == "skip" && isWeekend(d) {
			d = step(x, d)
			continue
		}
		due := shiftWeekend(d, x.Weekend)
		if due > until {
			break
		}
		out = append(out, due)
		if left > 0 {
			left--
		}
		d = step(x, d)
	}
	return out
}

// ---- matching payments ----

var reDigits = regexp.MustCompile(`[0-9#.,/:-]+`)

func normText(s string) string {
	s = fold(s)
	s = reDigits.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func fold(s string) string {
	r := strings.NewReplacer("č", "c", "ć", "c", "š", "s", "ž", "z", "đ", "dj", "Č", "c", "Ć", "c", "Š", "s", "Ž", "z", "Đ", "dj")
	return strings.ToLower(r.Replace(s))
}

func textMatches(x *store.Subscription, t *store.Transaction) bool {
	hay := " " + fold(t.Description+" "+t.Payee) + " "
	if fold(strings.TrimSpace(t.Description)) == fold(x.Name) {
		return true
	}
	words := strings.Fields(fold(x.MatchText))
	if len(words) == 0 {
		words = strings.Fields(fold(x.Name))
	}
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

func (s *Server) amountFits(ctx context.Context, x *store.Subscription, t *store.Transaction) bool {
	if x.Amount == 0 {
		return true
	}
	var v int64
	switch x.Currency {
	case t.Currency:
		v = t.Amount
	case t.ForeignCurrency:
		v = t.ForeignAmount
	default:
		r, _ := s.rate(ctx, t.Currency, x.Currency, t.Date)
		v = currency.Convert(t.Amount, t.Currency, x.Currency, r)
	}
	hi := x.Amount
	if x.AmountMax > hi {
		hi = x.AmountMax
	}
	return float64(v) >= float64(x.Amount)*0.8 && float64(v) <= float64(hi)*1.25
}

// matchSubscription links a new transaction to the subscription it pays:
// one of the booker's own, or of the account owner's when booked on a shared
// account.
func (s *Server) matchSubscription(ctx context.Context, t *store.Transaction) {
	if t.Kind == "transfer" && t.ToAccountID == nil {
		return
	}
	owners := []int64{actorID(ctx)}
	if t.UserID > 0 && t.UserID != owners[0] {
		owners = append(owners, t.UserID)
	}
	var subs []store.Subscription
	for _, o := range owners {
		if o <= 0 {
			continue
		}
		list, err := s.store.ListSubscriptions(ctx, o)
		if err != nil {
			return
		}
		subs = append(subs, list...)
	}
	for i := range subs {
		x := &subs[i]
		if !x.Active || x.Kind != t.Kind || x.NextDate == "" || limitReached(x) {
			continue
		}
		if x.AccountID != nil && *x.AccountID != t.AccountID {
			continue
		}
		if x.AccountID == nil && x.OwnerID != actorID(ctx) && s.store.AccountRole(ctx, t.AccountID, x.OwnerID) == "" {
			continue
		}
		tol := tolerance(x)
		lo, _ := store.ParseDate(dueOf(x))
		from := lo.AddDate(0, 0, -tol).Format("2006-01-02")
		to := lo.AddDate(0, 0, int(math.Max(float64(tol), subPeriodDays(x.Freq, x.Every)/2))).Format("2006-01-02")
		if t.Date < from || t.Date > to {
			continue
		}
		if !textMatches(x, t) || !s.amountFits(ctx, x, t) {
			continue
		}
		id := x.ID
		t.SubscriptionID = &id
		_ = s.store.SetTransactionSubscription(ctx, t.ID, &id)
		s.advanceSubscription(ctx, x.ID, t.Date)
		return
	}
}

// advanceSubscription moves next_date past a payment made on date.
func (s *Server) advanceSubscription(ctx context.Context, id int64, date string) {
	x, err := s.store.GetSubscription(ctx, id)
	if err != nil || x.NextDate == "" {
		return
	}
	nd, _ := store.ParseDate(dueOf(x))
	if date < nd.AddDate(0, 0, -tolerance(x)).Format("2006-01-02") {
		return // an early or old payment does not pay the next one
	}
	s.moveOn(ctx, x, true)
}

// moveOn steps a subscription to its next due date; paid counts toward
// "stop after N", a skip does not. Weekend-skipped dates are stepped over.
func (s *Server) moveOn(ctx context.Context, x *store.Subscription, paid bool) {
	for i := 0; x.Weekend == "skip" && isWeekend(x.NextDate) && i < 30; i++ {
		x.NextDate = step(x, x.NextDate)
	}
	last := x.NextDate
	x.NextDate = step(x, x.NextDate)
	if paid {
		x.DoneCount++
		if limitReached(x) && (x.EndDate == "" || x.EndDate > last) {
			x.EndDate = last
		}
	}
	x.NotifiedFor = ""
	_ = s.store.UpdateSubscription(ctx, x)
}

// bookDueSubscriptions creates the transactions of "auto" subscriptions that
// are due, each booked as its owner. People whose Firefly III is still being
// mirrored (mirrored[owner]) are skipped: Firefly's own recurrences book theirs.
func (s *Server) bookDueSubscriptions(ctx context.Context, mirrored map[int64]bool) {
	ctx = withAuditJob(ctx, "Auto-booking", "schedule")
	subs, err := s.store.ListSubscriptions(ctx, 0)
	if err != nil {
		return
	}
	today := store.Today()
	for i := range subs {
		x := &subs[i]
		if mirrored[x.OwnerID] {
			continue
		}
		for n := 0; n < 12 && x.Active && x.Mode == "auto" && x.NextDate != "" && !limitReached(x) && dueOf(x) <= today && (x.EndDate == "" || x.NextDate <= x.EndDate); n++ {
			owner, err := s.store.GetUser(ctx, x.OwnerID)
			if err != nil {
				break
			}
			uctx := context.WithValue(ctx, userKey, owner)
			due := dueOf(x)
			before := x.NextDate
			t, msg, err := s.bookSubscription(uctx, x, due, nil, nil)
			if msg != "" || err != nil {
				s.log.Warn("auto-book subscription", "name", x.Name, "msg", msg, "err", err)
				break
			}
			s.log.Info("subscription booked", "name", x.Name, "date", due)
			s.publish("money.subscription_paid", fmt.Sprintf("subscription:%d", x.ID), s.subPaidEvent(ctx, x, t, true))
			nx, err := s.store.GetSubscription(ctx, x.ID)
			if err != nil || nx.NextDate == before {
				break
			}
			*x = *nx
		}
	}
}

// bookSubscription creates the transaction for one due date.
func (s *Server) bookSubscription(ctx context.Context, x *store.Subscription, date string, amount *int64, accountID *int64) (*store.Transaction, string, error) {
	desc := x.Description
	if desc == "" {
		desc = x.Name
	}
	amt := x.Amount
	if amount != nil {
		amt = *amount
	}
	id := x.ID
	in := txIn{Kind: &x.Kind, Date: &date, AccountID: x.AccountID, ToAccountID: x.ToAccountID, Amount: &amt, Currency: &x.Currency, Description: &desc,
		Payee: &x.Payee, CategoryID: x.CategoryID, Tags: &x.Tags, SubscriptionID: &id, Source: "subscription", SkipRules: true}
	empty := ""
	in.Time = &empty
	if accountID != nil {
		in.AccountID = accountID
	}
	if in.AccountID == nil {
		return nil, "the subscription has no account to pay from", nil
	}
	return s.createTx(ctx, &in)
}

// ---- handlers ----

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subs, err := s.store.ListSubscriptions(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "subscriptions")
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	var monthly, yearly, income int64
	for i := range subs {
		s.decorate(ctx, &subs[i], st.HomeCurrency)
		if subs[i].Active && subs[i].Status != "ended" {
			if subs[i].Kind == "income" {
				income += -subs[i].MonthlyHome
			} else if subs[i].Kind == "expense" {
				monthly += subs[i].MonthlyHome
				yearly += subs[i].YearlyHome
			}
		}
	}
	// next 60 days as a list of dated payments
	until := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
	type upcoming struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Date     string `json:"date"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Kind     string `json:"kind"`
		Icon     string `json:"icon"`
	}
	up := []upcoming{}
	for _, x := range subs {
		if !x.Active {
			continue
		}
		for _, d := range occurrences(&x, until, 70) {
			up = append(up, upcoming{x.ID, x.Name, d, x.Amount, x.Currency, x.Kind, firstNonEmpty(x.Icon, x.CategoryIcon)})
		}
	}
	sort.Slice(up, func(i, j int) bool { return up[i].Date < up[j].Date })
	writeJSON(w, http.StatusOK, map[string]any{"items": subs, "monthly": monthly, "yearly": yearly, "income_monthly": income, "currency": st.HomeCurrency, "upcoming": up})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Server) ownSub(w http.ResponseWriter, r *http.Request) *store.Subscription {
	x, err := s.store.GetSubscription(r.Context(), idParam(r, "id"))
	if err != nil || x.OwnerID != actorID(r.Context()) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return x
}

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	x := s.ownSub(w, r)
	if x == nil {
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	s.decorate(ctx, x, st.HomeCurrency)
	txs, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), SubscriptionID: x.ID, Limit: 60})
	writeJSON(w, http.StatusOK, map[string]any{"subscription": x, "payments": txs, "next": occurrences(x, time.Now().AddDate(1, 0, 0).Format("2006-01-02"), 14)})
}

type subIn struct {
	Name        *string   `json:"name"`
	Kind        *string   `json:"kind"`
	Amount      *int64    `json:"amount"`
	AmountMax   *int64    `json:"amount_max"`
	Currency    *string   `json:"currency"`
	AccountID   *int64    `json:"account_id"`
	ToAccountID *int64    `json:"to_account_id"`
	CategoryID  *int64    `json:"category_id"`
	Tags        *[]string `json:"tags"`
	Payee       *string   `json:"payee"`
	Description *string   `json:"description"`
	Freq        *string   `json:"freq"`
	Every       *int      `json:"every"`
	StartDate   *string   `json:"start_date"`
	NextDate    *string   `json:"next_date"`
	EndDate     *string   `json:"end_date"`
	Mode        *string   `json:"mode"`
	NotifyDays  *int      `json:"notify_days"`
	MatchText   *string   `json:"match_text"`
	URL         *string   `json:"url"`
	Icon        *string   `json:"icon"`
	Notes       *string   `json:"notes"`
	Active      *bool     `json:"active"`
	Weekend     *string   `json:"weekend"`
	Nth         *int      `json:"nth"`
	MaxCount    *int      `json:"max_count"`
	DoneCount   *int      `json:"done_count"`
}

func (s *Server) applySub(ctx context.Context, in *subIn, x *store.Subscription) string {
	str := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	str(&x.Name, in.Name)
	str(&x.Kind, in.Kind)
	str(&x.Payee, in.Payee)
	str(&x.Description, in.Description)
	str(&x.Freq, in.Freq)
	str(&x.StartDate, in.StartDate)
	str(&x.NextDate, in.NextDate)
	str(&x.EndDate, in.EndDate)
	str(&x.Mode, in.Mode)
	str(&x.MatchText, in.MatchText)
	str(&x.URL, in.URL)
	str(&x.Icon, in.Icon)
	if in.Notes != nil {
		x.Notes = *in.Notes
	}
	if in.Currency != nil {
		x.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	if in.Amount != nil {
		x.Amount = *in.Amount
	}
	if in.AmountMax != nil {
		x.AmountMax = *in.AmountMax
	}
	if in.AccountID != nil {
		x.AccountID = idOrNil(in.AccountID)
	}
	if in.ToAccountID != nil {
		x.ToAccountID = idOrNil(in.ToAccountID)
	}
	if in.CategoryID != nil {
		x.CategoryID = idOrNil(in.CategoryID)
	}
	if in.Tags != nil {
		x.Tags = cleanTags(*in.Tags)
	}
	if in.Every != nil {
		x.Every = *in.Every
	}
	if in.NotifyDays != nil {
		x.NotifyDays = *in.NotifyDays
	}
	if in.Active != nil {
		x.Active = *in.Active
	}
	if in.Weekend != nil {
		x.Weekend = *in.Weekend
	}
	if in.Nth != nil {
		x.Nth = *in.Nth
	}
	if in.MaxCount != nil {
		x.MaxCount = max(0, *in.MaxCount)
	}
	if in.DoneCount != nil {
		x.DoneCount = max(0, *in.DoneCount)
	}
	if x.Weekend != "before" && x.Weekend != "after" && x.Weekend != "skip" {
		x.Weekend = ""
	}
	if x.Nth < 0 || x.Nth > 5 || x.Freq != "monthly" {
		x.Nth = 0
	}
	// "2nd Tuesday": the next date must itself be that weekday of its month
	if x.Nth > 0 {
		if t, err := store.ParseDate(x.NextDate); err == nil {
			c := nthWeekday(t.Year(), t.Month(), x.Nth, anchorWeekday(x))
			if c.Before(t) {
				m := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
				c = nthWeekday(m.Year(), m.Month(), x.Nth, anchorWeekday(x))
			}
			x.NextDate = c.Format("2006-01-02")
		}
	}
	if x.Every < 1 {
		x.Every = 1
	}
	if x.NextDate == "" {
		x.NextDate = x.StartDate
	}
	if x.StartDate == "" {
		x.StartDate = x.NextDate
	}
	if x.AmountMax != 0 && x.AmountMax < x.Amount {
		x.AmountMax = 0
	}
	switch {
	case x.Name == "":
		return "name is required"
	case !store.Valid(store.TransactionKinds, x.Kind):
		return "unknown kind"
	case !store.Valid(store.Frequencies, x.Freq):
		return "frequency must be daily, weekly, monthly or yearly"
	case !store.Valid(store.SubscriptionModes, x.Mode):
		return "mode must be remind, auto or track"
	case !currency.Valid(x.Currency):
		return "unknown currency"
	case x.NextDate == "" || !validDate(x.NextDate) || !validDate(x.StartDate) || !validDate(x.EndDate):
		return "dates must be YYYY-MM-DD (the next due date is required)"
	case x.AccountID != nil && !s.canWrite(ctx, *x.AccountID):
		return "you cannot book on that account"
	case x.ToAccountID != nil && !s.canWrite(ctx, *x.ToAccountID):
		return "you cannot book on the destination account"
	case in.CategoryID != nil && x.CategoryID != nil && !s.store.OwnsCategories(ctx, x.OwnerID, []int64{*x.CategoryID}):
		return "unknown category"
	case x.Mode == "auto" && x.AccountID == nil:
		return "automatic booking needs an account"
	case x.Kind == "transfer" && x.ToAccountID == nil:
		return "a transfer needs a destination account"
	}
	return ""
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in subIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	x := &store.Subscription{OwnerID: actorID(ctx), Kind: "expense", Currency: st.HomeCurrency, Freq: "monthly", Every: 1, Mode: "remind", NotifyDays: 2, Active: true, Tags: []string{}}
	if msg := s.applySub(ctx, &in, x); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if x.CategoryID == nil {
		x.CategoryID = s.store.LearnedCategory(ctx, x.OwnerID, x.Name)
	}
	if err := s.store.CreateSubscription(ctx, x); err != nil {
		s.fail(w, err, "create subscription")
		return
	}
	// payments already on the books under this name get linked
	s.relinkSubscription(ctx, x.ID)
	x, _ = s.store.GetSubscription(ctx, x.ID)
	s.decorate(ctx, x, st.HomeCurrency)
	s.store.LogActivity(ctx, actorID(ctx), "subscription.create", x.Name)
	writeJSON(w, http.StatusCreated, x)
}

// relinkSubscription links past unlinked transactions that look like payments of it.
func (s *Server) relinkSubscription(ctx context.Context, id int64) int {
	x, err := s.store.GetSubscription(ctx, id)
	if err != nil {
		return 0
	}
	from := time.Now().AddDate(-2, 0, 0).Format("2006-01-02")
	txs, _, err := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), Kind: x.Kind, From: from, Limit: 5000})
	if err != nil {
		return 0
	}
	n := 0
	for i := range txs {
		t := &txs[i]
		if t.SubscriptionID != nil || (x.AccountID != nil && *x.AccountID != t.AccountID) || !s.canWrite(ctx, t.AccountID) {
			continue
		}
		if textMatches(x, t) && s.amountFits(ctx, x, t) {
			_ = s.store.SetTransactionSubscription(ctx, t.ID, &id)
			n++
		}
	}
	return n
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	x := s.ownSub(w, r)
	if x == nil {
		return
	}
	var in subIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	oldNext := x.NextDate
	if msg := s.applySub(ctx, &in, x); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if x.NextDate != oldNext {
		x.NotifiedFor = ""
	}
	if err := s.store.UpdateSubscription(ctx, x); err != nil {
		s.fail(w, err, "update subscription")
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	x, _ = s.store.GetSubscription(ctx, x.ID)
	s.decorate(ctx, x, st.HomeCurrency)
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	x := s.ownSub(w, r)
	if x == nil {
		return
	}
	if err := s.store.DeleteSubscription(r.Context(), x.ID); err != nil {
		s.fail(w, err, "delete subscription")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePaySubscription(w http.ResponseWriter, r *http.Request) {
	x := s.ownSub(w, r)
	if x == nil {
		return
	}
	var in struct {
		Amount    *int64 `json:"amount"`
		Date      string `json:"date"`
		AccountID *int64 `json:"account_id"`
	}
	_ = decode(r, &in)
	date := in.Date
	if date == "" {
		date = store.Today()
	}
	t, msg, err := s.bookSubscription(r.Context(), x, date, in.Amount, idOrNil(in.AccountID))
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		s.fail(w, err, "pay")
		return
	}
	// paying ahead of time still pays the next one
	if nx, err := s.store.GetSubscription(r.Context(), x.ID); err == nil && nx.NextDate == x.NextDate {
		s.moveOn(r.Context(), nx, true)
	}
	s.publish("money.subscription_paid", fmt.Sprintf("subscription:%d", x.ID), s.subPaidEvent(r.Context(), x, t, false))
	writeJSON(w, http.StatusCreated, t)
}

// subPaidEvent is the small payload a Pocket Core event carries about a paid subscription.
func (s *Server) subPaidEvent(ctx context.Context, x *store.Subscription, t *store.Transaction, auto bool) map[string]any {
	email := ""
	if u, err := s.store.GetUser(ctx, x.OwnerID); err == nil {
		email = u.Email
	}
	ev := map[string]any{"user_id": x.OwnerID, "user_email": email, "subscription_id": x.ID, "subscription": x.Name, "currency": x.Currency, "auto": auto}
	if t != nil {
		ev["transaction_id"], ev["date"], ev["amount"] = t.ID, t.Date, t.Amount
	}
	return ev
}

func (s *Server) handleSkipSubscription(w http.ResponseWriter, r *http.Request) {
	x := s.ownSub(w, r)
	if x == nil {
		return
	}
	s.moveOn(r.Context(), x, false)
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) handleLinkSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	x := s.ownSub(w, r)
	if x == nil {
		return
	}
	var in struct {
		TransactionID int64 `json:"transaction_id"`
		Unlink        bool  `json:"unlink"`
		Relink        bool  `json:"relink"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Relink {
		writeJSON(w, http.StatusOK, map[string]int{"linked": s.relinkSubscription(ctx, x.ID)})
		return
	}
	t, err := s.store.GetTransaction(ctx, in.TransactionID)
	if err != nil || !s.canWrite(ctx, t.AccountID) {
		writeErr(w, http.StatusNotFound, "transaction not found")
		return
	}
	if in.Unlink {
		_ = s.store.SetTransactionSubscription(ctx, t.ID, nil)
	} else {
		id := x.ID
		_ = s.store.SetTransactionSubscription(ctx, t.ID, &id)
		s.advanceSubscription(ctx, x.ID, t.Date)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Detected is a repeating payment that is not a subscription yet.
type Detected struct {
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Amount      int64   `json:"amount"`
	Currency    string  `json:"currency"`
	AccountID   int64   `json:"account_id"`
	CategoryID  *int64  `json:"category_id"`
	Freq        string  `json:"freq"`
	Every       int     `json:"every"`
	Count       int     `json:"count"`
	LastDate    string  `json:"last_date"`
	NextDate    string  `json:"next_date"`
	Confidence  float64 `json:"confidence"`
	MonthlyHome int64   `json:"monthly_home"`
}

// handleDetectSubscriptions looks for payments that repeat on a rhythm.
func (s *Server) handleDetectSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.store.PatternRows(ctx, s.visibleIDs(ctx), time.Now().AddDate(0, -14, 0).Format("2006-01-02"))
	if err != nil {
		s.fail(w, err, "detect")
		return
	}
	subs, _ := s.store.ListSubscriptions(ctx, actorID(ctx))
	known := map[string]bool{}
	for _, x := range subs {
		known[normText(x.Name)] = true
		if x.MatchText != "" {
			known[normText(x.MatchText)] = true
		}
	}
	groups := map[string][]store.PatternRow{}
	for _, row := range rows {
		if row.SubID != nil {
			continue
		}
		key := normText(firstNonEmpty(row.Payee, row.Description))
		if key == "" || known[key] {
			continue
		}
		groups[row.Kind+"|"+key] = append(groups[row.Kind+"|"+key], row)
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	out := []Detected{}
	for _, g := range groups {
		if len(g) < 3 {
			continue
		}
		var gaps []float64
		for i := 1; i < len(g); i++ {
			a, _ := store.ParseDate(g[i-1].Date)
			b, _ := store.ParseDate(g[i].Date)
			gaps = append(gaps, b.Sub(a).Hours()/24)
		}
		med := median(gaps)
		freq, every := "", 1
		switch {
		case med >= 6 && med <= 8:
			freq = "weekly"
		case med >= 13 && med <= 15:
			freq, every = "weekly", 2
		case med >= 27 && med <= 33:
			freq = "monthly"
		case med >= 85 && med <= 95:
			freq, every = "monthly", 3
		case med >= 350 && med <= 380:
			freq = "yearly"
		default:
			continue
		}
		// the rhythm must hold for most gaps and the amount must be steady
		steady := 0
		for _, gp := range gaps {
			if math.Abs(gp-med) <= math.Max(3, med*0.15) {
				steady++
			}
		}
		amts := make([]float64, len(g))
		for i := range g {
			amts[i] = float64(g[i].Amount)
		}
		mAmt := median(amts)
		spread := 0.0
		for _, a := range amts {
			spread = math.Max(spread, math.Abs(a-mAmt)/math.Max(1, mAmt))
		}
		maxSpread := 0.2
		if freq == "weekly" {
			maxSpread = 0.08 // weekly shopping/fuel is a habit, not a subscription
		}
		if float64(steady) < 0.7*float64(len(gaps)) || spread > maxSpread {
			continue
		}
		last := g[len(g)-1]
		lt := &store.Transaction{Description: last.Description, Payee: last.Payee}
		covered := false
		for i := range subs {
			if subs[i].Kind == last.Kind && textMatches(&subs[i], lt) {
				covered = true
			}
		}
		if covered {
			continue
		}
		next := addPeriod(last.Date, freq, every, 0)
		if store.DaysBetween(next, store.Today()) > int(med) {
			continue // stopped a while ago
		}
		d := Detected{Name: capFirst(firstNonEmpty(last.Payee, last.Description)), Kind: last.Kind, Amount: int64(mAmt), Currency: last.Currency, AccountID: last.AccountID,
			CategoryID: last.CategoryID, Freq: freq, Every: every, Count: len(g), LastDate: last.Date, NextDate: next,
			Confidence: math.Round((float64(steady)/float64(len(gaps))*(1-spread))*100) / 100}
		r, _ := s.rate(ctx, d.Currency, st.HomeCurrency, store.Today())
		d.MonthlyHome = int64(float64(currency.Convert(d.Amount, d.Currency, st.HomeCurrency, r)) * 30.44 / subPeriodDays(freq, every))
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MonthlyHome > out[j].MonthlyHome })
	writeJSON(w, http.StatusOK, out)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64{}, v...)
	sort.Float64s(c)
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// handleCalendarFeed serves upcoming payments as iCalendar; ?token= authenticates.
func (s *Server) handleCalendarFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := s.store.GetUserByAPIToken(ctx, r.URL.Query().Get("token"))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "token required")
		return
	}
	subs, _ := s.store.ListSubscriptions(ctx, u.ID)
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Pocket Money//Subscriptions//EN\r\nCALSCALE:GREGORIAN\r\nX-WR-CALNAME:Payments\r\n")
	until := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	stamp := time.Now().UTC().Format("20060102T150405Z")
	esc := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`)
	for _, x := range subs {
		if !x.Active {
			continue
		}
		for _, d := range occurrences(&x, until, 60) {
			day := strings.ReplaceAll(d, "-", "")
			t, _ := store.ParseDate(d)
			end := t.AddDate(0, 0, 1).Format("20060102")
			amt := majorString(x.Amount, x.Currency)
			fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:pm-sub-%d-%s\r\nDTSTAMP:%s\r\nDTSTART;VALUE=DATE:%s\r\nDTEND;VALUE=DATE:%s\r\nSUMMARY:%s\r\nDESCRIPTION:%s\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\n",
				x.ID, day, stamp, day, end, esc.Replace(x.Name+" · "+amt), esc.Replace(strings.TrimSpace(x.Notes)))
		}
	}
	b.WriteString("END:VCALENDAR\r\n")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
