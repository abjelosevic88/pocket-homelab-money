package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// AuditEntry is one line of an account's audit log.
type AuditEntry struct {
	ID            int64           `json:"id"`
	AccountID     int64           `json:"account_id"`
	AccountName   string          `json:"account_name,omitempty"`
	ActorUserID   *int64          `json:"actor_user_id"`
	ActorName     string          `json:"actor_name"` // the person's current name, else the label
	ActorLabel    string          `json:"actor_label"`
	Source        string          `json:"source"`
	Action        string          `json:"action"`
	TransactionID *int64          `json:"transaction_id"`
	Summary       string          `json:"summary"`
	Changes       json.RawMessage `json:"changes"`
	CreatedAt     string          `json:"created_at"`
}

// AuditFilter narrows an audit query; Before is an id cursor (newest first).
type AuditFilter struct {
	AccountIDs  []int64
	Before      int64
	Limit       int
	ActorUserID int64  // a person
	ActorLabel  string // a job or service with no person ("Firefly mirror")
	Action      string // exact ("transaction.deleted") or a prefix ("transaction")
}

// AuditActor is someone (or something) that appears in an audit log.
type AuditActor struct {
	Key    string `json:"key"` // user id as text, or the label for jobs
	UserID *int64 `json:"user_id"`
	Name   string `json:"name"`
}

// AddAudit appends an entry.
func (s *Store) AddAudit(ctx context.Context, e *AuditEntry) error {
	ch := e.Changes
	if len(ch) == 0 {
		ch = json.RawMessage(`{}`)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO account_audit (account_id, actor_user_id, actor_label, source, action, transaction_id, summary, changes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.AccountID, nullI(e.ActorUserID), e.ActorLabel, e.Source, e.Action, nullI(e.TransactionID), e.Summary, string(ch), Now())
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// ListAudit returns entries of the given accounts, newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	out := []AuditEntry{}
	if len(f.AccountIDs) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(f.AccountIDs)), ",")
	q := `SELECT x.id, x.account_id, a.name, x.actor_user_id, COALESCE(NULLIF(u.name, ''), u.email, ''), x.actor_label, x.source, x.action, x.transaction_id, x.summary, x.changes, x.created_at
		FROM account_audit x JOIN accounts a ON a.id = x.account_id LEFT JOIN users u ON u.id = x.actor_user_id
		WHERE x.account_id IN (` + ph + `)`
	args := make([]any, 0, len(f.AccountIDs)+6)
	for _, id := range f.AccountIDs {
		args = append(args, id)
	}
	if f.Before > 0 {
		q += ` AND x.id < ?`
		args = append(args, f.Before)
	}
	if f.ActorUserID > 0 {
		q += ` AND x.actor_user_id = ?`
		args = append(args, f.ActorUserID)
	} else if f.ActorLabel != "" {
		q += ` AND x.actor_user_id IS NULL AND x.actor_label = ?`
		args = append(args, f.ActorLabel)
	}
	if f.Action != "" {
		q += ` AND (x.action = ? OR x.action LIKE ?)`
		args = append(args, f.Action, f.Action+".%")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += ` ORDER BY x.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e AuditEntry
		var ch string
		if err := rows.Scan(&e.ID, &e.AccountID, &e.AccountName, &e.ActorUserID, &e.ActorName, &e.ActorLabel, &e.Source, &e.Action, &e.TransactionID, &e.Summary, &ch, &e.CreatedAt); err != nil {
			return nil, err
		}
		if e.ActorName == "" {
			e.ActorName = e.ActorLabel
		}
		if ch == "" {
			ch = "{}"
		}
		e.Changes = json.RawMessage(ch)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AuditActors lists who appears in the logs of these accounts (for filters).
func (s *Store) AuditActors(ctx context.Context, accountIDs []int64) ([]AuditActor, error) {
	out := []AuditActor{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(accountIDs)), ",")
	args := make([]any, 0, len(accountIDs))
	for _, id := range accountIDs {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT x.actor_user_id, CASE WHEN x.actor_user_id IS NULL THEN x.actor_label ELSE COALESCE(NULLIF(u.name, ''), u.email, x.actor_label) END AS nm
		FROM account_audit x LEFT JOIN users u ON u.id = x.actor_user_id
		WHERE x.account_id IN (`+ph+`) GROUP BY x.actor_user_id, CASE WHEN x.actor_user_id IS NULL THEN x.actor_label ELSE '' END ORDER BY nm`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a AuditActor
		if err := rows.Scan(&a.UserID, &a.Name); err != nil {
			return nil, err
		}
		if a.UserID != nil {
			a.Key = strconv.FormatInt(*a.UserID, 10)
		} else {
			a.Key = a.Name
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OwnedAccountIDs returns the ids of the accounts a person owns (archived included).
func (s *Store) OwnedAccountIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM accounts WHERE owner_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
