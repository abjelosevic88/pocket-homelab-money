package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/firefly"
	"github.com/abjelosevic88/pocket-homelab-money/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-money/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

func majorString(minor int64, code string) string {
	dec := currency.Decimals(code)
	return strconv.FormatFloat(float64(minor)/math.Pow10(dec), 'f', dec, 64) + " " + code
}

func (s *Server) publicSettings(st store.Settings) map[string]any {
	model := st.AIModel
	if model == "" {
		model = ai.DefaultModel
	}
	return map[string]any{
		"title": st.Title, "home_currency": st.HomeCurrency, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"week_start": st.WeekStart, "month_start_day": st.MonthStartDay, "fx_auto": st.FXAuto, "fx_provider": s.cfg.FXProvider,
		"default_account_id": st.DefaultAcct, "quick_lang": st.QuickLang,
		"firefly_url": st.FireflyURL, "firefly_token": mask(st.FireflyToken), "firefly_configured": st.FireflyURL != "" && st.FireflyToken != "",
		"firefly_mirror": st.FireflyMirror, "pico_url": st.PicoURL,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_hour": st.NotifyHour, "notify_digest": st.NotifyDigest, "notify_configured": st.NotifyURL != "",
		"ai_url": st.AIURL, "ai_key": mask(st.AIKey), "ai_model": st.AIModel, "ai_model_effective": model, "ai_transcribe_model": st.AITranscribe,
		"ai_language": st.AILanguage, "ai_context": st.AIContext, "ai_configured": st.AIKey != "",
		"travel_url": st.TravelURL, "travel_public_url": st.TravelPublicURL, "travel_token": mask(st.TravelToken), "travel_configured": st.TravelURL != "" && st.TravelToken != "",
		"travel_auto_tag":  st.TravelAutoTag,
		"max_upload_bytes": s.cfg.MaxUploadBytes, "setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(st))
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.requireAdmin(w, r) {
		return
	}
	cur, err := s.store.GetSettings(ctx)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	oldHome := cur.HomeCurrency
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["home_currency"] {
		if !currency.Valid(in.HomeCurrency) {
			writeErr(w, http.StatusBadRequest, "unknown currency")
			return
		}
		cur.HomeCurrency = strings.ToUpper(in.HomeCurrency)
	}
	str := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	flag := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	str("date_format", &cur.DateFormat, in.DateFormat)
	str("time_format", &cur.TimeFormat, in.TimeFormat)
	str("quick_lang", &cur.QuickLang, in.QuickLang)
	if keys["week_start"] && (in.WeekStart == 0 || in.WeekStart == 1) {
		cur.WeekStart = in.WeekStart
	}
	if keys["month_start_day"] && in.MonthStartDay >= 1 && in.MonthStartDay <= 28 {
		cur.MonthStartDay = in.MonthStartDay
	}
	if keys["default_account_id"] {
		cur.DefaultAcct = in.DefaultAcct
	}
	flag("fx_auto", &cur.FXAuto, in.FXAuto)
	url("firefly_url", &cur.FireflyURL, in.FireflyURL)
	secret("firefly_token", &cur.FireflyToken, in.FireflyToken)
	flag("firefly_mirror", &cur.FireflyMirror, in.FireflyMirror)
	url("pico_url", &cur.PicoURL, in.PicoURL)
	str("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	if keys["notify_hour"] && in.NotifyHour >= 0 && in.NotifyHour <= 23 {
		cur.NotifyHour = in.NotifyHour
	}
	flag("notify_digest", &cur.NotifyDigest, in.NotifyDigest)
	url("ai_url", &cur.AIURL, in.AIURL)
	secret("ai_key", &cur.AIKey, in.AIKey)
	str("ai_model", &cur.AIModel, in.AIModel)
	str("ai_transcribe_model", &cur.AITranscribe, in.AITranscribe)
	str("ai_language", &cur.AILanguage, in.AILanguage)
	if keys["ai_context"] {
		cur.AIContext = strings.TrimSpace(in.AIContext)
	}
	url("travel_url", &cur.TravelURL, in.TravelURL)
	url("travel_public_url", &cur.TravelPublicURL, in.TravelPublicURL)
	secret("travel_token", &cur.TravelToken, in.TravelToken)
	flag("travel_auto_tag", &cur.TravelAutoTag, in.TravelAutoTag)
	if err := s.store.SaveSettings(ctx, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	_ = s.store.CacheDelete(ctx, "travel:")
	if cur.HomeCurrency != oldHome {
		go s.recomputeHome(context.Background())
	}
	writeJSON(w, http.StatusOK, s.publicSettings(cur))
}

// recomputeHome re-values every transaction in a new home currency.
func (s *Server) recomputeHome(ctx context.Context) {
	ids, err := s.store.AllTransactionIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		t, err := s.store.GetTransaction(ctx, id)
		if err != nil {
			continue
		}
		s.fillHome(ctx, t)
		_ = s.store.UpdateHomeAmount(ctx, id, t.AmountHome, t.FXRate)
	}
	s.log.Info("home currency changed, transactions re-valued", "count", len(ids))
}

func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
		Model  string `json:"model"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(in.URL, "/")
	}
	var msg string
	var err error
	switch in.Which {
	case "firefly":
		c := firefly.New(urlOr(st.FireflyURL), keyOr(st.FireflyToken), 20*time.Second)
		var v string
		v, err = c.About(ctx)
		if err == nil {
			accts, _ := c.Accounts(ctx, "")
			msg = fmt.Sprintf("Firefly III %s · %d accounts", v, len(accts))
		}
	case "pico":
		c := firefly.New(st.FireflyURL, st.FireflyToken, 20*time.Second)
		var tpls []firefly.PicoTemplate
		tpls, err = c.PicoTemplates(ctx, urlOr(st.PicoURL))
		msg = fmt.Sprintf("Firefly Pico reachable · %d templates", len(tpls))
	case "notify":
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Money", Body: "Notifications work. Payment reminders and budget alerts will arrive here.", Priority: "info", Tags: []string{"moneybag"}})
		msg = "Test message sent via " + n.Format
	case "travel":
		var v string
		v, err = pocket.New(urlOr(st.TravelURL), keyOr(st.TravelToken), 15*time.Second).Health(ctx)
		msg = "Pocket Travel " + v
	case "ai":
		model := in.Model
		if model == "" {
			model = st.AIModel
		}
		c := ai.New(urlOr(st.AIURL), keyOr(st.AIKey), model, "")
		var ids []string
		ids, err = c.Models(ctx)
		msg = fmt.Sprintf("API key works · %d models, using %s", len(ids), c.Model)
	default:
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size, files int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	_ = filepath.WalkDir(s.cfg.FilesDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				files += fi.Size()
			}
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size, "files_bytes": files,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.Counts(r.Context()),
	})
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-money-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	defer os.Remove(dst)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	http.ServeFile(w, r, dst)
}

// handleMeta returns the static lists the UI needs once.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"account_kinds": store.AccountKinds, "transaction_kinds": store.TransactionKinds, "frequencies": store.Frequencies,
		"subscription_modes": store.SubscriptionModes, "budget_periods": store.BudgetPeriods,
	})
}

func (s *Server) handleCurrencies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currency.All())
}

func (s *Server) handleFX(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, date := strings.ToUpper(q.Get("from")), strings.ToUpper(q.Get("to")), q.Get("date")
	if !currency.Valid(from) || !currency.Valid(to) {
		writeErr(w, http.StatusBadRequest, "unknown currency")
		return
	}
	rate, src := s.rate(r.Context(), from, to, date)
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "date": date, "rate": rate, "source": src})
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListActivity(r.Context(), 100)
	if err != nil {
		s.fail(w, err, "activity")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- Pocket Travel: the running trip's tag on new expenses ----

func (s *Server) tripList(ctx context.Context) []pocket.Trip {
	c := s.clients(ctx)
	if !c.travel.Configured() {
		return nil
	}
	if val, at, ok := s.store.CacheGet(ctx, "travel:trips"); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 30*time.Minute {
			var out []pocket.Trip
			if json.Unmarshal([]byte(val), &out) == nil {
				return out
			}
		}
	}
	trips, err := c.travel.Trips(ctx)
	if err != nil {
		s.log.Warn("pocket travel", "err", err)
		return nil
	}
	b, _ := json.Marshal(trips)
	_ = s.store.CachePut(ctx, "travel:trips", string(b))
	return trips
}

func (s *Server) addTripTag(ctx context.Context, t *store.Transaction) {
	st, _ := s.store.GetSettings(ctx)
	if !st.TravelAutoTag || st.TravelURL == "" {
		return
	}
	if trip := pocket.TripOn(s.tripList(ctx), t.Date); trip != nil && trip.FireflyTag != "" {
		t.Tags = cleanTags(append(t.Tags, trip.FireflyTag))
	}
}

func (s *Server) handleTravelTrips(w http.ResponseWriter, r *http.Request) {
	trips := s.tripList(r.Context())
	if trips == nil {
		trips = []pocket.Trip{}
	}
	st, _ := s.store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"trips": trips, "public_url": firstNonEmpty(st.TravelPublicURL, st.TravelURL), "running": pocket.TripOn(trips, store.Today())})
}

// ---- home + dashboard ----

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx)
	accts, home, err := s.accountsWithHome(ctx, false)
	if err != nil {
		s.fail(w, err, "accounts")
		return
	}
	var net, liquid int64
	for _, a := range accts {
		if a.IncludeNetWorth {
			net += a.BalanceHome
		}
		if a.Everyday {
			liquid += a.BalanceHome
		}
	}
	ids := s.visibleIDs(ctx)
	from, to := periodBounds(store.Today(), "monthly", st.MonthStartDay, st.WeekStart)
	days, _ := s.store.DailySums(ctx, ids, from, to)
	var inc, exp int64
	daily := map[string]int64{}
	for _, d := range days {
		if d.Kind == "income" {
			inc += d.Sum
		} else {
			exp += d.Sum
			daily[d.Date] += d.Sum
		}
	}
	spark := []store.DayAmount{}
	for d := from; d <= minStr(store.Today(), to); d = addDay(d) {
		spark = append(spark, store.DayAmount{Date: d, Amount: daily[d]})
	}
	cats, _ := s.store.CategorySums(ctx, ids, "expense", from, to)
	if len(cats) > 6 {
		cats = cats[:6]
	}
	recent, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: ids, Limit: 12})
	tpls, _ := s.store.ListTemplates(ctx, uid)
	if len(tpls) > 12 {
		tpls = tpls[:12]
	}
	budgets, _ := s.store.ListBudgets(ctx, uid)
	bl := []store.Budget{}
	for i := range budgets {
		if budgets[i].Archived {
			continue
		}
		budgets[i].Status = s.budgetStatus(ctx, &budgets[i], store.Today(), st)
		bl = append(bl, budgets[i])
	}
	goals, _ := s.store.ListGoals(ctx, uid)
	gl := []store.Goal{}
	for i := range goals {
		if !goals[i].Archived && goals[i].CompletedAt == "" {
			s.goalStats(ctx, &goals[i])
			gl = append(gl, goals[i])
		}
	}
	fc := s.forecast(ctx)
	var trip *pocket.Trip
	if st.TravelURL != "" {
		trip = pocket.TripOn(s.tripList(ctx), store.Today())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"currency": home, "net_worth": net, "liquid": liquid, "accounts": accts,
		"month":    map[string]any{"from": from, "to": to, "label": periodLabel(from, to, "monthly"), "income": inc, "expense": exp, "net": inc - exp, "daily": spark, "categories": cats},
		"forecast": fc, "recent": recent, "templates": tpls, "budgets": bl, "goals": gl, "insights": s.insights(ctx), "trip": trip,
		"needs_onboarding": len(accts) == 0,
	})
}

// handleDashboard is the flat shape a Homepage customapi widget reads.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx)
	accts, home, err := s.accountsWithHome(ctx, false)
	if err != nil {
		s.fail(w, err, "accounts")
		return
	}
	var net int64
	for _, a := range accts {
		if a.IncludeNetWorth {
			net += a.BalanceHome
		}
	}
	fc := s.forecast(ctx)
	dec := currency.Decimals(home)
	f := func(v int64) float64 { return math.Round(float64(v)/math.Pow10(dec)*100) / 100 }
	out := map[string]any{"currency": home, "accounts": len(accts),
		"net_worth": f(net), "net_worth_text": majorString(net, home),
		"month_spent": f(fc.SpentSoFar), "month_spent_text": majorString(fc.SpentSoFar, home),
		"month_income": f(fc.IncomeSoFar), "month_income_text": majorString(fc.IncomeSoFar, home),
		"safe_per_day": f(fc.SafePerDay), "safe_per_day_text": majorString(fc.SafePerDay, home),
		"bills_left": f(fc.BillsLeft), "bills_left_text": majorString(fc.BillsLeft, home),
		"projected_end": f(fc.ProjectedEnd), "days_left": fc.DaysLeft,
	}
	next := "nothing due"
	for _, u := range fc.Upcoming {
		if u["kind"] != "income" {
			next = fmt.Sprintf("%s %s · %s", u["name"], majorString(u["amount"].(int64), home), u["date"])
			break
		}
	}
	out["next_bill_text"] = next
	budgets, _ := s.store.ListBudgets(ctx, actorID(ctx))
	over, warn := 0, 0
	for i := range budgets {
		if budgets[i].Archived {
			continue
		}
		bs := s.budgetStatus(ctx, &budgets[i], store.Today(), st)
		switch bs.State {
		case "over":
			over++
		case "warn":
			warn++
		}
	}
	out["budgets"] = len(budgets)
	out["budgets_over"] = over
	out["budgets_warn"] = warn
	writeJSON(w, http.StatusOK, out)
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}
