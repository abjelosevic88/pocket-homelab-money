package store

import (
	"context"
	"encoding/json"
	"strings"
)

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func parseList(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func jsonIDs(v []int64) string {
	if v == nil {
		v = []int64{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func parseIDs(s string) []int64 {
	var out []int64
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = []int64{}
	}
	return out
}

const templateCols = `t.id, t.owner_id, t.name, t.aliases, t.kind, t.amount, t.currency, t.account_id, t.to_account_id, t.category_id, t.tags,
	t.description, t.payee, t.notes, t.pinned, t.use_count, t.last_used_at, t.sort_order, t.external_id, t.created_at,
	COALESCE(c.name, ''), COALESCE(c.icon, ''), COALESCE(a.name, '')`

func scanTemplate(row interface{ Scan(...any) error }) (*Template, error) {
	var t Template
	var tags string
	var pinned int
	err := row.Scan(&t.ID, &t.OwnerID, &t.Name, &t.Aliases, &t.Kind, &t.Amount, &t.Currency, &t.AccountID, &t.ToAccountID, &t.CategoryID, &tags,
		&t.Description, &t.Payee, &t.Notes, &pinned, &t.UseCount, &t.LastUsedAt, &t.SortOrder, &t.ExternalID, &t.CreatedAt,
		&t.CategoryName, &t.CategoryIcon, &t.AccountName)
	if err != nil {
		return nil, notFound(err)
	}
	t.Tags, t.Pinned = parseList(tags), pinned == 1
	return &t, nil
}

// ListTemplates returns a user's templates: pinned first, then most used.
func (s *Store) ListTemplates(ctx context.Context, ownerID int64) ([]Template, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+templateCols+` FROM templates t LEFT JOIN categories c ON c.id = t.category_id LEFT JOIN accounts a ON a.id = t.account_id
		WHERE t.owner_id = ? ORDER BY t.pinned DESC, t.use_count DESC, t.sort_order, lower(t.name)`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTemplate returns one template.
func (s *Store) GetTemplate(ctx context.Context, id int64) (*Template, error) {
	return scanTemplate(s.DB.QueryRowContext(ctx, `SELECT `+templateCols+` FROM templates t LEFT JOIN categories c ON c.id = t.category_id LEFT JOIN accounts a ON a.id = t.account_id WHERE t.id = ?`, id))
}

// CreateTemplate inserts a template.
func (s *Store) CreateTemplate(ctx context.Context, t *Template) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO templates (owner_id, name, aliases, kind, amount, currency, account_id, to_account_id, category_id, tags, description, payee, notes, pinned, use_count, sort_order, external_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.OwnerID, t.Name, t.Aliases, t.Kind, nullI(t.Amount), t.Currency, nullI(t.AccountID), nullI(t.ToAccountID), nullI(t.CategoryID), jsonList(t.Tags),
		t.Description, t.Payee, t.Notes, boolInt(t.Pinned), t.UseCount, t.SortOrder, t.ExternalID)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

// UpdateTemplate saves a template.
func (s *Store) UpdateTemplate(ctx context.Context, t *Template) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE templates SET name = ?, aliases = ?, kind = ?, amount = ?, currency = ?, account_id = ?, to_account_id = ?, category_id = ?, tags = ?,
		description = ?, payee = ?, notes = ?, pinned = ?, sort_order = ?, external_id = ? WHERE id = ?`,
		t.Name, t.Aliases, t.Kind, nullI(t.Amount), t.Currency, nullI(t.AccountID), nullI(t.ToAccountID), nullI(t.CategoryID), jsonList(t.Tags),
		t.Description, t.Payee, t.Notes, boolInt(t.Pinned), t.SortOrder, t.ExternalID, t.ID)
	return err
}

// DeleteTemplate removes a template.
func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM templates WHERE id = ?`, id)
	return err
}

// TouchTemplate counts one use (server-side, so every device sorts the same).
func (s *Store) TouchTemplate(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE templates SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`, Now(), id)
}

// TemplateByExternalID finds an imported template.
func (s *Store) TemplateByExternalID(ctx context.Context, ext string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM templates WHERE external_id = ?`, ext).Scan(&id)
	return id, notFound(err)
}

// ---- subscriptions ----

const subCols = `s.id, s.owner_id, s.name, s.kind, s.amount, s.amount_max, s.currency, s.account_id, s.to_account_id, s.category_id, s.tags, s.payee, s.description,
	s.freq, s.every, s.start_date, s.next_date, s.end_date, s.mode, s.notify_days, s.match_text, s.url, s.icon, s.notes, s.active, s.notified_for, s.external_id,
	s.weekend, s.nth, s.max_count, s.done_count, s.created_at, s.updated_at, COALESCE(c.name, ''), COALESCE(c.icon, ''), COALESCE(a.name, ''),
	COALESCE((SELECT MAX(date) FROM transactions t WHERE t.subscription_id = s.id), ''),
	COALESCE((SELECT amount FROM transactions t WHERE t.subscription_id = s.id ORDER BY date DESC, id DESC LIMIT 1), 0),
	(SELECT COUNT(1) FROM transactions t WHERE t.subscription_id = s.id)`

const subFrom = ` FROM subscriptions s LEFT JOIN categories c ON c.id = s.category_id LEFT JOIN accounts a ON a.id = s.account_id`

func scanSub(row interface{ Scan(...any) error }) (*Subscription, error) {
	var x Subscription
	var tags string
	var active int
	err := row.Scan(&x.ID, &x.OwnerID, &x.Name, &x.Kind, &x.Amount, &x.AmountMax, &x.Currency, &x.AccountID, &x.ToAccountID, &x.CategoryID, &tags, &x.Payee, &x.Description,
		&x.Freq, &x.Every, &x.StartDate, &x.NextDate, &x.EndDate, &x.Mode, &x.NotifyDays, &x.MatchText, &x.URL, &x.Icon, &x.Notes, &active, &x.NotifiedFor, &x.ExternalID,
		&x.Weekend, &x.Nth, &x.MaxCount, &x.DoneCount, &x.CreatedAt, &x.UpdatedAt, &x.CategoryName, &x.CategoryIcon, &x.AccountName, &x.LastPaid, &x.LastAmount, &x.PaidCount)
	if err != nil {
		return nil, notFound(err)
	}
	x.Tags, x.Active = parseList(tags), active == 1
	return &x, nil
}

// ListSubscriptions returns a user's subscriptions (ownerID 0 = everyone's).
func (s *Store) ListSubscriptions(ctx context.Context, ownerID int64) ([]Subscription, error) {
	q, args := `SELECT `+subCols+subFrom, []any{}
	if ownerID > 0 {
		q += ` WHERE s.owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY s.active DESC, s.next_date, lower(s.name)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		x, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// GetSubscription returns one subscription.
func (s *Store) GetSubscription(ctx context.Context, id int64) (*Subscription, error) {
	return scanSub(s.DB.QueryRowContext(ctx, `SELECT `+subCols+subFrom+` WHERE s.id = ?`, id))
}

// CreateSubscription inserts one.
func (s *Store) CreateSubscription(ctx context.Context, x *Subscription) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO subscriptions (owner_id, name, kind, amount, amount_max, currency, account_id, to_account_id, category_id, tags, payee, description,
		freq, every, start_date, next_date, end_date, mode, notify_days, match_text, url, icon, notes, active, external_id, weekend, nth, max_count, done_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.OwnerID, x.Name, x.Kind, x.Amount, x.AmountMax, x.Currency, nullI(x.AccountID), nullI(x.ToAccountID), nullI(x.CategoryID), jsonList(x.Tags), x.Payee, x.Description,
		x.Freq, x.Every, x.StartDate, x.NextDate, x.EndDate, x.Mode, x.NotifyDays, x.MatchText, x.URL, x.Icon, x.Notes, boolInt(x.Active), x.ExternalID,
		x.Weekend, x.Nth, x.MaxCount, x.DoneCount)
	if err != nil {
		return err
	}
	x.ID, _ = res.LastInsertId()
	return nil
}

// UpdateSubscription saves one.
func (s *Store) UpdateSubscription(ctx context.Context, x *Subscription) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE subscriptions SET name = ?, kind = ?, amount = ?, amount_max = ?, currency = ?, account_id = ?, to_account_id = ?, category_id = ?, tags = ?,
		payee = ?, description = ?, freq = ?, every = ?, start_date = ?, next_date = ?, end_date = ?, mode = ?, notify_days = ?, match_text = ?, url = ?, icon = ?, notes = ?,
		active = ?, notified_for = ?, external_id = ?, weekend = ?, nth = ?, max_count = ?, done_count = ?, updated_at = ? WHERE id = ?`,
		x.Name, x.Kind, x.Amount, x.AmountMax, x.Currency, nullI(x.AccountID), nullI(x.ToAccountID), nullI(x.CategoryID), jsonList(x.Tags),
		x.Payee, x.Description, x.Freq, x.Every, x.StartDate, x.NextDate, x.EndDate, x.Mode, x.NotifyDays, x.MatchText, x.URL, x.Icon, x.Notes,
		boolInt(x.Active), x.NotifiedFor, x.ExternalID, x.Weekend, x.Nth, x.MaxCount, x.DoneCount, Now(), x.ID)
	return err
}

// DeleteSubscription removes one (its transactions stay, unlinked).
func (s *Store) DeleteSubscription(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = ?`, id)
	return err
}

// SubscriptionByExternalID finds an imported subscription.
func (s *Store) SubscriptionByExternalID(ctx context.Context, ext string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM subscriptions WHERE external_id = ? OR (',' || external_id || ',') LIKE ('%,' || ? || ',%') LIMIT 1`, ext, ext).Scan(&id)
	return id, notFound(err)
}

// ---- budgets ----

const budgetCols = `id, owner_id, name, amount, period, rollover, category_ids, tag_names, start_date, alert_pct, notified, color, icon, archived, sort_order, created_at`

func scanBudget(row interface{ Scan(...any) error }) (*Budget, error) {
	var b Budget
	var roll, arch int
	var cats, tags string
	if err := row.Scan(&b.ID, &b.OwnerID, &b.Name, &b.Amount, &b.Period, &roll, &cats, &tags, &b.StartDate, &b.AlertPct, &b.Notified, &b.Color, &b.Icon, &arch, &b.SortOrder, &b.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	b.Rollover, b.Archived = roll == 1, arch == 1
	b.CategoryIDs, b.TagNames = parseIDs(cats), parseList(tags)
	return &b, nil
}

// ListBudgets returns a user's budgets (ownerID 0 = all).
func (s *Store) ListBudgets(ctx context.Context, ownerID int64) ([]Budget, error) {
	q, args := `SELECT `+budgetCols+` FROM budgets`, []any{}
	if ownerID > 0 {
		q += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY archived, sort_order, lower(name)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Budget{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// GetBudget returns one budget.
func (s *Store) GetBudget(ctx context.Context, id int64) (*Budget, error) {
	return scanBudget(s.DB.QueryRowContext(ctx, `SELECT `+budgetCols+` FROM budgets WHERE id = ?`, id))
}

// CreateBudget inserts one.
func (s *Store) CreateBudget(ctx context.Context, b *Budget) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO budgets (owner_id, name, amount, period, rollover, category_ids, tag_names, start_date, alert_pct, color, icon, archived, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, b.OwnerID, b.Name, b.Amount, b.Period, boolInt(b.Rollover), jsonIDs(b.CategoryIDs), jsonList(b.TagNames),
		b.StartDate, b.AlertPct, b.Color, b.Icon, boolInt(b.Archived), b.SortOrder)
	if err != nil {
		return err
	}
	b.ID, _ = res.LastInsertId()
	return nil
}

// UpdateBudget saves one.
func (s *Store) UpdateBudget(ctx context.Context, b *Budget) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE budgets SET name = ?, amount = ?, period = ?, rollover = ?, category_ids = ?, tag_names = ?, start_date = ?, alert_pct = ?,
		notified = ?, color = ?, icon = ?, archived = ?, sort_order = ? WHERE id = ?`, b.Name, b.Amount, b.Period, boolInt(b.Rollover), jsonIDs(b.CategoryIDs), jsonList(b.TagNames),
		b.StartDate, b.AlertPct, b.Notified, b.Color, b.Icon, boolInt(b.Archived), b.SortOrder, b.ID)
	return err
}

// DeleteBudget removes one.
func (s *Store) DeleteBudget(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM budgets WHERE id = ?`, id)
	return err
}

// SpentHome sums expenses (minus refunds booked as income in the same
// categories) in home currency for [from, to] over the given accounts,
// categories (nil = any) and tags (nil = any).
func (s *Store) SpentHome(ctx context.Context, accountIDs, categoryIDs []int64, tags []string, from, to string) (int64, int, error) {
	in, args := inList(accountIDs)
	q := `SELECT COALESCE(SUM(t.amount_home), 0), COUNT(DISTINCT t.transaction_id) FROM tx_lines t WHERE t.kind = 'expense' AND t.account_id IN ` + in + ` AND t.date >= ? AND t.date <= ?`
	args = append(args, from, to)
	if len(categoryIDs) > 0 {
		cin, cargs := inList(categoryIDs)
		q += ` AND t.category_id IN ` + cin
		args = append(args, cargs...)
	}
	if len(tags) > 0 {
		ph := make([]string, len(tags))
		for i, tg := range tags {
			ph[i] = "?"
			args = append(args, tg)
		}
		q += ` AND EXISTS (SELECT 1 FROM transaction_tags x JOIN tags g ON g.id = x.tag_id WHERE x.transaction_id = t.transaction_id AND g.name COLLATE NOCASE IN (` + strings.Join(ph, ",") + `))`
	}
	var sum int64
	var n int
	err := s.DB.QueryRowContext(ctx, q, args...).Scan(&sum, &n)
	return sum, n, err
}

// ---- goals ----

const goalCols = `g.id, g.owner_id, g.name, g.target, g.currency, g.target_date, g.account_id, g.icon, g.color, g.notes, g.archived, g.completed_at, g.external_id, g.created_at,
	COALESCE(a.name, ''), COALESCE((SELECT SUM(amount) FROM goal_entries e WHERE e.goal_id = g.id), 0)`

func scanGoal(row interface{ Scan(...any) error }) (*Goal, error) {
	var g Goal
	var arch int
	if err := row.Scan(&g.ID, &g.OwnerID, &g.Name, &g.Target, &g.Currency, &g.TargetDate, &g.AccountID, &g.Icon, &g.Color, &g.Notes, &arch, &g.CompletedAt, &g.ExternalID, &g.CreatedAt,
		&g.AccountName, &g.Saved); err != nil {
		return nil, notFound(err)
	}
	g.Archived = arch == 1
	return &g, nil
}

// ListGoals returns a user's goals.
func (s *Store) ListGoals(ctx context.Context, ownerID int64) ([]Goal, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+goalCols+` FROM goals g LEFT JOIN accounts a ON a.id = g.account_id WHERE g.owner_id = ? ORDER BY g.archived, g.completed_at != '', g.target_date = '', g.target_date, g.id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Goal{}
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// GetGoal returns one goal with its entries.
func (s *Store) GetGoal(ctx context.Context, id int64) (*Goal, error) {
	g, err := scanGoal(s.DB.QueryRowContext(ctx, `SELECT `+goalCols+` FROM goals g LEFT JOIN accounts a ON a.id = g.account_id WHERE g.id = ?`, id))
	if err != nil {
		return nil, err
	}
	g.Entries, err = s.GoalEntries(ctx, id)
	return g, err
}

// GoalEntries lists deposits, newest first.
func (s *Store) GoalEntries(ctx context.Context, goalID int64) ([]GoalEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, goal_id, date, amount, note, created_at FROM goal_entries WHERE goal_id = ? ORDER BY date DESC, id DESC`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GoalEntry{}
	for rows.Next() {
		var e GoalEntry
		if err := rows.Scan(&e.ID, &e.GoalID, &e.Date, &e.Amount, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateGoal inserts one.
func (s *Store) CreateGoal(ctx context.Context, g *Goal) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO goals (owner_id, name, target, currency, target_date, account_id, icon, color, notes, archived, completed_at, external_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.OwnerID, g.Name, g.Target, g.Currency, g.TargetDate, nullI(g.AccountID), g.Icon, g.Color, g.Notes, boolInt(g.Archived), g.CompletedAt, g.ExternalID)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	return nil
}

// UpdateGoal saves one.
func (s *Store) UpdateGoal(ctx context.Context, g *Goal) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE goals SET name = ?, target = ?, currency = ?, target_date = ?, account_id = ?, icon = ?, color = ?, notes = ?, archived = ?, completed_at = ? WHERE id = ?`,
		g.Name, g.Target, g.Currency, g.TargetDate, nullI(g.AccountID), g.Icon, g.Color, g.Notes, boolInt(g.Archived), g.CompletedAt, g.ID)
	return err
}

// DeleteGoal removes one with its entries.
func (s *Store) DeleteGoal(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, id)
	return err
}

// AddGoalEntry records a deposit (negative = withdrawal).
func (s *Store) AddGoalEntry(ctx context.Context, e *GoalEntry) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO goal_entries (goal_id, date, amount, note) VALUES (?, ?, ?, ?)`, e.GoalID, e.Date, e.Amount, e.Note)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// DeleteGoalEntry removes a deposit.
func (s *Store) DeleteGoalEntry(ctx context.Context, goalID, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM goal_entries WHERE id = ? AND goal_id = ?`, id, goalID)
	return err
}

// ---- rules ----

func scanRule(row interface{ Scan(...any) error }) (*Rule, error) {
	var r Rule
	var conds, acts string
	var active, stop int
	if err := row.Scan(&r.ID, &r.OwnerID, &r.Name, &r.Match, &conds, &acts, &active, &stop, &r.SortOrder, &r.Hits, &r.LastHitAt, &r.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(conds), &r.Conditions)
	_ = json.Unmarshal([]byte(acts), &r.Actions)
	if r.Conditions == nil {
		r.Conditions = []Condition{}
	}
	r.Active, r.Stop = active == 1, stop == 1
	return &r, nil
}

const ruleCols = `id, owner_id, name, match, conditions, actions, active, stop, sort_order, hits, last_hit_at, created_at`

// ListRules returns a user's rules in run order (ownerID 0 = all).
func (s *Store) ListRules(ctx context.Context, ownerID int64) ([]Rule, error) {
	q, args := `SELECT `+ruleCols+` FROM rules`, []any{}
	if ownerID > 0 {
		q += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY sort_order, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRule returns one rule.
func (s *Store) GetRule(ctx context.Context, id int64) (*Rule, error) {
	return scanRule(s.DB.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM rules WHERE id = ?`, id))
}

// CreateRule inserts one.
func (s *Store) CreateRule(ctx context.Context, r *Rule) error {
	c, _ := json.Marshal(r.Conditions)
	a, _ := json.Marshal(r.Actions)
	if r.SortOrder == 0 {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM rules`).Scan(&r.SortOrder)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO rules (owner_id, name, match, conditions, actions, active, stop, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.OwnerID, r.Name, r.Match, string(c), string(a), boolInt(r.Active), boolInt(r.Stop), r.SortOrder)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// UpdateRule saves one.
func (s *Store) UpdateRule(ctx context.Context, r *Rule) error {
	c, _ := json.Marshal(r.Conditions)
	a, _ := json.Marshal(r.Actions)
	_, err := s.DB.ExecContext(ctx, `UPDATE rules SET name = ?, match = ?, conditions = ?, actions = ?, active = ?, stop = ?, sort_order = ? WHERE id = ?`,
		r.Name, r.Match, string(c), string(a), boolInt(r.Active), boolInt(r.Stop), r.SortOrder, r.ID)
	return err
}

// DeleteRule removes one.
func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id)
	return err
}

// RuleHit counts a match.
func (s *Store) RuleHit(ctx context.Context, id int64, n int) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE rules SET hits = hits + ?, last_hit_at = ? WHERE id = ?`, n, Now(), id)
}
