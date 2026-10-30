package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// ---- scheduled jobs, for Pocket Monitoring's Jobs tab ----
//
// The jobs contract (docs/jobs.md in pocket-homelab-monitoring): what this
// app runs on its own clock, when it ran, how it went and when it runs next,
// as the person asking sees it. The background round is the box's (admins
// only); reminders, the product check and the day review are per person.

type jobRun struct {
	Job     string `json:"job"`
	ID      string `json:"id"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
}

type jobOut struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Schedule    string  `json:"schedule"`
	Enabled     bool    `json:"enabled"`
	Next        int64   `json:"next"`
	Running     bool    `json:"running"`
	Started     int64   `json:"started"`
	Last        *jobRun `json:"last"`
	URL         string  `json:"url"`
	CanRun      bool    `json:"can_run"`
}

// runRound is one background round, remembered for the Jobs tab.
func (s *Server) runRound(ctx context.Context, mirror bool) {
	start := time.Now()
	s.runJobs(ctx, mirror)
	end := time.Now()
	s.jobsMu.Lock()
	s.rounds = append(s.rounds, jobRun{Job: "round", ID: fmt.Sprintf("round-%d", start.UnixMilli()), Start: start.UnixMilli(), End: end.UnixMilli(), OK: true})
	if len(s.rounds) > 50 {
		s.rounds = s.rounds[len(s.rounds)-50:]
	}
	s.jobsMu.Unlock()
}

// dailyNext is the next run of a job that runs once a day after `hour`,
// keyed in the cache by the day it last ran.
func (s *Server) dailyNext(ctx context.Context, key string, hour int) int64 {
	now := time.Now()
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if last, _, _ := s.store.CacheGet(ctx, key); last == now.Format("2006-01-02") {
		at = at.AddDate(0, 0, 1)
	}
	return at.UnixMilli()
}

func ms(rfc string) int64 {
	if t, err := time.Parse(time.RFC3339Nano, rfc); err == nil {
		return t.UnixMilli()
	}
	return 0
}

func atHour(h int) string { return fmt.Sprintf("every day at %02d:00", h) }

// jobsFor lists the person's jobs and their runs that ended after since.
func (s *Server) jobsFor(ctx context.Context, since int64) ([]jobOut, []jobRun) {
	uid := actorID(ctx)
	admin := isAdmin(ctx)
	var jobs []jobOut
	var runs []jobRun
	s.jobsMu.Lock()
	running := map[string]int64{}
	for k, t := range s.jobsRunning {
		running[k] = t.UnixMilli()
	}
	rounds := append([]jobRun(nil), s.rounds...)
	s.jobsMu.Unlock()
	add := func(j jobOut, all []jobRun) {
		if t, ok := running[fmt.Sprintf("%d:%s", uid, j.ID)]; ok {
			j.Running, j.Started = true, t
		}
		for i := range all {
			r := all[i]
			if j.Last == nil || r.End > j.Last.End {
				j.Last = &r
			}
			if r.End > since {
				runs = append(runs, r)
			}
		}
		jobs = append(jobs, j)
	}

	if admin {
		j := jobOut{ID: "round", Name: "Background round", Schedule: "every 10 minutes", Enabled: true, CanRun: true,
			Description: "Books due subscriptions and loan interest, mirrors Firefly when that is on, retries receipts waiting on the tax office, sends reminders and starts the daily jobs below."}
		if n := len(rounds); n > 0 {
			j.Next = time.UnixMilli(rounds[n-1].Start).Add(10 * time.Minute).UnixMilli()
		}
		add(j, rounds)
	}

	st, _ := s.store.GetSettings(ctx, uid)
	hooks, _ := s.store.ListWebhooks(ctx, uid)
	rem := jobOut{ID: "reminders", Name: "Reminders", Schedule: atHour(st.NotifyHour), Enabled: st.NotifyURL != "" || len(hooks) > 0, CanRun: true, URL: "/settings/notifications",
		Description: "Subscription and bill reminders, budget warnings and, on Mondays, the weekly digest."}
	remKey := fmt.Sprintf("notify:last_day:%d", uid)
	rem.Next = s.dailyNext(ctx, remKey, st.NotifyHour)
	var remRuns []jobRun
	if day, at, ok := s.store.CacheGet(ctx, remKey); ok && day != "" {
		if t := ms(at); t > 0 {
			remRuns = append(remRuns, jobRun{Job: "reminders", ID: "rem-" + day, Start: t, End: t, OK: true})
		}
	}
	add(rem, remRuns)

	rs := s.store.GetReceiptSettings(ctx, uid)
	pc := jobOut{ID: "product-check", Name: "Product check", Schedule: atHour(rs.GroupJobHour) + " (" + rs.GroupJob + ")", Enabled: rs.GroupJob != "off", CanRun: true, URL: "/products?report=1",
		Description: "Puts receipt products without a group into groups (keywords, similar products, the AI when chosen) and suggests new groups. Runs only when there are ungrouped products."}
	pc.Next = s.dailyNext(ctx, fmt.Sprintf("productcheck:day:%d", uid), rs.GroupJobHour)
	var pcRuns []jobRun
	if list, err := s.store.ProductCheckRuns(ctx, uid, 30); err == nil {
		for _, r := range list {
			if r.Trigger == "scan" || r.FinishedAt == "" {
				continue
			}
			pcRuns = append(pcRuns, jobRun{Job: "product-check", ID: fmt.Sprintf("pc-%d", r.ID), Start: ms(r.StartedAt), End: ms(r.FinishedAt), OK: r.Error == "",
				Summary: firstNonBlank(r.Error, fmt.Sprintf("%d checked · %d grouped · %d left", r.Checked, r.ByKeywords+r.BySimilar+r.ByAI, r.StillUngrouped))})
		}
	}
	add(pc, pcRuns)

	ds := s.store.GetDayReviewSettings(ctx, uid)
	client, detail := s.receiptAI(ctx)
	dr := jobOut{ID: "day-review", Name: "Day review", Schedule: atHour(ds.Hour) + " (" + ds.Mode + ")", Enabled: ds.Mode != "off" && client != nil, CanRun: client != nil, URL: "/review",
		Description: "The AI looks over the past day's bookings and flags (or, in fix mode, corrects) what looks off. Runs only when something was booked."}
	if ds.Mode != "off" && client == nil {
		dr.Description += " Waiting for an AI: " + detail + "."
	}
	dr.Next = s.dailyNext(ctx, fmt.Sprintf("dayreview:day:%d", uid), ds.Hour)
	var drRuns []jobRun
	if list, err := s.store.DayReviews(ctx, uid, 30); err == nil {
		for _, r := range list {
			open := 0
			for _, f := range r.Findings {
				if f.Status == "open" {
					open++
				}
			}
			summary := fmt.Sprintf("%s: %d checked · %d findings · %d to look at", r.Day, r.Checked, len(r.Findings), open)
			drRuns = append(drRuns, jobRun{Job: "day-review", ID: fmt.Sprintf("dr-%d", r.ID), Start: ms(r.StartedAt), End: ms(r.FinishedAt), OK: r.Error == "", Summary: firstNonBlank(r.Error, summary)})
		}
	}
	add(dr, drRuns)

	sort.Slice(runs, func(a, b int) bool { return runs[a].End < runs[b].End })
	if len(runs) > 200 {
		runs = runs[len(runs)-200:]
	}
	return jobs, runs
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	jobs, runs := s.jobsFor(r.Context(), since)
	if runs == nil {
		runs = []jobRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "runs": runs})
}

// handleRunJob starts a job now, in the background.
func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	jobs, _ := s.jobsFor(ctx, 0)
	var job *jobOut
	for i := range jobs {
		if jobs[i].ID == id {
			job = &jobs[i]
		}
	}
	switch {
	case job == nil:
		writeErr(w, http.StatusNotFound, "no such job")
		return
	case !job.CanRun:
		writeErr(w, http.StatusForbidden, "this job can't be started now")
		return
	case job.Running:
		writeErr(w, http.StatusConflict, job.Name+" is running already")
		return
	}
	key := fmt.Sprintf("%d:%s", actorID(ctx), id)
	s.jobsMu.Lock()
	if s.jobsRunning == nil {
		s.jobsRunning = map[string]time.Time{}
	}
	s.jobsRunning[key] = time.Now()
	s.jobsMu.Unlock()
	bg := context.WithoutCancel(ctx)
	s.receiptWork.Add(1)
	go func() {
		defer s.receiptWork.Done()
		defer func() { s.jobsMu.Lock(); delete(s.jobsRunning, key); s.jobsMu.Unlock() }()
		var err error
		switch id {
		case "round":
			s.runRound(bg, false)
		case "reminders":
			_, err = s.runNotifications(bg, false)
			if err == nil {
				_ = s.store.CachePut(bg, fmt.Sprintf("notify:last_day:%d", actorID(bg)), time.Now().Format("2006-01-02"))
			}
		case "product-check":
			_, err = s.runProductCheck(bg, "manual", nil)
		case "day-review":
			_, err = s.runDayReview(bg, time.Now().AddDate(0, 0, -1).Format("2006-01-02"), "manual")
		}
		if err != nil {
			s.log.Warn("job run by hand", "job", id, "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}
