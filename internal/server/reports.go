package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// rangeParams reads from/to, or month=YYYY-MM (a payday month when the
// month starts later than the 1st), defaulting to the current month.
func (s *Server) rangeParams(r *http.Request, st store.Settings) (string, string) {
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if from != "" && to != "" && validDate(from) && validDate(to) {
		return from, to
	}
	if m := q.Get("month"); len(m) == 7 {
		day := fmt.Sprintf("%s-%02d", m, st.MonthStartDay)
		return periodBounds(day, "monthly", st.MonthStartDay, st.WeekStart)
	}
	if y := q.Get("year"); len(y) == 4 {
		return y + "-01-01", y + "-12-31"
	}
	return periodBounds(store.Today(), "monthly", st.MonthStartDay, st.WeekStart)
}

// shiftRange returns the same-length range right before [from, to].
func shiftRange(from, to string) (string, string) {
	n := store.DaysBetween(from, to) + 1
	return addDays(from, -n), addDays(from, -1)
}

func (s *Server) handleReportOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	from, to := s.rangeParams(r, st)
	ids := s.visibleIDs(ctx)
	if a := queryInt(r, "account"); a > 0 && s.canSee(ctx, int64(a)) {
		ids = []int64{int64(a)}
	}
	days, err := s.store.DailySums(ctx, ids, from, to)
	if err != nil {
		s.fail(w, err, "overview")
		return
	}
	var inc, exp int64
	count := 0
	daily := map[string]int64{}
	for _, d := range days {
		if d.Kind == "income" {
			inc += d.Sum
		} else {
			exp += d.Sum
			daily[d.Date] += d.Sum
		}
		count += d.Count
	}
	pf, pt := shiftRange(from, to)
	pdays, _ := s.store.DailySums(ctx, ids, pf, pt)
	var pinc, pexp int64
	for _, d := range pdays {
		if d.Kind == "income" {
			pinc += d.Sum
		} else {
			pexp += d.Sum
		}
	}
	series := []store.DayAmount{}
	for d := from; d <= to; d = addDay(d) {
		series = append(series, store.DayAmount{Date: d, Amount: daily[d]})
	}
	cats, _ := s.store.CategorySums(ctx, ids, "expense", from, to)
	pcats, _ := s.store.CategorySums(ctx, ids, "expense", pf, pt)
	prev := map[int64]int64{}
	for _, c := range pcats {
		prev[c.ID] = c.Sum
	}
	for i := range cats {
		cats[i].Prev = prev[cats[i].ID]
	}
	incCats, _ := s.store.CategorySums(ctx, ids, "income", from, to)
	top, _ := s.store.TopSpends(ctx, ids, from, to, 8)
	elapsed := store.DaysBetween(from, minStr(store.Today(), to)) + 1
	if store.Today() < from {
		elapsed = 0
	}
	avg := int64(0)
	if elapsed > 0 {
		avg = exp / int64(elapsed)
	}
	rate := 0.0
	if inc > 0 {
		rate = math.Round(float64(inc-exp)/float64(inc)*1000) / 10
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "currency": st.HomeCurrency, "label": periodLabel(from, to, guessPeriod(from, to)),
		"income": inc, "expense": exp, "net": inc - exp, "count": count, "savings_rate": rate, "avg_daily": avg,
		"prev":  map[string]any{"from": pf, "to": pt, "income": pinc, "expense": pexp, "net": pinc - pexp},
		"daily": series, "categories": cats, "income_categories": incCats, "top": top,
	})
}

func guessPeriod(from, to string) string {
	n := store.DaysBetween(from, to) + 1
	switch {
	case n == 7:
		return "weekly"
	case n >= 365:
		return "yearly"
	}
	return "monthly"
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}

// handleReportFlow returns income/expense per month for the last N months.
func (s *Server) handleReportFlow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	n := queryInt(r, "months")
	if n <= 0 || n > 60 {
		n = 12
	}
	ids := s.visibleIDs(ctx)
	if a := queryInt(r, "account"); a > 0 && s.canSee(ctx, int64(a)) {
		ids = []int64{int64(a)}
	}
	type bucket struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Label   string `json:"label"`
		Income  int64  `json:"income"`
		Expense int64  `json:"expense"`
		Net     int64  `json:"net"`
	}
	f, t := periodBounds(store.Today(), "monthly", st.MonthStartDay, st.WeekStart)
	buckets := make([]bucket, n)
	for i := n - 1; i >= 0; i-- {
		buckets[i] = bucket{From: f, To: t, Label: periodLabel(f, t, "monthly")}
		f, t = prevPeriod(f, "monthly", st.MonthStartDay, st.WeekStart)
	}
	days, err := s.store.DailySums(ctx, ids, buckets[0].From, buckets[n-1].To)
	if err != nil {
		s.fail(w, err, "flow")
		return
	}
	j := 0
	for _, d := range days {
		for j < n && d.Date > buckets[j].To {
			j++
		}
		if j >= n {
			break
		}
		if d.Kind == "income" {
			buckets[j].Income += d.Sum
		} else {
			buckets[j].Expense += d.Sum
		}
	}
	for i := range buckets {
		buckets[i].Net = buckets[i].Income - buckets[i].Expense
	}
	writeJSON(w, http.StatusOK, map[string]any{"currency": st.HomeCurrency, "months": buckets})
}

// handleReportCategories totals a kind per category with the previous period
// and a 3-period average for comparison; parents carry their children.
func (s *Server) handleReportCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	from, to := s.rangeParams(r, st)
	kind := r.URL.Query().Get("kind")
	if kind != "income" {
		kind = "expense"
	}
	ids := s.visibleIDs(ctx)
	cur, err := s.store.CategorySums(ctx, ids, kind, from, to)
	if err != nil {
		s.fail(w, err, "categories")
		return
	}
	pf, pt := shiftRange(from, to)
	prev, _ := s.store.CategorySums(ctx, ids, kind, pf, pt)
	span := store.DaysBetween(from, to) + 1
	avg3, _ := s.store.CategorySums(ctx, ids, kind, addDays(from, -3*span), addDays(from, -1))
	pm, am := map[int64]int64{}, map[int64]int64{}
	for _, c := range prev {
		pm[c.ID] = c.Sum
	}
	for _, c := range avg3 {
		am[c.ID] = c.Sum / 3
	}
	for i := range cur {
		cur[i].Prev = pm[cur[i].ID]
		cur[i].Average = am[cur[i].ID]
	}
	// roll children up into their parents for the top-level view
	cats, _ := s.store.ListCategories(ctx)
	byID := map[int64]store.Category{}
	for _, c := range cats {
		byID[c.ID] = c
	}
	parents := map[int64]*store.GroupSum{}
	var order []int64
	for _, g := range cur {
		pid := g.ID
		if g.ParentID != nil {
			pid = *g.ParentID
		}
		p := parents[pid]
		if p == nil {
			c := byID[pid]
			p = &store.GroupSum{ID: pid, Name: c.Name, Icon: c.Icon, Color: c.Color}
			parents[pid] = p
			order = append(order, pid)
		}
		p.Sum += g.Sum
		p.Count += g.Count
		p.Prev += g.Prev
		p.Average += g.Average
	}
	top := []store.GroupSum{}
	for _, id := range order {
		top = append(top, *parents[id])
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Sum > top[j].Sum })
	var total int64
	for _, g := range cur {
		total += g.Sum
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "kind": kind, "currency": st.HomeCurrency, "total": total, "categories": cur, "parents": top})
}

func (s *Server) handleReportTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	from, to := s.rangeParams(r, st)
	if queryBool(r, "all") {
		from, to = "1900-01-01", "2999-12-31"
	}
	out, err := s.store.TagSums(ctx, s.visibleIDs(ctx), "expense", from, to)
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "currency": st.HomeCurrency, "tags": out})
}

// handleReportNetWorth returns month-end net worth for the last N months,
// every account converted at that date's rate.
func (s *Server) handleReportNetWorth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	n := queryInt(r, "months")
	if n <= 0 || n > 60 {
		n = 12
	}
	accts, err := s.store.ListAccounts(ctx, actorID(ctx), true)
	if err != nil {
		s.fail(w, err, "net worth")
		return
	}
	type point struct {
		Date        string           `json:"date"`
		Label       string           `json:"label"`
		Assets      int64            `json:"assets"`
		Liabilities int64            `json:"liabilities"`
		Net         int64            `json:"net"`
		ByAccount   map[string]int64 `json:"by_account"`
	}
	ends := []string{}
	f, t := periodBounds(store.Today(), "monthly", st.MonthStartDay, st.WeekStart)
	ends = append(ends, store.Today())
	for i := 1; i < n; i++ {
		f, t = prevPeriod(f, "monthly", st.MonthStartDay, st.WeekStart)
		ends = append(ends, t)
	}
	first := s.store.FirstTransactionDate(ctx, s.visibleIDs(ctx))
	pts := []point{}
	for i := len(ends) - 1; i >= 0; i-- {
		d := ends[i]
		if first != "" && d < addDays(first, -31) {
			continue
		}
		bal, err := s.store.Balances(ctx, d)
		if err != nil {
			s.fail(w, err, "net worth")
			return
		}
		p := point{Date: d, ByAccount: map[string]int64{}}
		tt, _ := store.ParseDate(d)
		p.Label = monNames[tt.Month()-1] + " " + tt.Format("06")
		for _, a := range accts {
			if !a.IncludeNetWorth || (a.OpeningDate != "" && a.OpeningDate > d) {
				continue
			}
			b := a.OpeningBalance + bal[a.ID].Delta
			rt, _ := s.rate(ctx, a.Currency, st.HomeCurrency, d)
			h := currency.Convert(b, a.Currency, st.HomeCurrency, rt)
			p.ByAccount[strconv.FormatInt(a.ID, 10)] = h
			if h < 0 {
				p.Liabilities += -h
			} else {
				p.Assets += h
			}
		}
		p.Net = p.Assets - p.Liabilities
		pts = append(pts, p)
	}
	names := map[string]string{}
	for _, a := range accts {
		names[strconv.FormatInt(a.ID, 10)] = a.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{"currency": st.HomeCurrency, "points": pts, "accounts": names})
}

// handleReportCalendar returns spend per day for a year (heatmap).
func (s *Server) handleReportCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	year := r.URL.Query().Get("year")
	if len(year) != 4 {
		year = time.Now().Format("2006")
	}
	days, err := s.store.DailySums(ctx, s.visibleIDs(ctx), year+"-01-01", year+"-12-31")
	if err != nil {
		s.fail(w, err, "calendar")
		return
	}
	out := map[string]map[string]int64{}
	for _, d := range days {
		if out[d.Date] == nil {
			out[d.Date] = map[string]int64{}
		}
		out[d.Date][d.Kind] += d.Sum
		out[d.Date]["count"] += int64(d.Count)
	}
	writeJSON(w, http.StatusOK, map[string]any{"year": year, "currency": st.HomeCurrency, "days": out})
}

// Forecast is what the rest of the month probably looks like.
type Forecast struct {
	From           string            `json:"from"`
	To             string            `json:"to"`
	DaysLeft       int               `json:"days_left"`
	Currency       string            `json:"currency"`
	Balance        int64             `json:"balance"`         // liquid accounts now
	IncomeSoFar    int64             `json:"income_so_far"`   // this month
	SpentSoFar     int64             `json:"spent_so_far"`    // this month
	BillsLeft      int64             `json:"bills_left"`      // subscriptions still due this month
	IncomeExpected int64             `json:"income_expected"` // recurring income still due
	GoalsNeed      int64             `json:"goals_need"`      // monthly saving the goals ask for
	VariableDaily  int64             `json:"variable_daily"`  // typical day of non-subscription spending
	ProjectedSpend int64             `json:"projected_spend"` // spent + bills + variable × days left
	ProjectedEnd   int64             `json:"projected_end"`   // balance at month end
	SafeToSpend    int64             `json:"safe_to_spend"`   // for the rest of the month
	SafePerDay     int64             `json:"safe_per_day"`
	Upcoming       []map[string]any  `json:"upcoming"`
	Curve          []store.DayAmount `json:"curve"` // projected liquid balance per day, 30 days
	LastMonthByNow int64             `json:"last_month_by_now"`
}

func (s *Server) forecast(ctx context.Context) Forecast {
	st, _ := s.store.GetSettings(ctx)
	today := store.Today()
	from, to := periodBounds(today, "monthly", st.MonthStartDay, st.WeekStart)
	fc := Forecast{From: from, To: to, Currency: st.HomeCurrency, DaysLeft: store.DaysBetween(today, to), Upcoming: []map[string]any{}}
	accts, _, _ := s.accountsWithHome(ctx, false)
	ids := s.visibleIDs(ctx)
	everyday := map[int64]bool{}
	for _, a := range accts {
		// everyday money: what can be spent without touching savings
		if a.Everyday {
			fc.Balance += a.BalanceHome
			everyday[a.ID] = true
		}
	}
	days, _ := s.store.DailySums(ctx, ids, from, today)
	for _, d := range days {
		if d.Kind == "income" {
			fc.IncomeSoFar += d.Sum
		} else {
			fc.SpentSoFar += d.Sum
		}
	}
	// same point last month, for pace
	pf, _ := prevPeriod(from, "monthly", st.MonthStartDay, st.WeekStart)
	pdays, _ := s.store.DailySums(ctx, ids, pf, addDays(pf, store.DaysBetween(from, today)))
	for _, d := range pdays {
		if d.Kind == "expense" {
			fc.LastMonthByNow += d.Sum
		}
	}
	// typical variable spending: last 90 days, subscriptions left out
	// (only from everyday accounts — business or savings spending is not "a day")
	rows, _ := s.store.PatternRows(ctx, ids, addDays(today, -90))
	perDay := map[string]int64{}
	var variable int64
	for _, r := range rows {
		if r.Kind == "expense" && r.SubID == nil && r.Date < today && everyday[r.AccountID] {
			perDay[r.Date] += r.AmountHome
			variable += r.AmountHome
		}
	}
	span := 90
	if first := s.store.FirstTransactionDate(ctx, ids); first != "" && first > addDays(today, -90) {
		span = max(1, store.DaysBetween(first, today))
	}
	// one-off big purchases skew a daily average: cap any day at 3× the mean
	if mean := variable / int64(span); mean > 0 {
		variable = 0
		for _, v := range perDay {
			variable += min(v, 3*mean)
		}
	}
	fc.VariableDaily = variable / int64(span)
	subs, _ := s.store.ListSubscriptions(ctx, actorID(ctx))
	until := addDays(today, 30)
	byDay := map[string]int64{}
	for i := range subs {
		x := &subs[i]
		if !x.Active || x.Kind == "transfer" || (x.AccountID != nil && !everyday[*x.AccountID]) {
			continue
		}
		r, _ := s.rate(ctx, x.Currency, st.HomeCurrency, today)
		amt := currency.Convert(x.Amount, x.Currency, st.HomeCurrency, r)
		for _, d := range occurrences(x, until, 40) {
			if d < today {
				d = today // overdue still has to be paid
			}
			sign := int64(-1)
			if x.Kind == "income" {
				sign = 1
			}
			byDay[d] += sign * amt
			if d <= to {
				if x.Kind == "income" {
					fc.IncomeExpected += amt
				} else {
					fc.BillsLeft += amt
				}
				fc.Upcoming = append(fc.Upcoming, map[string]any{"id": x.ID, "name": x.Name, "date": d, "amount": amt, "kind": x.Kind, "icon": firstNonEmpty(x.Icon, x.CategoryIcon)})
			}
		}
	}
	sort.Slice(fc.Upcoming, func(i, j int) bool { return fc.Upcoming[i]["date"].(string) < fc.Upcoming[j]["date"].(string) })
	goals, _ := s.store.ListGoals(ctx, actorID(ctx))
	for i := range goals {
		g := &goals[i]
		if g.Archived || g.CompletedAt != "" {
			continue
		}
		s.goalStats(ctx, g)
		r, _ := s.rate(ctx, g.Currency, st.HomeCurrency, today)
		fc.GoalsNeed += currency.Convert(g.MonthlyNeed, g.Currency, st.HomeCurrency, r)
	}
	fc.ProjectedSpend = fc.SpentSoFar + fc.BillsLeft + fc.VariableDaily*int64(fc.DaysLeft)
	fc.ProjectedEnd = fc.Balance - fc.BillsLeft + fc.IncomeExpected - fc.VariableDaily*int64(fc.DaysLeft)
	// safe to spend: this month's money in, minus what is out and still owed
	// safe to spend: everyday money plus pay still coming, minus bills still
	// due this month and what the goals ask to put aside
	fc.SafeToSpend = fc.Balance + fc.IncomeExpected - fc.BillsLeft - fc.GoalsNeed
	if fc.DaysLeft >= 0 {
		fc.SafePerDay = fc.SafeToSpend / int64(fc.DaysLeft+1)
	}
	bal := fc.Balance
	for d := today; d <= until; d = addDay(d) {
		if d > today {
			bal -= fc.VariableDaily
		}
		bal += byDay[d]
		fc.Curve = append(fc.Curve, store.DayAmount{Date: d, Amount: bal})
	}
	return fc
}

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.forecast(r.Context()))
}

// Insight is one observation worth a glance.
type Insight struct {
	Kind  string `json:"kind"` // up | down | info | warn | sub
	Icon  string `json:"icon"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Link  string `json:"link,omitempty"`
}

func (s *Server) insights(ctx context.Context) []Insight {
	st, _ := s.store.GetSettings(ctx)
	ids := s.visibleIDs(ctx)
	today := store.Today()
	from, _ := periodBounds(today, "monthly", st.MonthStartDay, st.WeekStart)
	money := func(v int64) string { return majorString(v, st.HomeCurrency) }
	out := []Insight{}
	elapsed := store.DaysBetween(from, today) + 1

	// pace vs last month
	fc := s.forecast(ctx)
	if fc.LastMonthByNow > 0 && fc.SpentSoFar > 0 {
		diff := float64(fc.SpentSoFar-fc.LastMonthByNow) / float64(fc.LastMonthByNow) * 100
		if math.Abs(diff) >= 10 {
			k, word := "down", "less"
			if diff > 0 {
				k, word = "up", "more"
			}
			out = append(out, Insight{k, "📈", "Spending pace", fmt.Sprintf("%s spent so far this month — %.0f%% %s than by this day last month (%s).", money(fc.SpentSoFar), math.Abs(diff), word, money(fc.LastMonthByNow)), "/reports"})
		}
	}
	// categories running hot vs their 3-month average
	cur, _ := s.store.CategorySums(ctx, ids, "expense", from, today)
	pf, _ := prevPeriod(from, "monthly", st.MonthStartDay, st.WeekStart)
	pf, _ = prevPeriod(pf, "monthly", st.MonthStartDay, st.WeekStart)
	pf, _ = prevPeriod(pf, "monthly", st.MonthStartDay, st.WeekStart)
	past, _ := s.store.CategorySums(ctx, ids, "expense", pf, addDays(from, -1))
	avg := map[int64]int64{}
	for _, c := range past {
		avg[c.ID] = c.Sum / 3
	}
	minAmt := int64(20 * math.Pow10(currency.Decimals(st.HomeCurrency)))
	n := 0
	for _, c := range cur {
		a := avg[c.ID]
		if c.ID == 0 || a <= 0 || c.Sum < minAmt || n >= 3 {
			continue
		}
		if c.Sum > a*13/10 {
			out = append(out, Insight{"up", firstNonEmpty(c.Icon, "🔺"), c.Name + " is running high", fmt.Sprintf("%s so far this month vs. %s in an average month (%d%% of it after %d days).", money(c.Sum), money(a), c.Sum*100/a, elapsed), fmt.Sprintf("/transactions?category=%d&from=%s", c.ID, from)})
			n++
		}
	}
	// the biggest single expense this month
	big, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: ids, Kind: "expense", From: from, To: today, Limit: 300})
	var top *store.Transaction
	for i := range big {
		if big[i].SubscriptionID == nil && (top == nil || big[i].AmountHome > top.AmountHome) {
			top = &big[i]
		}
	}
	if top != nil && top.AmountHome >= minAmt {
		out = append(out, Insight{"info", firstNonEmpty(top.CategoryIcon, "💸"), "Biggest expense this month", fmt.Sprintf("%s — %s on %s.", top.Description, money(top.AmountHome), top.Date), fmt.Sprintf("/transactions/%d", top.ID)})
	}
	// subscriptions whose price moved
	subs, _ := s.store.ListSubscriptions(ctx, actorID(ctx))
	for _, x := range subs {
		if x.Active && x.Amount > 0 && x.LastAmount > 0 && x.AmountMax == 0 {
			lastIn := x.LastAmount
			if x.AccountID != nil {
				if a, err := s.store.GetAccount(ctx, *x.AccountID); err == nil && a.Currency != x.Currency {
					continue
				}
			}
			if d := float64(lastIn-x.Amount) / float64(x.Amount); math.Abs(d) > 0.02 {
				out = append(out, Insight{"sub", firstNonEmpty(x.Icon, x.CategoryIcon, "🔁"), x.Name + " changed price", fmt.Sprintf("Last payment was %s, the subscription expects %s (%+.0f%%).", majorString(lastIn, x.Currency), majorString(x.Amount, x.Currency), d*100), fmt.Sprintf("/subscriptions/%d", x.ID)})
			}
		}
	}
	// uncategorised
	_, tot, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: ids, Uncategorized: true, From: addDays(today, -60), Limit: 1})
	if tot.Count > 0 {
		out = append(out, Insight{"warn", "🏷️", "Uncategorised", fmt.Sprintf("%d transactions in the last 60 days have no category.", tot.Count), "/transactions?category=none"})
	}
	// savings rate last full month
	pfrom, pto := prevPeriod(from, "monthly", st.MonthStartDay, st.WeekStart)
	pd, _ := s.store.DailySums(ctx, ids, pfrom, pto)
	var pi, pe int64
	for _, d := range pd {
		if d.Kind == "income" {
			pi += d.Sum
		} else {
			pe += d.Sum
		}
	}
	if pi > 0 {
		rate := float64(pi-pe) / float64(pi) * 100
		out = append(out, Insight{"info", "🐷", "Last month you kept " + fmt.Sprintf("%.0f%%", rate), fmt.Sprintf("%s in, %s out (%s).", money(pi), money(pe), periodLabel(pfrom, pto, "monthly")), "/reports"})
	}
	return out
}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.insights(r.Context()))
}
