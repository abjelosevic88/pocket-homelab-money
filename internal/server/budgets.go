package server

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// periodBounds returns the period around date. A "month" may start on any
// day (payday months); weeks start on Monday or Sunday.
func periodBounds(date, period string, monthStart, weekStart int) (string, string) {
	t, err := store.ParseDate(date)
	if err != nil {
		t, _ = store.ParseDate(store.Today())
	}
	switch period {
	case "weekly":
		back := (int(t.Weekday()) - weekStart + 7) % 7
		f := t.AddDate(0, 0, -back)
		return f.Format("2006-01-02"), f.AddDate(0, 0, 6).Format("2006-01-02")
	case "yearly":
		return t.Format("2006") + "-01-01", t.Format("2006") + "-12-31"
	}
	if monthStart < 1 || monthStart > 28 {
		monthStart = 1
	}
	f := time.Date(t.Year(), t.Month(), monthStart, 0, 0, 0, 0, time.UTC)
	if t.Day() < monthStart {
		f = f.AddDate(0, -1, 0)
	}
	return f.Format("2006-01-02"), f.AddDate(0, 1, -1).Format("2006-01-02")
}

// prevPeriod returns the period just before the one starting at from.
func prevPeriod(from, period string, monthStart, weekStart int) (string, string) {
	t, _ := store.ParseDate(from)
	return periodBounds(t.AddDate(0, 0, -1).Format("2006-01-02"), period, monthStart, weekStart)
}

var monNames = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func periodLabel(from, to, period string) string {
	f, _ := store.ParseDate(from)
	switch period {
	case "weekly":
		return "Week of " + f.Format("2 Jan")
	case "yearly":
		return f.Format("2006")
	}
	if f.Day() == 1 {
		return monNames[f.Month()-1] + " " + f.Format("2006")
	}
	t, _ := store.ParseDate(to)
	return f.Format("2 Jan") + " – " + t.Format("2 Jan")
}

// budgetStatus computes how a budget is doing in the period around date.
func (s *Server) budgetStatus(ctx context.Context, b *store.Budget, date string, st store.Settings) *store.BudgetStatus {
	accts, _ := s.store.VisibleAccountIDs(ctx, b.OwnerID)
	cats, _ := s.store.CategoryChildren(ctx, b.CategoryIDs)
	from, to := periodBounds(date, b.Period, st.MonthStartDay, st.WeekStart)
	spent, n, _ := s.store.SpentHome(ctx, accts, cats, b.TagNames, from, to)
	bs := &store.BudgetStatus{From: from, To: to, Spent: spent, TxCount: n, PeriodLabel: periodLabel(from, to, b.Period)}
	start := b.StartDate
	if start == "" {
		start = b.CreatedAt[:10]
	}
	// previous periods: history (6) and rollover (up to 24, from the budget's start)
	pf := from
	hist := []int64{}
	for i := 0; i < 24; i++ {
		f, t := prevPeriod(pf, b.Period, st.MonthStartDay, st.WeekStart)
		pf = f
		if i >= 6 && (!b.Rollover || t < start) {
			break
		}
		sp, _, _ := s.store.SpentHome(ctx, accts, cats, b.TagNames, f, t)
		if i < 6 {
			hist = append([]int64{sp}, hist...)
		}
		if b.Rollover && t >= start {
			bs.Carry += b.Amount - sp
		}
	}
	bs.History = hist
	bs.Available = b.Amount + bs.Carry
	bs.Left = bs.Available - spent
	if bs.Available > 0 {
		bs.Pct = math.Round(float64(spent)/float64(bs.Available)*1000) / 10
	} else if spent > 0 {
		bs.Pct = 100
	}
	today := store.Today()
	total := float64(store.DaysBetween(from, to) + 1)
	gone := float64(store.DaysBetween(from, today) + 1)
	if today > to {
		gone = total
	}
	if today < from {
		gone = 0
	}
	bs.ElapsedPct = math.Round(gone/total*1000) / 10
	if gone > 0 {
		bs.Projected = int64(float64(spent) / gone * total)
	}
	if rem := total - gone + 1; rem > 0 && today <= to && bs.Left > 0 {
		bs.DailyLeft = int64(float64(bs.Left) / rem)
	}
	switch {
	case spent > bs.Available:
		bs.State = "over"
	case bs.Pct >= float64(b.AlertPct) || (gone/total > 0.2 && bs.Projected > bs.Available):
		bs.State = "warn"
	default:
		bs.State = "ok"
	}
	return bs
}

func (s *Server) handleListBudgets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bs, err := s.store.ListBudgets(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "budgets")
		return
	}
	st, _ := s.store.GetSettings(ctx)
	date := r.URL.Query().Get("date")
	if date == "" {
		date = store.Today()
	}
	var total, spent int64
	for i := range bs {
		if bs[i].Archived && !queryBool(r, "archived") {
			continue
		}
		bs[i].Status = s.budgetStatus(ctx, &bs[i], date, st)
		if !bs[i].Archived && bs[i].Period == "monthly" {
			total += bs[i].Status.Available
			spent += bs[i].Status.Spent
		}
	}
	// what was spent this month outside every budget
	from, to := periodBounds(date, "monthly", st.MonthStartDay, st.WeekStart)
	accts := s.visibleIDs(ctx)
	all, _, _ := s.store.SpentHome(ctx, accts, nil, nil, from, to)
	writeJSON(w, http.StatusOK, map[string]any{"items": bs, "currency": st.HomeCurrency, "month_total": total, "month_spent": spent, "month_all_spent": all,
		"month_from": from, "month_to": to, "month_label": periodLabel(from, to, "monthly")})
}

func (s *Server) ownBudget(w http.ResponseWriter, r *http.Request) *store.Budget {
	b, err := s.store.GetBudget(r.Context(), idParam(r, "id"))
	if err != nil || b.OwnerID != actorID(r.Context()) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return b
}

func (s *Server) handleGetBudget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.ownBudget(w, r)
	if b == nil {
		return
	}
	st, _ := s.store.GetSettings(ctx)
	date := r.URL.Query().Get("date")
	if date == "" {
		date = store.Today()
	}
	b.Status = s.budgetStatus(ctx, b, date, st)
	cats, _ := s.store.CategoryChildren(ctx, b.CategoryIDs)
	f := store.TxFilter{AccountIDs: s.visibleIDs(ctx), Kind: "expense", CategoryIDs: cats, From: b.Status.From, To: b.Status.To, Limit: 500}
	if len(b.TagNames) == 1 {
		f.Tag = b.TagNames[0]
	}
	txs, _, _ := s.store.ListTransactions(ctx, f)
	// daily cumulative spend for the burn chart
	in := map[int64]bool{}
	for _, id := range cats {
		in[id] = true
	}
	cum := map[string]int64{}
	for _, t := range txs {
		cum[t.Date] += partHome(&t, in)
	}
	days := []store.DayAmount{}
	run := int64(0)
	for d := b.Status.From; d <= b.Status.To; d = addDay(d) {
		run += cum[d]
		days = append(days, store.DayAmount{Date: d, Amount: run})
	}
	writeJSON(w, http.StatusOK, map[string]any{"budget": b, "transactions": txs, "burn": days})
}

func addDay(d string) string {
	t, _ := store.ParseDate(d)
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

type budgetIn struct {
	Name        *string   `json:"name"`
	Amount      *int64    `json:"amount"`
	Period      *string   `json:"period"`
	Rollover    *bool     `json:"rollover"`
	CategoryIDs *[]int64  `json:"category_ids"`
	TagNames    *[]string `json:"tag_names"`
	StartDate   *string   `json:"start_date"`
	AlertPct    *int      `json:"alert_pct"`
	Color       *string   `json:"color"`
	Icon        *string   `json:"icon"`
	Archived    *bool     `json:"archived"`
	SortOrder   *int      `json:"sort_order"`
}

func (in *budgetIn) apply(b *store.Budget) string {
	if in.Name != nil {
		b.Name = strings.TrimSpace(*in.Name)
	}
	if in.Amount != nil {
		b.Amount = *in.Amount
	}
	if in.Period != nil {
		b.Period = *in.Period
	}
	if in.Rollover != nil {
		b.Rollover = *in.Rollover
	}
	if in.CategoryIDs != nil {
		b.CategoryIDs = *in.CategoryIDs
	}
	if in.TagNames != nil {
		b.TagNames = cleanTags(*in.TagNames)
	}
	if in.StartDate != nil {
		b.StartDate = *in.StartDate
	}
	if in.AlertPct != nil {
		b.AlertPct = *in.AlertPct
	}
	if in.Color != nil {
		b.Color = *in.Color
	}
	if in.Icon != nil {
		b.Icon = *in.Icon
	}
	if in.Archived != nil {
		b.Archived = *in.Archived
	}
	if in.SortOrder != nil {
		b.SortOrder = *in.SortOrder
	}
	switch {
	case b.Name == "":
		return "name is required"
	case b.Amount <= 0:
		return "amount must be more than zero"
	case !store.Valid(store.BudgetPeriods, b.Period):
		return "period must be weekly, monthly or yearly"
	case !validDate(b.StartDate):
		return "start_date must be YYYY-MM-DD"
	}
	if b.AlertPct <= 0 || b.AlertPct > 100 {
		b.AlertPct = 80
	}
	return ""
}

func (s *Server) handleCreateBudget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in budgetIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	b := &store.Budget{OwnerID: actorID(ctx), Period: "monthly", AlertPct: 80, CategoryIDs: []int64{}, TagNames: []string{}}
	if msg := in.apply(b); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if b.StartDate == "" {
		st, _ := s.store.GetSettings(ctx)
		b.StartDate, _ = periodBounds(store.Today(), b.Period, st.MonthStartDay, st.WeekStart)
	}
	if err := s.store.CreateBudget(ctx, b); err != nil {
		s.fail(w, err, "create budget")
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) handleUpdateBudget(w http.ResponseWriter, r *http.Request) {
	b := s.ownBudget(w, r)
	if b == nil {
		return
	}
	var in budgetIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(b); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateBudget(r.Context(), b); err != nil {
		s.fail(w, err, "update budget")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleDeleteBudget(w http.ResponseWriter, r *http.Request) {
	b := s.ownBudget(w, r)
	if b == nil {
		return
	}
	if err := s.store.DeleteBudget(r.Context(), b.ID); err != nil {
		s.fail(w, err, "delete budget")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSuggestBudgets proposes monthly budgets from the last three full
// months of spending, rounded up to a friendly number.
func (s *Server) handleSuggestBudgets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	cur, _ := periodBounds(store.Today(), "monthly", st.MonthStartDay, st.WeekStart)
	f1, _ := prevPeriod(cur, "monthly", st.MonthStartDay, st.WeekStart)
	f2, _ := prevPeriod(f1, "monthly", st.MonthStartDay, st.WeekStart)
	f3, _ := prevPeriod(f2, "monthly", st.MonthStartDay, st.WeekStart)
	to := addDays(cur, -1)
	sums, err := s.store.CategorySums(ctx, s.visibleIDs(ctx), "expense", f3, to)
	if err != nil {
		s.fail(w, err, "suggest")
		return
	}
	existing, _ := s.store.ListBudgets(ctx, actorID(ctx))
	has := map[int64]bool{}
	for _, b := range existing {
		for _, id := range b.CategoryIDs {
			has[id] = true
		}
	}
	months := 3.0
	if first := s.store.FirstTransactionDate(ctx, s.visibleIDs(ctx)); first > f3 {
		months = math.Max(1, float64(store.DaysBetween(first, to))/30.44)
	}
	type sug struct {
		CategoryID int64  `json:"category_id"`
		Name       string `json:"name"`
		Icon       string `json:"icon"`
		Average    int64  `json:"average"`
		Suggested  int64  `json:"suggested"`
	}
	out := []sug{}
	dec := currency.Decimals(st.HomeCurrency)
	for _, g := range sums {
		if g.ID == 0 || has[g.ID] {
			continue
		}
		avg := float64(g.Sum) / months
		if avg < math.Pow10(dec)*5 {
			continue
		}
		out = append(out, sug{g.ID, g.Name, g.Icon, int64(avg), niceCeil(avg, dec)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Average > out[j].Average })
	if len(out) > 12 {
		out = out[:12]
	}
	writeJSON(w, http.StatusOK, out)
}

// niceCeil rounds minor units up to 5/10/50/100… major units.
func niceCeil(minor float64, decimals int) int64 {
	major := minor / math.Pow10(decimals)
	step := 5.0
	switch {
	case major > 2000:
		step = 100
	case major > 500:
		step = 50
	case major > 100:
		step = 10
	}
	return int64(math.Ceil(major*1.05/step) * step * math.Pow10(decimals))
}

func addDays(d string, n int) string {
	t, _ := store.ParseDate(d)
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// ---- goals ----

func (s *Server) goalStats(ctx context.Context, g *store.Goal) {
	if g.AccountID != nil {
		if a, err := s.store.GetAccount(ctx, *g.AccountID); err == nil {
			r, _ := s.rate(ctx, a.Currency, g.Currency, store.Today())
			g.Saved = currency.Convert(a.Balance, a.Currency, g.Currency, r)
			// pace = balance change over the last 90 days
			bal, _ := s.store.Balances(ctx, addDays(store.Today(), -90))
			then := currency.Convert(a.OpeningBalance+bal[a.ID].Delta, a.Currency, g.Currency, r)
			g.AvgPerMonth = int64(float64(g.Saved-then) / 3)
		}
	} else {
		entries, _ := s.store.GoalEntries(ctx, g.ID)
		cut := addDays(store.Today(), -90)
		var recent int64
		first := store.Today()
		for _, e := range entries {
			if e.Date >= cut {
				recent += e.Amount
			}
			if e.Date < first {
				first = e.Date
			}
		}
		months := math.Min(3, math.Max(1, float64(store.DaysBetween(first, store.Today()))/30.44))
		g.AvgPerMonth = int64(float64(recent) / months)
	}
	if g.Target > 0 {
		g.Pct = math.Round(math.Min(100, float64(g.Saved)/float64(g.Target)*100)*10) / 10
	}
	left := g.Target - g.Saved
	if g.TargetDate != "" && left > 0 {
		days := store.DaysBetween(store.Today(), g.TargetDate)
		g.MonthsLeft = int(math.Ceil(float64(days) / 30.44))
		if g.MonthsLeft < 1 {
			g.MonthsLeft = 1
		}
		g.MonthlyNeed = int64(math.Ceil(float64(left) / float64(g.MonthsLeft)))
		on := g.AvgPerMonth >= g.MonthlyNeed
		g.OnTrack = &on
	}
	if left > 0 && g.AvgPerMonth > 0 {
		months := float64(left) / float64(g.AvgPerMonth)
		if months < 600 {
			g.ETA = time.Now().AddDate(0, int(math.Ceil(months)), 0).Format("2006-01-02")
		}
	}
}

func (s *Server) handleListGoals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gs, err := s.store.ListGoals(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "goals")
		return
	}
	for i := range gs {
		s.goalStats(ctx, &gs[i])
	}
	writeJSON(w, http.StatusOK, gs)
}

func (s *Server) ownGoal(w http.ResponseWriter, r *http.Request) *store.Goal {
	g, err := s.store.GetGoal(r.Context(), idParam(r, "id"))
	if err != nil || g.OwnerID != actorID(r.Context()) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return g
}

func (s *Server) handleGetGoal(w http.ResponseWriter, r *http.Request) {
	if g := s.ownGoal(w, r); g != nil {
		s.goalStats(r.Context(), g)
		writeJSON(w, http.StatusOK, g)
	}
}

type goalIn struct {
	Name       *string `json:"name"`
	Target     *int64  `json:"target"`
	Currency   *string `json:"currency"`
	TargetDate *string `json:"target_date"`
	AccountID  *int64  `json:"account_id"`
	Icon       *string `json:"icon"`
	Color      *string `json:"color"`
	Notes      *string `json:"notes"`
	Archived   *bool   `json:"archived"`
	Completed  *bool   `json:"completed"`
}

func (s *Server) applyGoal(ctx context.Context, in *goalIn, g *store.Goal) string {
	if in.Name != nil {
		g.Name = strings.TrimSpace(*in.Name)
	}
	if in.Target != nil {
		g.Target = *in.Target
	}
	if in.Currency != nil {
		g.Currency = strings.ToUpper(*in.Currency)
	}
	if in.TargetDate != nil {
		g.TargetDate = *in.TargetDate
	}
	if in.AccountID != nil {
		g.AccountID = idOrNil(in.AccountID)
	}
	if in.Icon != nil {
		g.Icon = *in.Icon
	}
	if in.Color != nil {
		g.Color = *in.Color
	}
	if in.Notes != nil {
		g.Notes = *in.Notes
	}
	if in.Archived != nil {
		g.Archived = *in.Archived
	}
	if in.Completed != nil {
		if *in.Completed && g.CompletedAt == "" {
			g.CompletedAt = store.Today()
		} else if !*in.Completed {
			g.CompletedAt = ""
		}
	}
	switch {
	case g.Name == "":
		return "name is required"
	case g.Target <= 0:
		return "target must be more than zero"
	case !currency.Valid(g.Currency):
		return "unknown currency"
	case !validDate(g.TargetDate):
		return "target_date must be YYYY-MM-DD"
	case g.AccountID != nil && !s.canSee(ctx, *g.AccountID):
		return "unknown account"
	}
	return ""
}

func (s *Server) handleCreateGoal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in goalIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx)
	g := &store.Goal{OwnerID: actorID(ctx), Currency: st.HomeCurrency, Icon: "🎯"}
	if msg := s.applyGoal(ctx, &in, g); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateGoal(ctx, g); err != nil {
		s.fail(w, err, "create goal")
		return
	}
	s.goalStats(ctx, g)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleUpdateGoal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	g := s.ownGoal(w, r)
	if g == nil {
		return
	}
	var in goalIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := s.applyGoal(ctx, &in, g); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateGoal(ctx, g); err != nil {
		s.fail(w, err, "update goal")
		return
	}
	s.goalStats(ctx, g)
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	g := s.ownGoal(w, r)
	if g == nil {
		return
	}
	if err := s.store.DeleteGoal(r.Context(), g.ID); err != nil {
		s.fail(w, err, "delete goal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAddGoalEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	g := s.ownGoal(w, r)
	if g == nil {
		return
	}
	var e store.GoalEntry
	if err := decode(r, &e); err != nil || e.Amount == 0 {
		writeErr(w, http.StatusBadRequest, "amount is required")
		return
	}
	if e.Date == "" {
		e.Date = store.Today()
	}
	e.GoalID = g.ID
	if err := s.store.AddGoalEntry(ctx, &e); err != nil {
		s.fail(w, err, "goal entry")
		return
	}
	g, _ = s.store.GetGoal(ctx, g.ID)
	s.goalStats(ctx, g)
	if g.Saved >= g.Target && g.CompletedAt == "" {
		g.CompletedAt = store.Today()
		_ = s.store.UpdateGoal(ctx, g)
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleDeleteGoalEntry(w http.ResponseWriter, r *http.Request) {
	g := s.ownGoal(w, r)
	if g == nil {
		return
	}
	if err := s.store.DeleteGoalEntry(r.Context(), g.ID, idParam(r, "entryID")); err != nil {
		s.fail(w, err, "delete entry")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// partHome is what of a transaction falls into a set of categories (all of it
// when the set is empty or the transaction is not split).
func partHome(t *store.Transaction, cats map[int64]bool) int64 {
	if len(cats) == 0 || len(t.Splits) == 0 {
		return t.AmountHome
	}
	var v int64
	for _, sp := range t.Splits {
		if sp.CategoryID != nil && cats[*sp.CategoryID] {
			v += sp.AmountHome
		}
	}
	return v
}
