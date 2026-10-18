package store

import (
	"context"
	"strconv"
)

// DefaultTitle is the app title until someone changes it.
const DefaultTitle = "Pocket Money"

// GetSettings loads all settings with defaults applied.
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	st := Settings{Title: DefaultTitle, HomeCurrency: "EUR", DateFormat: "auto", TimeFormat: "auto", WeekStart: 1, MonthStartDay: 1, FXAuto: true, QuickLang: "auto", NotifyFormat: "apprise", NotifyHour: 9, NotifyDigest: true, TravelAutoTag: true}
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	str := map[string]*string{
		"firefly_url": &st.FireflyURL, "firefly_token": &st.FireflyToken, "pico_url": &st.PicoURL,
		"notify_url": &st.NotifyURL, "ai_url": &st.AIURL, "ai_key": &st.AIKey, "ai_model": &st.AIModel,
		"ai_transcribe_model": &st.AITranscribe, "ai_language": &st.AILanguage, "ai_context": &st.AIContext,
		"travel_url": &st.TravelURL, "travel_token": &st.TravelToken, "travel_public_url": &st.TravelPublicURL,
	}
	nonEmpty := map[string]*string{
		"title": &st.Title, "home_currency": &st.HomeCurrency, "date_format": &st.DateFormat, "time_format": &st.TimeFormat,
		"quick_lang": &st.QuickLang, "notify_format": &st.NotifyFormat,
	}
	flags := map[string]*bool{
		"fx_auto": &st.FXAuto, "firefly_mirror": &st.FireflyMirror, "notify_digest": &st.NotifyDigest,
		"travel_auto_tag": &st.TravelAutoTag, "setup_complete": &st.SetupComplete,
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		if p, ok := str[k]; ok {
			*p = v
			continue
		}
		if p, ok := nonEmpty[k]; ok {
			if v != "" {
				*p = v
			}
			continue
		}
		if p, ok := flags[k]; ok {
			*p = v == "1"
			continue
		}
		switch k {
		case "week_start":
			if n, err := strconv.Atoi(v); err == nil && (n == 0 || n == 1) {
				st.WeekStart = n
			}
		case "month_start_day":
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 28 {
				st.MonthStartDay = n
			}
		case "default_account_id":
			st.DefaultAcct, _ = strconv.ParseInt(v, 10, 64)
		case "notify_hour":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 23 {
				st.NotifyHour = n
			}
		}
	}
	return st, rows.Err()
}

// SaveSettings persists all settings.
func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	b := map[bool]string{true: "1", false: "0"}
	kv := map[string]string{
		"title":               st.Title,
		"home_currency":       st.HomeCurrency,
		"date_format":         st.DateFormat,
		"time_format":         st.TimeFormat,
		"week_start":          strconv.Itoa(st.WeekStart),
		"month_start_day":     strconv.Itoa(st.MonthStartDay),
		"fx_auto":             b[st.FXAuto],
		"default_account_id":  strconv.FormatInt(st.DefaultAcct, 10),
		"quick_lang":          st.QuickLang,
		"firefly_url":         st.FireflyURL,
		"firefly_token":       st.FireflyToken,
		"firefly_mirror":      b[st.FireflyMirror],
		"pico_url":            st.PicoURL,
		"notify_url":          st.NotifyURL,
		"notify_format":       st.NotifyFormat,
		"notify_hour":         strconv.Itoa(st.NotifyHour),
		"notify_digest":       b[st.NotifyDigest],
		"ai_url":              st.AIURL,
		"ai_key":              st.AIKey,
		"ai_model":            st.AIModel,
		"ai_transcribe_model": st.AITranscribe,
		"ai_language":         st.AILanguage,
		"ai_context":          st.AIContext,
		"travel_url":          st.TravelURL,
		"travel_token":        st.TravelToken,
		"travel_public_url":   st.TravelPublicURL,
		"travel_auto_tag":     b[st.TravelAutoTag],
		"setup_complete":      b[st.SetupComplete],
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ---- generic cache ----

// CacheGet returns a cached value and when it was fetched.
func (s *Store) CacheGet(ctx context.Context, key string) (string, string, bool) {
	var v, at string
	if err := s.DB.QueryRowContext(ctx, `SELECT value, fetched_at FROM cache WHERE key = ?`, key).Scan(&v, &at); err != nil {
		return "", "", false
	}
	return v, at, true
}

// CachePut stores a value.
func (s *Store) CachePut(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO cache (key, value, fetched_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, fetched_at = excluded.fetched_at`, key, value, Now())
	return err
}

// CacheDelete removes every key with the prefix.
func (s *Store) CacheDelete(ctx context.Context, prefix string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM cache WHERE key LIKE ? || '%'`, prefix)
	return err
}

// ---- fx cache ----

// GetFXRate returns a cached rate.
func (s *Store) GetFXRate(ctx context.Context, base, quote, date string) (float64, string, bool) {
	var r float64
	var src string
	if err := s.DB.QueryRowContext(ctx, `SELECT rate, source FROM fx_cache WHERE base = ? AND quote = ? AND date = ?`, base, quote, date).Scan(&r, &src); err != nil {
		return 0, "", false
	}
	return r, src, true
}

// PutFXRate caches a rate.
func (s *Store) PutFXRate(ctx context.Context, base, quote, date string, rate float64, source string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO fx_cache (base, quote, date, rate, source) VALUES (?, ?, ?, ?, ?) ON CONFLICT(base, quote, date) DO UPDATE SET rate = excluded.rate, source = excluded.source`, base, quote, date, rate, source)
	return err
}

// LatestFXRate returns the newest cached rate on or before date.
func (s *Store) LatestFXRate(ctx context.Context, base, quote, date string) (float64, string, bool) {
	var r float64
	var src string
	if err := s.DB.QueryRowContext(ctx, `SELECT rate, source FROM fx_cache WHERE base = ? AND quote = ? AND date <= ? ORDER BY date DESC LIMIT 1`, base, quote, date).Scan(&r, &src); err != nil {
		return 0, "", false
	}
	return r, src, true
}
