package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// maybeNotify runs the daily checks once the configured hour has passed.
func (s *Server) maybeNotify(ctx context.Context, st store.Settings) {
	if st.NotifyURL == "" {
		if hooks, _ := s.store.ListWebhooks(ctx, 0); len(hooks) == 0 {
			return
		}
	}
	now := time.Now()
	day := now.Format("2006-01-02")
	if s.notifyDay == "" {
		if v, _, ok := s.store.CacheGet(ctx, "notify:last_day"); ok {
			s.notifyDay = v
		}
	}
	if s.notifyDay != day && now.Hour() >= st.NotifyHour {
		s.notifyDay = day
		_ = s.store.CachePut(ctx, "notify:last_day", day)
		if _, err := s.runNotifications(ctx, false); err != nil {
			s.log.Warn("notifications", "err", err)
		}
	}
	// weekly digest: Monday at the notify hour, about the week that ended
	if st.NotifyDigest && now.Weekday() == time.Monday && now.Hour() >= st.NotifyHour {
		if s.digestDay == "" {
			if v, _, ok := s.store.CacheGet(ctx, "notify:digest_day"); ok {
				s.digestDay = v
			}
		}
		if s.digestDay != day {
			s.digestDay = day
			_ = s.store.CachePut(ctx, "notify:digest_day", day)
			if err := s.sendDigest(ctx); err != nil {
				s.log.Warn("weekly digest", "err", err)
			}
		}
	}
}

// runNotifications pushes due subscriptions and budget alerts. With dry it
// only reports what it would send.
func (s *Server) runNotifications(ctx context.Context, dry bool) ([]notify.Message, error) {
	c := s.clients(ctx)
	st := c.st
	var msgs []notify.Message
	link := func(p string) string {
		if s.cfg.BaseURL == "" {
			return ""
		}
		return s.cfg.BaseURL + p
	}
	subs, err := s.store.ListSubscriptions(ctx, 0)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		x := &subs[i]
		if !x.Active || x.NotifyDays < 0 || x.NextDate == "" || x.NotifiedFor == x.NextDate {
			continue
		}
		s.decorate(ctx, x, st.HomeCurrency)
		if x.Status == "ended" || x.DaysLeft > x.NotifyDays {
			continue
		}
		when := "today"
		switch {
		case x.DaysLeft < 0:
			when = fmt.Sprintf("%d days overdue", -x.DaysLeft)
		case x.DaysLeft == 1:
			when = "tomorrow"
		case x.DaysLeft > 1:
			when = fmt.Sprintf("in %d days (%s)", x.DaysLeft, x.DueDate)
		}
		body := fmt.Sprintf("%s %s", majorString(x.Amount, x.Currency), when)
		if x.Mode == "auto" {
			body += " — it will be booked automatically"
		}
		if x.AccountName != "" {
			body += " from " + x.AccountName
		}
		m := notify.Message{Title: "💳 " + x.Name, Body: body, Priority: "info", Tags: []string{"credit_card"}, Link: link(fmt.Sprintf("/subscriptions/%d", x.ID))}
		if x.DaysLeft < 0 {
			m.Priority = "warning"
		}
		msgs = append(msgs, m)
		if !dry {
			x.NotifiedFor = x.NextDate
			_ = s.store.UpdateSubscription(ctx, x)
			s.emit(ctx, x.OwnerID, "subscription.due", x.Name, map[string]any{"subscription": x, "message": m.Title + " — " + m.Body})
		}
	}
	budgets, _ := s.store.ListBudgets(ctx, 0)
	for i := range budgets {
		b := &budgets[i]
		if b.Archived {
			continue
		}
		bs := s.budgetStatus(ctx, b, store.Today(), st)
		if bs.State == "ok" {
			continue
		}
		key := bs.From + ":" + bs.State
		if b.Notified == key || (bs.State == "warn" && b.Notified == bs.From+":over") {
			continue
		}
		var m notify.Message
		if bs.State == "over" {
			m = notify.Message{Title: "🚨 " + b.Name + " budget is over", Priority: "warning",
				Body: fmt.Sprintf("%s spent of %s (%s over) — %s.", majorString(bs.Spent, st.HomeCurrency), majorString(bs.Available, st.HomeCurrency), majorString(-bs.Left, st.HomeCurrency), bs.PeriodLabel)}
		} else {
			m = notify.Message{Title: "⚠️ " + b.Name + " budget", Priority: "info",
				Body: fmt.Sprintf("%.0f%% used with %.0f%% of %s gone — %s left (%s a day).", bs.Pct, bs.ElapsedPct, bs.PeriodLabel, majorString(bs.Left, st.HomeCurrency), majorString(bs.DailyLeft, st.HomeCurrency))}
		}
		m.Tags, m.Link = []string{"money_with_wings"}, link("/budgets")
		msgs = append(msgs, m)
		if !dry {
			b.Notified = key
			_ = s.store.UpdateBudget(ctx, b)
			b.Status = bs
			s.emit(ctx, b.OwnerID, "budget.alert", b.Name, map[string]any{"budget": b, "message": m.Title + " — " + m.Body})
		}
	}
	if dry || !c.notify.Configured() {
		return msgs, nil
	}
	for _, m := range msgs {
		if err := c.notify.Send(ctx, m); err != nil {
			return msgs, err
		}
	}
	return msgs, nil
}

// sendDigest pushes last week's numbers.
func (s *Server) sendDigest(ctx context.Context) error {
	c := s.clients(ctx)
	if !c.notify.Configured() {
		return nil
	}
	owner, err := s.importOwner(ctx)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, userKey, owner)
	m, ok := s.digest(ctx)
	if !ok {
		return nil
	}
	return c.notify.Send(ctx, m)
}

func (s *Server) digest(ctx context.Context) (notify.Message, bool) {
	st, _ := s.store.GetSettings(ctx)
	ids := s.visibleIDs(ctx)
	end := addDays(store.Today(), -1)
	start := addDays(end, -6)
	days, _ := s.store.DailySums(ctx, ids, start, end)
	var inc, exp int64
	for _, d := range days {
		if d.Kind == "income" {
			inc += d.Sum
		} else {
			exp += d.Sum
		}
	}
	if inc == 0 && exp == 0 {
		return notify.Message{}, false
	}
	m := func(v int64) string { return majorString(v, st.HomeCurrency) }
	// the 8 weeks before, for "usual"
	prev, _ := s.store.DailySums(ctx, ids, addDays(start, -56), addDays(start, -1))
	var pexp int64
	for _, d := range prev {
		if d.Kind == "expense" {
			pexp += d.Sum
		}
	}
	usual := pexp / 8
	var b strings.Builder
	fmt.Fprintf(&b, "Spent %s", m(exp))
	if usual > 0 {
		fmt.Fprintf(&b, " (usual week %s)", m(usual))
	}
	if inc > 0 {
		fmt.Fprintf(&b, ", received %s", m(inc))
	}
	b.WriteString(".")
	cats, _ := s.store.CategorySums(ctx, ids, "expense", start, end)
	if len(cats) > 0 {
		var parts []string
		for i, c := range cats {
			if i >= 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s %s", firstNonEmpty(c.Name, "other"), m(c.Sum)))
		}
		b.WriteString("\nTop: " + strings.Join(parts, ", "))
	}
	fc := s.forecast(ctx)
	var due []string
	week := addDays(store.Today(), 7)
	sort.Slice(fc.Upcoming, func(i, j int) bool { return fc.Upcoming[i]["date"].(string) < fc.Upcoming[j]["date"].(string) })
	for _, u := range fc.Upcoming {
		if u["date"].(string) <= week && u["kind"] != "income" {
			due = append(due, fmt.Sprintf("%s %s", u["name"], m(u["amount"].(int64))))
		}
	}
	if len(due) > 0 {
		b.WriteString("\nThis week: " + strings.Join(due, ", "))
	}
	link := ""
	if s.cfg.BaseURL != "" {
		link = s.cfg.BaseURL + "/reports"
	}
	return notify.Message{Title: "📊 Your week in money", Body: b.String(), Priority: "info", Tags: []string{"bar_chart"}, Link: link}, true
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	c := s.clients(r.Context())
	if !c.notify.Configured() {
		writeErr(w, http.StatusBadRequest, "set a notification URL first")
		return
	}
	if err := c.notify.Send(r.Context(), notify.Message{Title: "Pocket Money", Body: "Notifications work. Payment reminders and budget alerts will arrive here.", Priority: "info", Tags: []string{"moneybag"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleNotifyRun previews (dry=1) or sends the reminder check now.
func (s *Server) handleNotifyRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dry := queryBool(r, "dry")
	msgs, err := s.runNotifications(r.Context(), dry)
	if queryBool(r, "digest") {
		if m, ok := s.digest(r.Context()); ok {
			msgs = append(msgs, m)
			if !dry {
				c := s.clients(r.Context())
				if c.notify.Configured() {
					err = c.notify.Send(r.Context(), m)
				}
			}
		}
	}
	out := map[string]any{"messages": msgs, "sent": !dry}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
