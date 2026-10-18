// Package server wires the HTTP API, auth, background jobs and the embedded SPA.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-money/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/firefly"
	"github.com/abjelosevic88/pocket-homelab-money/internal/fx"
	"github.com/abjelosevic88/pocket-homelab-money/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-money/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	log      *slog.Logger
	webFS    fs.FS
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	fx       *fx.Client

	jobMu     sync.Mutex // one background pass (or import) at a time
	importing atomic.Bool
	notifyDay string
	digestDay string

	tripMu  sync.Mutex
	trips   []pocket.Trip
	tripsAt time.Time
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	return &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(), fx: fx.New(cfg.FXProvider, cfg.UserAgent, 8*time.Second)}
}

// Bootstrap seeds the first admin (if configured), integration defaults,
// demo data and the first Firefly import.
func (s *Server) Bootstrap(ctx context.Context) error {
	if err := os.MkdirAll(s.cfg.FilesDir, 0o755); err != nil {
		return fmt.Errorf("files dir: %w", err)
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st, _ := s.store.GetSettings(ctx)
		st.SetupComplete = true
		if err := s.store.SaveSettings(ctx, st); err != nil {
			return err
		}
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	changed := false
	set := func(dst *string, v string) {
		if *dst == "" && v != "" {
			*dst = v
			changed = true
		}
	}
	set(&st.FireflyURL, s.cfg.FireflyURL)
	set(&st.FireflyToken, s.cfg.FireflyToken)
	set(&st.PicoURL, s.cfg.PicoURL)
	set(&st.NotifyURL, s.cfg.NotifyURL)
	set(&st.AIKey, s.cfg.OpenAIKey)
	set(&st.AIModel, s.cfg.OpenAIModel)
	set(&st.AIURL, s.cfg.OpenAIBaseURL)
	set(&st.TravelURL, s.cfg.TravelURL)
	set(&st.TravelToken, s.cfg.TravelToken)
	set(&st.TravelPublicURL, s.cfg.TravelPublicURL)
	if s.cfg.NotifyFormat != "" && st.NotifyURL == s.cfg.NotifyURL && st.NotifyFormat != s.cfg.NotifyFormat {
		st.NotifyFormat = s.cfg.NotifyFormat
		changed = true
	}
	if s.cfg.NotifyHour != 9 && st.NotifyHour == 9 {
		st.NotifyHour = s.cfg.NotifyHour
		changed = true
	}
	if st.HomeCurrency == "EUR" && s.cfg.DefaultCurrency != "EUR" {
		st.HomeCurrency = s.cfg.DefaultCurrency
		changed = true
	}
	if changed {
		if err := s.store.SaveSettings(ctx, st); err != nil {
			return err
		}
	}
	if s.cfg.DemoData {
		if err := s.seedDemo(ctx); err != nil {
			return err
		}
	}
	// First start next to a Firefly III: bring everything over once, in the
	// background so the UI is up immediately.
	if st.FireflyURL != "" && st.FireflyToken != "" && s.cfg.FireflyImport {
		if counts := s.store.Counts(ctx); counts["accounts"] == 0 && counts["transactions"] == 0 {
			if owner, err := s.importOwner(ctx); err == nil {
				if s.cfg.FireflyMirror && !st.FireflyMirror {
					st.FireflyMirror = true
					_ = s.store.SaveSettings(ctx, st)
				}
				go func() {
					res := s.importFirefly(context.Background(), owner.ID, importOptions{All: true})
					s.log.Info("firefly import on first start", "summary", res.Summary(), "error", res.Error)
				}()
			} else {
				s.log.Warn("firefly import skipped: no user to own the accounts yet", "err", err)
			}
		}
	}
	_ = s.store.PurgeSessions(ctx)
	go s.janitor(ctx)
	return nil
}

// importOwner picks the account that owns imported data: the first admin.
func (s *Server) importOwner(ctx context.Context) (*store.User, error) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Role == "admin" {
			return &users[i], nil
		}
	}
	if len(users) > 0 {
		return &users[0], nil
	}
	if s.cfg.AuthMode != "local" {
		return s.firstOrSystemUser(ctx, "admin@localhost", "Admin")
	}
	return nil, errors.New("no users")
}

// janitor runs the background work every 10 minutes: book due subscriptions,
// mirror Firefly when asked to, and once a day send reminders.
func (s *Server) janitor(ctx context.Context) {
	first := time.NewTimer(15 * time.Second)
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		}
		tick++
		s.runJobs(ctx, tick%3 == 1)
	}
}

func (s *Server) runJobs(ctx context.Context, mirror bool) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	st, _ := s.store.GetSettings(ctx)
	if mirror && st.FireflyMirror && st.FireflyURL != "" && !s.importing.Load() {
		if owner, err := s.importOwner(ctx); err == nil {
			res := s.importFirefly(ctx, owner.ID, importOptions{Transactions: true, Since: time.Now().AddDate(0, 0, -45).Format("2006-01-02"), Quiet: true})
			if res.Error != "" {
				s.log.Warn("firefly mirror", "err", res.Error)
			} else if res.Created+res.Updated > 0 {
				s.log.Info("firefly mirror", "summary", res.Summary())
			}
		}
	}
	if !st.FireflyMirror {
		// while Firefly is still the source, its own recurrences book these
		s.bookDueSubscriptions(ctx)
	}
	s.maybeNotify(ctx, st)
}

// ---- integration clients, built from the current settings ----

type clients struct {
	st      store.Settings
	firefly *firefly.Client
	notify  *notify.Client
	travel  *pocket.Client
	ai      *ai.Client
}

func (s *Server) clients(ctx context.Context) clients {
	st, _ := s.store.GetSettings(ctx)
	return clients{st: st,
		firefly: firefly.New(st.FireflyURL, st.FireflyToken, 60*time.Second),
		notify:  notify.New(st.NotifyURL, st.NotifyFormat),
		travel:  pocket.New(st.TravelURL, st.TravelToken, 15*time.Second),
		ai:      ai.New(st.AIURL, st.AIKey, st.AIModel, st.AITranscribe),
	}
}

// rate returns how many units of `to` one unit of `from` is worth on a date.
// Falls back to 1 (and says so) when nothing is available so an amount is never lost.
func (s *Server) rate(ctx context.Context, from, to, date string) (float64, string) {
	from, to = strings.ToUpper(from), strings.ToUpper(to)
	if from == to || from == "" || to == "" {
		return 1, "same"
	}
	if date == "" || date > store.Today() {
		date = store.Today()
	}
	if r, src, ok := s.store.GetFXRate(ctx, from, to, date); ok {
		return r, src
	}
	st, _ := s.store.GetSettings(ctx)
	if !st.FXAuto {
		if r, ok := fx.Pegged(from, to); ok {
			return r, "fixed peg"
		}
		return 1, "disabled"
	}
	r, src, err := s.fx.Rate(ctx, from, to, date)
	if err != nil {
		// an older cached rate beats none at all
		if r, src, ok := s.store.LatestFXRate(ctx, from, to, date); ok {
			return r, src + " (older)"
		}
		s.log.Warn("fx lookup failed", "from", from, "to", to, "date", date, "err", err)
		return 1, "unavailable"
	}
	_ = s.store.PutFXRate(ctx, from, to, date, r, src)
	return r, src
}

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(180 * time.Second))

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/setup", s.handleSetup)
		r.Get("/auth/status", s.handleAuthStatus)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Put("/auth/me", s.handleUpdateMe)

			r.Get("/dashboard", s.handleDashboard)
			r.Get("/home", s.handleHome)
			r.Get("/currencies", s.handleCurrencies)
			r.Get("/fx", s.handleFX)
			r.Get("/meta", s.handleMeta)
			r.Get("/activity", s.handleActivity)

			r.Get("/accounts", s.handleListAccounts)
			r.Post("/accounts", s.handleCreateAccount)
			r.Put("/accounts/reorder", s.handleReorderAccounts)
			r.Get("/accounts/{id}", s.handleGetAccount)
			r.Put("/accounts/{id}", s.handleUpdateAccount)
			r.Delete("/accounts/{id}", s.handleDeleteAccount)
			r.Get("/accounts/{id}/history", s.handleAccountHistory)
			r.Put("/accounts/{id}/members", s.handleAccountMembers)
			r.Post("/accounts/{id}/reconcile", s.handleReconcile)

			r.Get("/transactions", s.handleListTransactions)
			r.Post("/transactions", s.handleCreateTransaction)
			r.Post("/transactions/bulk", s.handleBulkTransactions)
			r.Get("/transactions/{id}", s.handleGetTransaction)
			r.Put("/transactions/{id}", s.handleUpdateTransaction)
			r.Delete("/transactions/{id}", s.handleDeleteTransaction)
			r.Post("/transactions/{id}/duplicate", s.handleDuplicateTransaction)
			r.Post("/transactions/{id}/attachments", s.handleUploadAttachment)
			r.Get("/attachments/{id}", s.handleGetAttachment)
			r.Delete("/attachments/{id}", s.handleDeleteAttachment)
			r.Get("/suggest", s.handleSuggest)
			r.Get("/payees", s.handlePayees)

			r.Post("/quick/parse", s.handleQuickParse)
			r.Post("/quick", s.handleQuickAdd)

			r.Get("/categories", s.handleListCategories)
			r.Post("/categories", s.handleCreateCategory)
			r.Put("/categories/{id}", s.handleUpdateCategory)
			r.Delete("/categories/{id}", s.handleDeleteCategory)
			r.Get("/tags", s.handleListTags)
			r.Post("/tags", s.handleCreateTag)
			r.Put("/tags/{id}", s.handleUpdateTag)
			r.Delete("/tags/{id}", s.handleDeleteTag)
			r.Post("/tags/{id}/merge", s.handleMergeTag)

			r.Get("/templates", s.handleListTemplates)
			r.Post("/templates", s.handleCreateTemplate)
			r.Put("/templates/{id}", s.handleUpdateTemplate)
			r.Delete("/templates/{id}", s.handleDeleteTemplate)
			r.Post("/templates/{id}/use", s.handleUseTemplate)

			r.Get("/subscriptions", s.handleListSubscriptions)
			r.Post("/subscriptions", s.handleCreateSubscription)
			r.Get("/subscriptions/detect", s.handleDetectSubscriptions)
			r.Get("/subscriptions/{id}", s.handleGetSubscription)
			r.Put("/subscriptions/{id}", s.handleUpdateSubscription)
			r.Delete("/subscriptions/{id}", s.handleDeleteSubscription)
			r.Post("/subscriptions/{id}/pay", s.handlePaySubscription)
			r.Post("/subscriptions/{id}/skip", s.handleSkipSubscription)
			r.Post("/subscriptions/{id}/link", s.handleLinkSubscription)

			r.Get("/budgets", s.handleListBudgets)
			r.Post("/budgets", s.handleCreateBudget)
			r.Get("/budgets/{id}", s.handleGetBudget)
			r.Put("/budgets/{id}", s.handleUpdateBudget)
			r.Delete("/budgets/{id}", s.handleDeleteBudget)
			r.Get("/budgets/suggest", s.handleSuggestBudgets)

			r.Get("/goals", s.handleListGoals)
			r.Post("/goals", s.handleCreateGoal)
			r.Get("/goals/{id}", s.handleGetGoal)
			r.Put("/goals/{id}", s.handleUpdateGoal)
			r.Delete("/goals/{id}", s.handleDeleteGoal)
			r.Post("/goals/{id}/entries", s.handleAddGoalEntry)
			r.Delete("/goals/{id}/entries/{entryID}", s.handleDeleteGoalEntry)

			r.Get("/rules", s.handleListRules)
			r.Post("/rules", s.handleCreateRule)
			r.Put("/rules/reorder", s.handleReorderRules)
			r.Put("/rules/{id}", s.handleUpdateRule)
			r.Delete("/rules/{id}", s.handleDeleteRule)
			r.Post("/rules/test", s.handleTestRule)
			r.Post("/rules/{id}/apply", s.handleApplyRule)

			r.Get("/reports/overview", s.handleReportOverview)
			r.Get("/reports/flow", s.handleReportFlow)
			r.Get("/reports/categories", s.handleReportCategories)
			r.Get("/reports/tags", s.handleReportTags)
			r.Get("/reports/networth", s.handleReportNetWorth)
			r.Get("/reports/calendar", s.handleReportCalendar)
			r.Get("/reports/forecast", s.handleForecast)
			r.Get("/reports/insights", s.handleInsights)

			r.Post("/ai/parse", s.handleAIParse)
			r.Post("/ai/transcribe", s.handleAITranscribe)
			r.Post("/ai/ask", s.handleAIAsk)
			r.Get("/ai/usage", s.handleAIUsage)

			r.Post("/import/firefly", s.handleImportFirefly)
			r.Get("/import/firefly/status", s.handleImportStatus)
			r.Post("/import/csv/preview", s.handleCSVPreview)
			r.Post("/import/csv", s.handleCSVImport)
			r.Get("/export/transactions.csv", s.handleExportCSV)
			r.Get("/export/money.json", s.handleExportJSON)

			r.Post("/integrations/test", s.handleIntegrationTest)
			r.Post("/notify/test", s.handleNotifyTest)
			r.Post("/notify/run", s.handleNotifyRun)
			r.Get("/travel/trips", s.handleTravelTrips)

			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Put("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Get("/settings/system", s.handleSystemInfo)

			r.Get("/tokens", s.handleListTokens)
			r.Post("/tokens", s.handleCreateToken)
			r.Delete("/tokens/{id}", s.handleDeleteToken)

			r.Get("/backup", s.handleBackup)
		})
	})

	// Firefly III-compatible API (and Firefly Pico's template endpoint) so
	// Raycast, the phone app, scripts and the other Pocket apps keep working
	// with only their Firefly URL changed to <base>/firefly.
	s.compatRoutes(r)

	// Calendar subscription of upcoming payments: token in the query.
	r.Get("/cal/subscriptions.ics", s.handleCalendarFeed)

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

// decodeKeys decodes JSON into v and also reports which top-level keys were
// present, so PUT handlers can tell "not sent" from "sent empty".
func decodeKeys(r *http.Request, v any) (map[string]bool, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(raw))
	for k := range raw {
		keys[k] = true
	}
	return keys, json.Unmarshal(b, v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_money_info Build info.\n# TYPE pocket_money_info gauge\npocket_money_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_money_uptime_seconds Process uptime.\n# TYPE pocket_money_uptime_seconds gauge\npocket_money_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_money_http_requests_total HTTP requests served.\n# TYPE pocket_money_http_requests_total counter\npocket_money_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_money_http_errors_total HTTP 5xx responses.\n# TYPE pocket_money_http_errors_total counter\npocket_money_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_money_rows Rows per table.\n# TYPE pocket_money_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_money_rows{table=%q} %d\n", k, v)
	}
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}

// ---------- files ----------

// saveFile writes bytes under FILES_DIR/<yyyy-mm>/ with a random name and returns the relative path.
func (s *Server) saveFile(bucket string, ext string, data []byte) (string, error) {
	dir := filepath.Join(s.cfg.FilesDir, bucket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext = strings.ToLower(filepath.Ext("x" + ext))
	if len(ext) > 8 {
		ext = ""
	}
	name := store.RandomToken(12) + ext
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return bucket + "/" + name, nil
}

func (s *Server) filePath(rel string) string {
	rel = filepath.Clean("/" + rel)
	return filepath.Join(s.cfg.FilesDir, rel)
}

func (s *Server) removeFile(rel string) {
	if rel == "" {
		return
	}
	_ = os.Remove(s.filePath(rel))
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, rel, mime, download string) {
	if rel == "" {
		writeErr(w, http.StatusNotFound, "no file")
		return
	}
	p := s.filePath(rel)
	if _, err := os.Stat(p); err != nil {
		writeErr(w, http.StatusNotFound, "file missing")
		return
	}
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	if download != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(download, `"`, "")+`"`)
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, p)
}

// readUpload reads one multipart file field (or the raw body) up to the limit.
func (s *Server) readUpload(r *http.Request, field string) ([]byte, string, string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, s.cfg.MaxUploadBytes)
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(s.cfg.MaxUploadBytes); err != nil {
			return nil, "", "", err
		}
		f, hdr, err := r.FormFile(field)
		if err != nil {
			return nil, "", "", err
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			return nil, "", "", err
		}
		return b, hdr.Filename, hdr.Header.Get("Content-Type"), nil
	}
	b, err := io.ReadAll(r.Body)
	return b, r.URL.Query().Get("name"), ct, err
}
