package store

import (
	"context"
	"database/sql"
)

// DayReviewSettings are one person's day review preferences.
type DayReviewSettings struct {
	Mode   string `json:"mode"` // off | report | fix
	Hour   int    `json:"hour"`
	Notify bool   `json:"notify"`
}

// DayReview is one look over a day's bookings.
type DayReview struct {
	ID         int64              `json:"id"`
	UserID     int64              `json:"user_id"`
	Day        string             `json:"day"`
	Trigger    string             `json:"trigger"`
	Mode       string             `json:"mode"`
	StartedAt  string             `json:"started_at"`
	FinishedAt string             `json:"finished_at"`
	Checked    int                `json:"checked"`
	Note       string             `json:"note"`
	Error      string             `json:"error"`
	Findings   []DayReviewFinding `json:"findings"`
}

// DayReviewFinding is one booking the review thinks is off, with what it
// suggests instead.
type DayReviewFinding struct {
	ID                int64  `json:"id"`
	ReviewID          int64  `json:"review_id"`
	TransactionID     *int64 `json:"transaction_id"`
	Note              string `json:"note"`
	CategoryID        *int64 `json:"category_id"`
	Description       string `json:"description"`
	DuplicateOf       *int64 `json:"duplicate_of"`
	OddAmount         bool   `json:"odd_amount"`
	Status            string `json:"status"`
	BeforeCategoryID  *int64 `json:"before_category_id"`
	BeforeDescription string `json:"before_description"`
	UpdatedAt         string `json:"updated_at"`
}

// GetDayReviewSettings returns the person's preferences (defaults when unset:
// report at midnight, with a notification).
func (s *Store) GetDayReviewSettings(ctx context.Context, userID int64) DayReviewSettings {
	st := DayReviewSettings{Mode: "report", Hour: 0, Notify: true}
	_ = s.DB.QueryRowContext(ctx, `SELECT mode, hour, notify FROM day_review_settings WHERE user_id = ?`, userID).Scan(&st.Mode, &st.Hour, &st.Notify)
	return st
}

// SaveDayReviewSettings stores the person's preferences.
func (s *Store) SaveDayReviewSettings(ctx context.Context, userID int64, st DayReviewSettings) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO day_review_settings (user_id, mode, hour, notify) VALUES (?,?,?,?)
		ON CONFLICT (user_id) DO UPDATE SET mode = excluded.mode, hour = excluded.hour, notify = excluded.notify`,
		userID, st.Mode, st.Hour, boolInt(st.Notify))
	return err
}

// DayTransactionIDs are the person's own expenses and income on a day.
func (s *Store) DayTransactionIDs(ctx context.Context, userID int64, day string) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM transactions WHERE user_id = ? AND date = ? AND kind IN ('expense','income') ORDER BY time, id`, userID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SaveDayReview stores a finished review with its findings. Open findings of
// earlier reviews of the same day are replaced by the new ones.
func (s *Store) SaveDayReview(ctx context.Context, r *DayReview) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE day_review_findings SET status = 'replaced', updated_at = ? WHERE user_id = ? AND status = 'open'
		AND review_id IN (SELECT id FROM day_reviews WHERE user_id = ? AND day = ?)`, Now(), r.UserID, r.UserID, r.Day); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO day_reviews (user_id, day, trigger, mode, started_at, finished_at, checked, note, error) VALUES (?,?,?,?,?,?,?,?,?)`,
		r.UserID, r.Day, r.Trigger, r.Mode, r.StartedAt, r.FinishedAt, r.Checked, r.Note, r.Error)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	for i := range r.Findings {
		f := &r.Findings[i]
		f.ReviewID = r.ID
		if f.Status == "" {
			f.Status = "open"
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO day_review_findings (review_id, user_id, transaction_id, note, category_id, description, duplicate_of, odd_amount, status, before_category_id, before_description, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.UserID, nullI(f.TransactionID), f.Note, nullI(f.CategoryID), f.Description, nullI(f.DuplicateOf), boolInt(f.OddAmount),
			f.Status, nullI(f.BeforeCategoryID), f.BeforeDescription, Now())
		if err != nil {
			return err
		}
		f.ID, _ = res.LastInsertId()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM day_reviews WHERE user_id = ? AND id NOT IN (SELECT id FROM day_reviews WHERE user_id = ? ORDER BY id DESC LIMIT 90)`, r.UserID, r.UserID); err != nil {
		return err
	}
	return tx.Commit()
}

const findingCols = `id, review_id, transaction_id, note, category_id, description, duplicate_of, odd_amount, status, before_category_id, before_description, updated_at`

func scanFinding(sc interface{ Scan(...any) error }) (DayReviewFinding, error) {
	var f DayReviewFinding
	err := sc.Scan(&f.ID, &f.ReviewID, &f.TransactionID, &f.Note, &f.CategoryID, &f.Description, &f.DuplicateOf, &f.OddAmount, &f.Status, &f.BeforeCategoryID, &f.BeforeDescription, &f.UpdatedAt)
	return f, err
}

// DayReviews are the person's latest reviews, newest first, with their findings
// (replaced ones left out).
func (s *Store) DayReviews(ctx context.Context, userID int64, limit int) ([]DayReview, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, day, trigger, mode, started_at, finished_at, checked, note, error FROM day_reviews WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	out := []DayReview{}
	idx := map[int64]int{}
	for rows.Next() {
		var r DayReview
		if err := rows.Scan(&r.ID, &r.UserID, &r.Day, &r.Trigger, &r.Mode, &r.StartedAt, &r.FinishedAt, &r.Checked, &r.Note, &r.Error); err != nil {
			rows.Close()
			return nil, err
		}
		r.Findings = []DayReviewFinding{}
		idx[r.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	frows, err := s.DB.QueryContext(ctx, `SELECT `+findingCols+` FROM day_review_findings WHERE user_id = ? AND status != 'replaced' AND review_id >= ? ORDER BY id`, userID, out[len(out)-1].ID)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		f, err := scanFinding(frows)
		if err != nil {
			return nil, err
		}
		if i, ok := idx[f.ReviewID]; ok {
			out[i].Findings = append(out[i].Findings, f)
		}
	}
	return out, frows.Err()
}

// OpenDayFindings counts the findings still waiting for the person.
func (s *Store) OpenDayFindings(ctx context.Context, userID int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM day_review_findings WHERE user_id = ? AND status = 'open'`, userID).Scan(&n)
	return n
}

// GetDayFinding returns one of the person's findings.
func (s *Store) GetDayFinding(ctx context.Context, userID, id int64) (*DayReviewFinding, error) {
	f, err := scanFinding(s.DB.QueryRowContext(ctx, `SELECT `+findingCols+` FROM day_review_findings WHERE id = ? AND user_id = ?`, id, userID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return &f, err
}

// SetDayFinding records what happened to a finding (and what applying it replaced).
func (s *Store) SetDayFinding(ctx context.Context, f *DayReviewFinding) error {
	f.UpdatedAt = Now()
	_, err := s.DB.ExecContext(ctx, `UPDATE day_review_findings SET status = ?, before_category_id = ?, before_description = ?, updated_at = ? WHERE id = ?`,
		f.Status, nullI(f.BeforeCategoryID), f.BeforeDescription, f.UpdatedAt, f.ID)
	return err
}
