// Package store wraps the SQLite database, migrations and all queries.
//
// The pool holds ONE connection: never run a second query while a rows
// cursor is still open — collect first, then query.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique constraint is violated.
var ErrConflict = errors.New("already exists")

// Store is the database handle.
type Store struct {
	DB  *sql.DB
	log *slog.Logger
}

// Open opens (and creates if needed) the SQLite database and runs migrations.
func Open(path string, log *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{DB: db, log: log}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists int
		if err := s.DB.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, name, Now()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.log.Info("applied migration", "name", name)
	}
	return nil
}

// Backup writes a consistent copy of the database to dst using VACUUM INTO.
func (s *Store) Backup(ctx context.Context, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	_, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, dst)
	return err
}

// Tables lists the tables counted for metrics / the About page.
var Tables = []string{"accounts", "transactions", "categories", "tags", "subscriptions", "budgets", "goals", "templates", "rules", "attachments", "users", "api_tokens"}

// Counts returns row counts per table.
func (s *Store) Counts(ctx context.Context) map[string]int {
	out := map[string]int{}
	for _, t := range Tables {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+t).Scan(&n); err == nil {
			out[t] = n
		}
	}
	return out
}

// Now returns the current UTC time formatted for storage.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Today returns today's date (local) as YYYY-MM-DD.
func Today() string { return time.Now().Format("2006-01-02") }

// ParseDate parses YYYY-MM-DD.
func ParseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

// DaysBetween returns b-a in days (0 when either is empty or b<a).
func DaysBetween(a, b string) int {
	ta, err1 := ParseDate(a)
	tb, err2 := ParseDate(b)
	if err1 != nil || err2 != nil || tb.Before(ta) {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func conflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullF(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func nullI(i *int64) any {
	if i == nil {
		return nil
	}
	return *i
}

// LogActivity appends a line to the change log (best effort).
func (s *Store) LogActivity(ctx context.Context, userID int64, action, subject string) {
	var uid any
	if userID > 0 {
		uid = userID
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO activity (user_id, action, subject) VALUES (?, ?, ?)`, uid, action, subject)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM activity WHERE id NOT IN (SELECT id FROM activity ORDER BY id DESC LIMIT 500)`)
}

// ListActivity returns the latest change-log lines.
func (s *Store) ListActivity(ctx context.Context, limit int) ([]Activity, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.user_id, COALESCE(u.name, u.email, ''), a.action, a.subject, a.created_at
		FROM activity a LEFT JOIN users u ON u.id = a.user_id ORDER BY a.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.UserID, &a.UserName, &a.Action, &a.Subject, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LogAI records the tokens one AI call used.
func (s *Store) LogAI(ctx context.Context, feature, model string, prompt, completion int) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO ai_usage (feature, model, prompt_tokens, completion_tokens) VALUES (?, ?, ?, ?)`, feature, model, prompt, completion)
}

// AIUsage sums tokens per feature for the last 30 days.
func (s *Store) AIUsage(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT feature, COUNT(1), SUM(prompt_tokens), SUM(completion_tokens) FROM ai_usage
		WHERE created_at >= strftime('%Y-%m-%dT%H:%M:%fZ','now','-30 days') GROUP BY feature ORDER BY feature`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var f string
		var n, p, c int
		if err := rows.Scan(&f, &n, &p, &c); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"feature": f, "calls": n, "prompt_tokens": p, "completion_tokens": c})
	}
	return out, rows.Err()
}
