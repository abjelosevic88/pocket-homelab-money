package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// TxFilter narrows a transaction list. AccountIDs is the visibility fence and
// is always applied (nil = nothing visible).
type TxFilter struct {
	AccountIDs     []int64
	AccountID      int64
	Kind           string
	CategoryIDs    []int64
	Uncategorized  bool
	Tag            string
	From, To       string
	Q              string
	Payee          string
	MinHome        *int64
	MaxHome        *int64
	SubscriptionID int64
	Source         string
	ExternalPrefix string
	Limit, Offset  int
	Oldest         bool
}

const txSelect = `SELECT t.id, t.kind, t.date, t.time, t.account_id, t.amount, t.to_account_id, t.to_amount, t.foreign_amount, t.foreign_currency,
	t.amount_home, t.fx_rate, t.description, t.payee, t.category_id, t.notes, t.subscription_id, t.source, t.external_id, t.lat, t.lng,
	t.created_by, t.created_at, t.updated_at,
	a.name, a.currency, COALESCE(b.name, ''), COALESCE(b.currency, ''), COALESCE(c.name, ''), COALESCE(c.icon, ''), COALESCE(c.color, ''),
	COALESCE(s.name, ''), COALESCE(u.name, u.email, ''), (SELECT COUNT(1) FROM attachments x WHERE x.transaction_id = t.id)
	FROM transactions t
	JOIN accounts a ON a.id = t.account_id
	LEFT JOIN accounts b ON b.id = t.to_account_id
	LEFT JOIN categories c ON c.id = t.category_id
	LEFT JOIN subscriptions s ON s.id = t.subscription_id
	LEFT JOIN users u ON u.id = t.created_by`

func scanTx(row interface{ Scan(...any) error }) (*Transaction, error) {
	var t Transaction
	err := row.Scan(&t.ID, &t.Kind, &t.Date, &t.Time, &t.AccountID, &t.Amount, &t.ToAccountID, &t.ToAmount, &t.ForeignAmount, &t.ForeignCurrency,
		&t.AmountHome, &t.FXRate, &t.Description, &t.Payee, &t.CategoryID, &t.Notes, &t.SubscriptionID, &t.Source, &t.ExternalID, &t.Lat, &t.Lng,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
		&t.AccountName, &t.Currency, &t.ToAccountName, &t.ToCurrency, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor,
		&t.SubscriptionNm, &t.CreatedByName, &t.AttachmentCount)
	if err != nil {
		return nil, notFound(err)
	}
	t.Tags = []string{}
	return &t, nil
}

func inList(ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "(NULL)", nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return "(" + strings.Join(ph, ",") + ")", args
}

func (f *TxFilter) where() (string, []any) {
	var conds []string
	var args []any
	in, a := inList(f.AccountIDs)
	conds = append(conds, "(t.account_id IN "+in+" OR t.to_account_id IN "+in+")")
	args = append(args, a...)
	args = append(args, a...)
	if f.AccountID > 0 {
		conds = append(conds, "(t.account_id = ? OR t.to_account_id = ?)")
		args = append(args, f.AccountID, f.AccountID)
	}
	if f.Kind != "" {
		conds = append(conds, "t.kind = ?")
		args = append(args, f.Kind)
	}
	if f.Uncategorized {
		conds = append(conds, "t.category_id IS NULL AND t.kind != 'transfer'")
	} else if len(f.CategoryIDs) > 0 {
		in, a := inList(f.CategoryIDs)
		conds = append(conds, "t.category_id IN "+in)
		args = append(args, a...)
	}
	if f.Tag != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM transaction_tags x JOIN tags g ON g.id = x.tag_id WHERE x.transaction_id = t.id AND g.name = ? COLLATE NOCASE)")
		args = append(args, f.Tag)
	}
	if f.From != "" {
		conds = append(conds, "t.date >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		conds = append(conds, "t.date <= ?")
		args = append(args, f.To)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		for _, w := range strings.Fields(strings.ToLower(q)) {
			conds = append(conds, "(lower(t.description) LIKE ? OR lower(t.payee) LIKE ? OR lower(t.notes) LIKE ? OR lower(COALESCE(c.name,'')) LIKE ? OR EXISTS (SELECT 1 FROM transaction_tags x JOIN tags g ON g.id = x.tag_id WHERE x.transaction_id = t.id AND lower(g.name) LIKE ?))")
			like := "%" + w + "%"
			args = append(args, like, like, like, like, like)
		}
	}
	if f.Payee != "" {
		conds = append(conds, "t.payee = ? COLLATE NOCASE")
		args = append(args, f.Payee)
	}
	if f.MinHome != nil {
		conds = append(conds, "t.amount_home >= ?")
		args = append(args, *f.MinHome)
	}
	if f.MaxHome != nil {
		conds = append(conds, "t.amount_home <= ?")
		args = append(args, *f.MaxHome)
	}
	if f.SubscriptionID > 0 {
		conds = append(conds, "t.subscription_id = ?")
		args = append(args, f.SubscriptionID)
	}
	if f.Source != "" {
		conds = append(conds, "t.source = ?")
		args = append(args, f.Source)
	}
	if f.ExternalPrefix != "" {
		conds = append(conds, "t.external_id LIKE ? || '%'")
		args = append(args, f.ExternalPrefix)
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// TxTotals summarises a filtered list in the home currency.
type TxTotals struct {
	Count     int   `json:"count"`
	Income    int64 `json:"income"`
	Expense   int64 `json:"expense"`
	Transfers int64 `json:"transfers"`
	Net       int64 `json:"net"`
}

// ListTransactions returns one page plus totals over the whole filter.
func (s *Store) ListTransactions(ctx context.Context, f TxFilter) ([]Transaction, TxTotals, error) {
	where, args := f.where()
	var tot TxTotals
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(CASE WHEN t.kind='income' THEN t.amount_home END),0),
		COALESCE(SUM(CASE WHEN t.kind='expense' THEN t.amount_home END),0), COALESCE(SUM(CASE WHEN t.kind='transfer' THEN t.amount_home END),0)
		FROM transactions t JOIN accounts a ON a.id = t.account_id LEFT JOIN categories c ON c.id = t.category_id`+where, args...).
		Scan(&tot.Count, &tot.Income, &tot.Expense, &tot.Transfers)
	if err != nil {
		return nil, tot, err
	}
	tot.Net = tot.Income - tot.Expense
	order := " ORDER BY t.date DESC, t.time DESC, t.id DESC"
	if f.Oldest {
		order = " ORDER BY t.date, t.time, t.id"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q := txSelect + where + order + fmt.Sprintf(" LIMIT %d OFFSET %d", limit, max(0, f.Offset))
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, tot, err
	}
	out := []Transaction{}
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			rows.Close()
			return nil, tot, err
		}
		out = append(out, *t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, tot, err
	}
	return out, tot, s.attachTags(ctx, out)
}

func (s *Store) attachTags(ctx context.Context, txs []Transaction) error {
	if len(txs) == 0 {
		return nil
	}
	ids := make([]int64, len(txs))
	idx := map[int64]int{}
	for i := range txs {
		ids[i] = txs[i].ID
		idx[txs[i].ID] = i
	}
	in, args := inList(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT x.transaction_id, g.name FROM transaction_tags x JOIN tags g ON g.id = x.tag_id WHERE x.transaction_id IN `+in+` ORDER BY lower(g.name)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		txs[idx[id]].Tags = append(txs[idx[id]].Tags, name)
	}
	return rows.Err()
}

// GetTransaction returns one transaction with tags and attachments.
func (s *Store) GetTransaction(ctx context.Context, id int64) (*Transaction, error) {
	t, err := scanTx(s.DB.QueryRowContext(ctx, txSelect+` WHERE t.id = ?`, id))
	if err != nil {
		return nil, err
	}
	list := []Transaction{*t}
	if err := s.attachTags(ctx, list); err != nil {
		return nil, err
	}
	*t = list[0]
	t.Attachments, err = s.ListAttachments(ctx, id)
	return t, err
}

// TransactionByExternalID finds an imported transaction.
func (s *Store) TransactionByExternalID(ctx context.Context, ext string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM transactions WHERE external_id = ? LIMIT 1`, ext).Scan(&id)
	return id, notFound(err)
}

// CreateTransaction inserts a transaction and its tags.
func (s *Store) CreateTransaction(ctx context.Context, t *Transaction) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO transactions (kind, date, time, account_id, amount, to_account_id, to_amount, foreign_amount, foreign_currency,
		amount_home, fx_rate, description, payee, category_id, notes, subscription_id, source, external_id, lat, lng, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Kind, t.Date, t.Time, t.AccountID, t.Amount, nullI(t.ToAccountID), t.ToAmount, t.ForeignAmount, t.ForeignCurrency,
		t.AmountHome, t.FXRate, t.Description, t.Payee, nullI(t.CategoryID), t.Notes, nullI(t.SubscriptionID), t.Source, t.ExternalID, nullF(t.Lat), nullF(t.Lng), nullI(t.CreatedBy))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	t.ID, _ = res.LastInsertId()
	if err := setTags(ctx, tx, t.ID, t.Tags); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// UpdateTransaction saves a transaction and replaces its tags.
func (s *Store) UpdateTransaction(ctx context.Context, t *Transaction) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE transactions SET kind = ?, date = ?, time = ?, account_id = ?, amount = ?, to_account_id = ?, to_amount = ?,
		foreign_amount = ?, foreign_currency = ?, amount_home = ?, fx_rate = ?, description = ?, payee = ?, category_id = ?, notes = ?, subscription_id = ?,
		source = ?, external_id = ?, lat = ?, lng = ?, updated_at = ? WHERE id = ?`,
		t.Kind, t.Date, t.Time, t.AccountID, t.Amount, nullI(t.ToAccountID), t.ToAmount, t.ForeignAmount, t.ForeignCurrency, t.AmountHome, t.FXRate,
		t.Description, t.Payee, nullI(t.CategoryID), t.Notes, nullI(t.SubscriptionID), t.Source, t.ExternalID, nullF(t.Lat), nullF(t.Lng), Now(), t.ID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id = ?`, t.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := setTags(ctx, tx, t.ID, t.Tags); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func setTags(ctx context.Context, tx *sql.Tx, txID int64, tags []string) error {
	seen := map[string]bool{}
	for _, name := range tags {
		name = strings.TrimSpace(name)
		k := strings.ToLower(name)
		if name == "" || seen[k] {
			continue
		}
		seen[k] = true
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&id); err != nil {
			res, err := tx.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?)`, name)
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO transaction_tags (transaction_id, tag_id) VALUES (?, ?)`, txID, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTransaction removes a transaction (attachments cascade; files are the caller's).
func (s *Store) DeleteTransaction(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, id)
	return err
}

// BulkUpdate applies a category and/or tags to many transactions.
func (s *Store) BulkUpdate(ctx context.Context, ids []int64, categoryID *int64, clearCategory bool, addTags, removeTags []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if categoryID != nil || clearCategory {
			if _, err := tx.ExecContext(ctx, `UPDATE transactions SET category_id = ?, updated_at = ? WHERE id = ? AND kind != 'transfer'`, nullI(categoryID), Now(), id); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if err := setTags(ctx, tx, id, addTags); err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, name := range removeTags {
			if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id = ? AND tag_id IN (SELECT id FROM tags WHERE name = ? COLLATE NOCASE)`, id, name); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit()
}

// SetTransactionSubscription links (or unlinks) a transaction to a subscription.
func (s *Store) SetTransactionSubscription(ctx context.Context, txID int64, subID *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE transactions SET subscription_id = ? WHERE id = ?`, nullI(subID), txID)
	return err
}

// UpdateHomeAmount stores a recomputed home value.
func (s *Store) UpdateHomeAmount(ctx context.Context, id, home int64, rate float64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE transactions SET amount_home = ?, fx_rate = ? WHERE id = ?`, home, rate, id)
	return err
}

// ---- attachments ----

// ListAttachments lists a transaction's files.
func (s *Store) ListAttachments(ctx context.Context, txID int64) ([]Attachment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, transaction_id, name, mime, size, path, created_at FROM attachments WHERE transaction_id = ? ORDER BY id`, txID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.TransactionID, &a.Name, &a.Mime, &a.Size, &a.Path, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAttachment returns one file record.
func (s *Store) GetAttachment(ctx context.Context, id int64) (*Attachment, error) {
	var a Attachment
	err := s.DB.QueryRowContext(ctx, `SELECT id, transaction_id, name, mime, size, path, created_at FROM attachments WHERE id = ?`, id).
		Scan(&a.ID, &a.TransactionID, &a.Name, &a.Mime, &a.Size, &a.Path, &a.CreatedAt)
	return &a, notFound(err)
}

// CreateAttachment stores a file record.
func (s *Store) CreateAttachment(ctx context.Context, a *Attachment) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO attachments (transaction_id, name, mime, size, path) VALUES (?, ?, ?, ?, ?)`, a.TransactionID, a.Name, a.Mime, a.Size, a.Path)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// DeleteAttachment removes a file record.
func (s *Store) DeleteAttachment(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id)
	return err
}

// AttachmentPaths returns the stored paths of a transaction's files.
func (s *Store) AttachmentPaths(ctx context.Context, txID int64) []string {
	as, _ := s.ListAttachments(ctx, txID)
	var out []string
	for _, a := range as {
		out = append(out, a.Path)
	}
	return out
}

// ---- autocomplete and learning ----

// Suggestion is a value seen before with how it was usually filed.
type Suggestion struct {
	Text       string `json:"text"`
	Count      int    `json:"count"`
	CategoryID *int64 `json:"category_id"`
	Payee      string `json:"payee,omitempty"`
	LastAmount int64  `json:"last_amount"`
	AccountID  int64  `json:"account_id"`
	Kind       string `json:"kind"`
}

// DescriptionSuggestions returns past descriptions starting with / containing q,
// most used first, each with the category it was filed under most often.
func (s *Store) DescriptionSuggestions(ctx context.Context, accountIDs []int64, q string, limit int) ([]Suggestion, error) {
	in, args := inList(accountIDs)
	like := "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
	args = append(args, like, strings.ToLower(strings.TrimSpace(q))+"%", limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT description, COUNT(1) n, MAX(date) FROM transactions
		WHERE account_id IN `+in+` AND description != '' AND lower(description) LIKE ?
		GROUP BY lower(description) ORDER BY (lower(description) LIKE ?) DESC, n DESC, MAX(date) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []Suggestion
	for rows.Next() {
		var sg Suggestion
		var last string
		if err := rows.Scan(&sg.Text, &sg.Count, &last); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, sg)
	}
	rows.Close()
	for i := range out {
		_ = s.DB.QueryRowContext(ctx, `SELECT category_id, payee, amount, account_id, kind FROM transactions WHERE lower(description) = lower(?)
			ORDER BY date DESC, id DESC LIMIT 1`, out[i].Text).Scan(&out[i].CategoryID, &out[i].Payee, &out[i].LastAmount, &out[i].AccountID, &out[i].Kind)
		if c := s.LearnedCategory(ctx, out[i].Text); c != nil {
			out[i].CategoryID = c
		}
	}
	if out == nil {
		out = []Suggestion{}
	}
	return out, nil
}

// LearnedCategory is the category most often used for this description (or payee).
func (s *Store) LearnedCategory(ctx context.Context, text string) *int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var id sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT category_id FROM transactions WHERE category_id IS NOT NULL AND (lower(description) = lower(?) OR lower(payee) = lower(?))
		GROUP BY category_id ORDER BY COUNT(1) DESC, MAX(date) DESC LIMIT 1`, text, text).Scan(&id)
	if !id.Valid {
		return nil
	}
	v := id.Int64
	return &v
}

// Payees returns payees seen before, most used first.
func (s *Store) Payees(ctx context.Context, accountIDs []int64) ([]Suggestion, error) {
	in, args := inList(accountIDs)
	rows, err := s.DB.QueryContext(ctx, `SELECT payee, COUNT(1) n FROM transactions WHERE account_id IN `+in+` AND payee != '' GROUP BY lower(payee) ORDER BY n DESC LIMIT 300`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Suggestion{}
	for rows.Next() {
		var sg Suggestion
		if err := rows.Scan(&sg.Text, &sg.Count); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// AllTransactionIDs returns every id (used for maintenance passes).
func (s *Store) AllTransactionIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM transactions ORDER BY id`)
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
