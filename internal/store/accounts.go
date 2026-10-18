package store

import (
	"context"
	"database/sql"
	"strings"
)

const accountCols = `a.id, a.owner_id, a.name, a.kind, a.currency, a.opening_balance, a.opening_date, a.institution, a.number, a.color, a.icon,
	a.credit_limit, a.include_net_worth, a.everyday, a.archived, a.sort_order, a.notes, a.external_id, a.created_at, a.updated_at`

func scanAccount(row interface{ Scan(...any) error }, extra ...any) (*Account, error) {
	var a Account
	var nw, ev, arch int
	dst := []any{&a.ID, &a.OwnerID, &a.Name, &a.Kind, &a.Currency, &a.OpeningBalance, &a.OpeningDate, &a.Institution, &a.Number, &a.Color, &a.Icon,
		&a.CreditLimit, &nw, &ev, &arch, &a.SortOrder, &a.Notes, &a.ExternalID, &a.CreatedAt, &a.UpdatedAt}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return nil, notFound(err)
	}
	a.IncludeNetWorth, a.Everyday, a.Archived = nw == 1, ev == 1, arch == 1
	return &a, nil
}

// visibleAccounts is the SQL condition for accounts user ? may see.
const visibleAccounts = `(a.owner_id = ? OR EXISTS (SELECT 1 FROM account_members m WHERE m.account_id = a.id AND m.user_id = ?))`

// ListAccounts returns the accounts a user owns or was given, with balances.
func (s *Store) ListAccounts(ctx context.Context, userID int64, withArchived bool) ([]Account, error) {
	q := `SELECT ` + accountCols + `, CASE WHEN a.owner_id = ? THEN 'owner' ELSE COALESCE((SELECT role FROM account_members m WHERE m.account_id = a.id AND m.user_id = ?), 'viewer') END
		FROM accounts a WHERE ` + visibleAccounts
	if !withArchived {
		q += ` AND a.archived = 0`
	}
	q += ` ORDER BY a.archived, a.sort_order, a.id`
	rows, err := s.DB.QueryContext(ctx, q, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	out := []Account{}
	for rows.Next() {
		var role string
		a, err := scanAccount(rows, &role)
		if err != nil {
			rows.Close()
			return nil, err
		}
		a.Role = role
		out = append(out, *a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	bal, err := s.Balances(ctx, "")
	if err != nil {
		return nil, err
	}
	for i := range out {
		b := bal[out[i].ID]
		out[i].Balance = out[i].OpeningBalance + b.Delta
		out[i].TxCount = b.Count
		out[i].LastTxDate = b.Last
	}
	return out, nil
}

// AllAccounts lists every account on the server (importer, compat lookups).
func (s *Store) AllAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts a ORDER BY a.sort_order, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetAccount returns one account (no access check).
func (s *Store) GetAccount(ctx context.Context, id int64) (*Account, error) {
	a, err := scanAccount(s.DB.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts a WHERE a.id = ?`, id))
	if err != nil {
		return nil, err
	}
	bal, err := s.Balances(ctx, "")
	if err != nil {
		return nil, err
	}
	b := bal[a.ID]
	a.Balance, a.TxCount, a.LastTxDate = a.OpeningBalance+b.Delta, b.Count, b.Last
	return a, nil
}

// AccountRole is the user's role on an account: owner | editor | viewer | "" (no access).
func (s *Store) AccountRole(ctx context.Context, accountID, userID int64) string {
	var owner int64
	if err := s.DB.QueryRowContext(ctx, `SELECT owner_id FROM accounts WHERE id = ?`, accountID).Scan(&owner); err != nil {
		return ""
	}
	if owner == userID {
		return "owner"
	}
	var role string
	if err := s.DB.QueryRowContext(ctx, `SELECT role FROM account_members WHERE account_id = ? AND user_id = ?`, accountID, userID).Scan(&role); err != nil {
		return ""
	}
	return role
}

// VisibleAccountIDs returns the ids a user may see (archived included).
func (s *Store) VisibleAccountIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE `+visibleAccounts, userID, userID)
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

// CreateAccount inserts an account.
func (s *Store) CreateAccount(ctx context.Context, a *Account) error {
	if a.SortOrder == 0 {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM accounts`).Scan(&a.SortOrder)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO accounts (owner_id, name, kind, currency, opening_balance, opening_date, institution, number, color, icon,
		credit_limit, include_net_worth, everyday, archived, sort_order, notes, external_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.OwnerID, a.Name, a.Kind, a.Currency, a.OpeningBalance, a.OpeningDate, a.Institution, a.Number, a.Color, a.Icon,
		a.CreditLimit, boolInt(a.IncludeNetWorth), boolInt(a.Everyday), boolInt(a.Archived), a.SortOrder, a.Notes, a.ExternalID)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// UpdateAccount saves an account.
func (s *Store) UpdateAccount(ctx context.Context, a *Account) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE accounts SET name = ?, kind = ?, currency = ?, opening_balance = ?, opening_date = ?, institution = ?, number = ?,
		color = ?, icon = ?, credit_limit = ?, include_net_worth = ?, everyday = ?, archived = ?, sort_order = ?, notes = ?, external_id = ?, updated_at = ? WHERE id = ?`,
		a.Name, a.Kind, a.Currency, a.OpeningBalance, a.OpeningDate, a.Institution, a.Number, a.Color, a.Icon, a.CreditLimit,
		boolInt(a.IncludeNetWorth), boolInt(a.Everyday), boolInt(a.Archived), a.SortOrder, a.Notes, a.ExternalID, Now(), a.ID)
	return err
}

// DeleteAccount removes an account and every transaction on it.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	return err
}

// ReorderAccounts stores a new order.
func (s *Store) ReorderAccounts(ctx context.Context, ids []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET sort_order = ? WHERE id = ?`, i+1, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// AccountMembers lists who an account is shared with.
func (s *Store) AccountMembers(ctx context.Context, accountID int64) ([]AccountMember, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.user_id, u.name, u.email, m.role FROM account_members m JOIN users u ON u.id = m.user_id WHERE m.account_id = ? ORDER BY u.name, u.email`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountMember{}
	for rows.Next() {
		var m AccountMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetAccountMembers replaces the share list.
func (s *Store) SetAccountMembers(ctx context.Context, accountID int64, members []AccountMember) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_members WHERE account_id = ?`, accountID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, m := range members {
		role := m.Role
		if role != "viewer" {
			role = "editor"
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO account_members (account_id, user_id, role) VALUES (?, ?, ?)`, accountID, m.UserID, role); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// BalanceInfo is the movement on one account.
type BalanceInfo struct {
	Delta int64
	Count int
	Last  string
}

// Balances sums every transaction per account up to and including date ("" = all).
func (s *Store) Balances(ctx context.Context, upTo string) (map[int64]BalanceInfo, error) {
	cond, args := "", []any{}
	if upTo != "" {
		cond, args = ` AND date <= ?`, []any{upTo, upTo}
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT acct, SUM(delta), COUNT(1), MAX(date) FROM (
			SELECT account_id AS acct, CASE kind WHEN 'income' THEN amount ELSE -amount END AS delta, date FROM transactions WHERE 1=1`+cond+`
			UNION ALL
			SELECT to_account_id, to_amount, date FROM transactions WHERE kind = 'transfer' AND to_account_id IS NOT NULL`+cond+`
		) GROUP BY acct`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]BalanceInfo{}
	for rows.Next() {
		var id int64
		var b BalanceInfo
		var last sql.NullString
		if err := rows.Scan(&id, &b.Delta, &b.Count, &last); err != nil {
			return nil, err
		}
		b.Last = last.String
		out[id] = b
	}
	return out, rows.Err()
}

// AccountDailyBalances returns the balance at the end of each day that had a
// movement in [from, to], plus the balance just before from.
func (s *Store) AccountDailyBalances(ctx context.Context, accountID int64, from, to string) (start int64, days []DayAmount, err error) {
	a, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return 0, nil, err
	}
	var before sql.NullInt64
	err = s.DB.QueryRowContext(ctx, `SELECT SUM(d) FROM (
		SELECT CASE kind WHEN 'income' THEN amount ELSE -amount END AS d FROM transactions WHERE account_id = ? AND date < ?
		UNION ALL SELECT to_amount FROM transactions WHERE to_account_id = ? AND kind = 'transfer' AND date < ?)`, accountID, from, accountID, from).Scan(&before)
	if err != nil {
		return 0, nil, err
	}
	start = a.OpeningBalance + before.Int64
	rows, err := s.DB.QueryContext(ctx, `SELECT date, SUM(d) FROM (
		SELECT date, CASE kind WHEN 'income' THEN amount ELSE -amount END AS d FROM transactions WHERE account_id = ? AND date >= ? AND date <= ?
		UNION ALL SELECT date, to_amount FROM transactions WHERE to_account_id = ? AND kind = 'transfer' AND date >= ? AND date <= ?)
		GROUP BY date ORDER BY date`, accountID, from, to, accountID, from, to)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	run := start
	for rows.Next() {
		var d DayAmount
		var delta int64
		if err := rows.Scan(&d.Date, &delta); err != nil {
			return 0, nil, err
		}
		run += delta
		d.Amount = run
		days = append(days, d)
	}
	return start, days, rows.Err()
}

// DayAmount is a value on a date.
type DayAmount struct {
	Date   string `json:"date"`
	Amount int64  `json:"amount"`
}

// FindAccountByName matches an account name case-insensitively among ids.
func FindAccountByName(accts []Account, name string) *Account {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return nil
	}
	for i := range accts {
		if strings.ToLower(accts[i].Name) == n {
			return &accts[i]
		}
	}
	for i := range accts {
		if strings.HasPrefix(strings.ToLower(accts[i].Name), n) {
			return &accts[i]
		}
	}
	return nil
}
