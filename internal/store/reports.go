package store

import (
	"context"
)

// DaySum is the home-currency total of one kind on one day.
type DaySum struct {
	Date  string `json:"date"`
	Kind  string `json:"kind"`
	Sum   int64  `json:"sum"`
	Count int    `json:"count"`
}

// DailySums returns income and expense per day (transfers excluded).
func (s *Store) DailySums(ctx context.Context, accountIDs []int64, from, to string) ([]DaySum, error) {
	in, args := inList(accountIDs)
	args = append(args, from, to)
	rows, err := s.DB.QueryContext(ctx, `SELECT date, kind, SUM(amount_home), COUNT(1) FROM transactions
		WHERE kind != 'transfer' AND account_id IN `+in+` AND date >= ? AND date <= ? GROUP BY date, kind ORDER BY date`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DaySum{}
	for rows.Next() {
		var d DaySum
		if err := rows.Scan(&d.Date, &d.Kind, &d.Sum, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GroupSum is a total for one category / tag / payee.
type GroupSum struct {
	ID       int64  `json:"id"`
	ParentID *int64 `json:"parent_id,omitempty"`
	Name     string `json:"name"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
	Sum      int64  `json:"sum"`
	Count    int    `json:"count"`
	Prev     int64  `json:"prev"`    // same span just before
	Average  int64  `json:"average"` // per-period average over the comparison window
}

// CategorySums totals one kind per category over [from, to].
func (s *Store) CategorySums(ctx context.Context, accountIDs []int64, kind, from, to string) ([]GroupSum, error) {
	in, args := inList(accountIDs)
	args = append(args, kind, from, to)
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(t.category_id, 0), c.parent_id, COALESCE(c.name, ''), COALESCE(c.icon, ''), COALESCE(c.color, ''), SUM(t.amount_home), COUNT(1)
		FROM transactions t LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.account_id IN `+in+` AND t.kind = ? AND t.date >= ? AND t.date <= ? GROUP BY COALESCE(t.category_id, 0) ORDER BY SUM(t.amount_home) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupSum{}
	for rows.Next() {
		var g GroupSum
		if err := rows.Scan(&g.ID, &g.ParentID, &g.Name, &g.Icon, &g.Color, &g.Sum, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// TagSums totals expenses per tag over [from, to].
func (s *Store) TagSums(ctx context.Context, accountIDs []int64, kind, from, to string) ([]GroupSum, error) {
	in, args := inList(accountIDs)
	args = append(args, kind, from, to)
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id, g.name, g.color, SUM(t.amount_home), COUNT(1)
		FROM transaction_tags x JOIN tags g ON g.id = x.tag_id JOIN transactions t ON t.id = x.transaction_id
		WHERE t.account_id IN `+in+` AND t.kind = ? AND t.date >= ? AND t.date <= ? GROUP BY g.id ORDER BY SUM(t.amount_home) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupSum{}
	for rows.Next() {
		var g GroupSum
		if err := rows.Scan(&g.ID, &g.Name, &g.Color, &g.Sum, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// TopSpends returns where money went by payee (or description when there is no payee).
func (s *Store) TopSpends(ctx context.Context, accountIDs []int64, from, to string, limit int) ([]GroupSum, error) {
	in, args := inList(accountIDs)
	args = append(args, from, to, limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT CASE WHEN payee != '' THEN payee ELSE description END AS who, SUM(amount_home), COUNT(1)
		FROM transactions WHERE account_id IN `+in+` AND kind = 'expense' AND date >= ? AND date <= ?
		GROUP BY lower(who) ORDER BY SUM(amount_home) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupSum{}
	for rows.Next() {
		var g GroupSum
		if err := rows.Scan(&g.Name, &g.Sum, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FirstTransactionDate is the oldest date on the given accounts ("" when none).
func (s *Store) FirstTransactionDate(ctx context.Context, accountIDs []int64) string {
	in, args := inList(accountIDs)
	var d *string
	_ = s.DB.QueryRowContext(ctx, `SELECT MIN(date) FROM transactions WHERE account_id IN `+in, args...).Scan(&d)
	if d == nil {
		return ""
	}
	return *d
}

// ExpenseRows returns (date, description, payee, amount_home, account, kind, subscription) for pattern detection.
type PatternRow struct {
	ID          int64
	Date        string
	Description string
	Payee       string
	Amount      int64
	AmountHome  int64
	Currency    string
	AccountID   int64
	CategoryID  *int64
	Kind        string
	SubID       *int64
}

// PatternRows lists expense + income rows since from, oldest first.
func (s *Store) PatternRows(ctx context.Context, accountIDs []int64, from string) ([]PatternRow, error) {
	in, args := inList(accountIDs)
	args = append(args, from)
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, t.date, t.description, t.payee, t.amount, t.amount_home, a.currency, t.account_id, t.category_id, t.kind, t.subscription_id
		FROM transactions t JOIN accounts a ON a.id = t.account_id WHERE t.kind != 'transfer' AND t.account_id IN `+in+` AND t.date >= ? ORDER BY t.date, t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PatternRow
	for rows.Next() {
		var r PatternRow
		if err := rows.Scan(&r.ID, &r.Date, &r.Description, &r.Payee, &r.Amount, &r.AmountHome, &r.Currency, &r.AccountID, &r.CategoryID, &r.Kind, &r.SubID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
