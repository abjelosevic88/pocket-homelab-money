package store

import (
	"context"
	"sort"
	"strings"
)

// Categories and tags belong to one person each: every query here is
// scoped by userID, and another person's id behaves like a missing one.

// ListCategories returns a person's categories with usage counts and full
// names ("Food › Bakery"), parents first.
func (s *Store) ListCategories(ctx context.Context, userID int64) ([]Category, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id, c.user_id, c.parent_id, c.name, c.kind, c.icon, c.color, c.archived, c.sort_order, c.external_id,
		(SELECT COUNT(1) FROM transactions t WHERE t.category_id = c.id) FROM categories c WHERE c.user_id = ? ORDER BY c.sort_order, lower(c.name)`, userID)
	if err != nil {
		return nil, err
	}
	out := []Category{}
	for rows.Next() {
		var c Category
		var arch int
		if err := rows.Scan(&c.ID, &c.UserID, &c.ParentID, &c.Name, &c.Kind, &c.Icon, &c.Color, &arch, &c.SortOrder, &c.ExternalID, &c.TxCount); err != nil {
			rows.Close()
			return nil, err
		}
		c.Archived = arch == 1
		out = append(out, c)
	}
	rows.Close()
	byID := map[int64]*Category{}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	for i := range out {
		out[i].FullName = out[i].Name
		if out[i].ParentID != nil {
			if p := byID[*out[i].ParentID]; p != nil {
				out[i].FullName = p.Name + " › " + out[i].Name
			}
		}
	}
	// parents, each followed by its children
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := sortKey(&out[i], byID), sortKey(&out[j], byID)
		return ki < kj
	})
	return out, rows.Err()
}

func sortKey(c *Category, byID map[int64]*Category) string {
	if c.ParentID != nil {
		if p := byID[*c.ParentID]; p != nil {
			return pad(p.SortOrder) + strings.ToLower(p.Name) + "\x01" + pad(c.SortOrder) + strings.ToLower(c.Name)
		}
	}
	return pad(c.SortOrder) + strings.ToLower(c.Name) + "\x00"
}

func pad(n int) string {
	s := strings.Repeat("0", 6)
	v := []byte(s)
	for i := 5; i >= 0 && n > 0; i-- {
		v[i] = byte('0' + n%10)
		n /= 10
	}
	return string(v)
}

// GetCategory returns one of a person's categories.
func (s *Store) GetCategory(ctx context.Context, userID, id int64) (*Category, error) {
	var c Category
	var arch int
	err := s.DB.QueryRowContext(ctx, `SELECT id, user_id, parent_id, name, kind, icon, color, archived, sort_order, external_id FROM categories WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&c.ID, &c.UserID, &c.ParentID, &c.Name, &c.Kind, &c.Icon, &c.Color, &arch, &c.SortOrder, &c.ExternalID)
	if err != nil {
		return nil, notFound(err)
	}
	c.Archived = arch == 1
	c.FullName = c.Name
	return &c, nil
}

// CreateCategory inserts a category for c.UserID.
func (s *Store) CreateCategory(ctx context.Context, c *Category) error {
	if c.Kind == "" {
		c.Kind = "expense"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO categories (user_id, parent_id, name, kind, icon, color, archived, sort_order, external_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.UserID, nullI(c.ParentID), strings.TrimSpace(c.Name), c.Kind, c.Icon, c.Color, boolInt(c.Archived), c.SortOrder, c.ExternalID)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

// UpdateCategory saves a category (only its owner's row is touched).
func (s *Store) UpdateCategory(ctx context.Context, c *Category) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE categories SET parent_id = ?, name = ?, kind = ?, icon = ?, color = ?, archived = ?, sort_order = ?, external_id = ? WHERE id = ? AND user_id = ?`,
		nullI(c.ParentID), strings.TrimSpace(c.Name), c.Kind, c.Icon, c.Color, boolInt(c.Archived), c.SortOrder, c.ExternalID, c.ID, c.UserID)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCategory removes one of a person's categories; what used it moves
// to moveTo (another of their categories) or to none.
func (s *Store) DeleteCategory(ctx context.Context, userID, id int64, moveTo *int64) error {
	if _, err := s.GetCategory(ctx, userID, id); err != nil {
		return err
	}
	if moveTo != nil {
		if _, err := s.GetCategory(ctx, userID, *moveTo); err != nil {
			return err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmts := []struct {
		q    string
		args []any
	}{
		{`UPDATE transactions SET category_id = ? WHERE category_id = ?`, []any{nullI(moveTo), id}},
		{`UPDATE transaction_splits SET category_id = ? WHERE category_id = ?`, []any{nullI(moveTo), id}},
		{`UPDATE templates SET category_id = ? WHERE category_id = ? AND owner_id = ?`, []any{nullI(moveTo), id, userID}},
		{`UPDATE subscriptions SET category_id = ? WHERE category_id = ? AND owner_id = ?`, []any{nullI(moveTo), id, userID}},
		{`UPDATE categories SET parent_id = NULL WHERE parent_id = ? AND user_id = ?`, []any{id, userID}},
		{`DELETE FROM categories WHERE id = ? AND user_id = ?`, []any{id, userID}},
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// EnsureCategory returns the id of a person's category with that name, creating it.
func (s *Store) EnsureCategory(ctx context.Context, userID int64, name, kind string) (int64, error) {
	name = strings.TrimSpace(name)
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM categories WHERE user_id = ? AND lower(name) = lower(?) ORDER BY parent_id IS NOT NULL, id LIMIT 1`, userID, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	c := &Category{UserID: userID, Name: name, Kind: kind}
	if err := s.CreateCategory(ctx, c); err != nil {
		return 0, err
	}
	return c.ID, nil
}

// OwnsCategories reports whether every id is one of the person's categories.
func (s *Store) OwnsCategories(ctx context.Context, userID int64, ids []int64) bool {
	for _, id := range ids {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM categories WHERE id = ? AND user_id = ?`, id, userID).Scan(&n); err != nil || n == 0 {
			return false
		}
	}
	return true
}

// CategoryChildren returns ids plus the ids of their children (the person's own).
func (s *Store) CategoryChildren(ctx context.Context, userID int64, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, parent_id FROM categories WHERE parent_id IS NOT NULL AND user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	var extra []int64
	for rows.Next() {
		var id, p int64
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, err
		}
		if seen[p] && !seen[id] {
			extra = append(extra, id)
		}
	}
	rows.Close()
	out := append([]int64{}, ids...)
	return append(out, extra...), nil
}

// ---- tags ----

// ListTags returns a person's tags with usage.
func (s *Store) ListTags(ctx context.Context, userID int64) ([]Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id, g.user_id, g.name, g.color, g.archived, g.external_id, g.created_at,
		(SELECT COUNT(1) FROM transaction_tags x WHERE x.tag_id = g.id),
		COALESCE((SELECT MAX(t.date) FROM transaction_tags x JOIN transactions t ON t.id = x.transaction_id WHERE x.tag_id = g.id), '')
		FROM tags g WHERE g.user_id = ? ORDER BY g.archived, lower(g.name)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		var arch int
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Color, &arch, &t.ExternalID, &t.CreatedAt, &t.TxCount, &t.LastUsed); err != nil {
			return nil, err
		}
		t.Archived = arch == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTag finds one of a person's tags by id.
func (s *Store) GetTag(ctx context.Context, userID, id int64) (*Tag, error) {
	var t Tag
	var arch int
	err := s.DB.QueryRowContext(ctx, `SELECT id, user_id, name, color, archived, external_id, created_at FROM tags WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&t.ID, &t.UserID, &t.Name, &t.Color, &arch, &t.ExternalID, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	t.Archived = arch == 1
	return &t, nil
}

// GetTagByName finds one of a person's tags by name (case-insensitive).
func (s *Store) GetTagByName(ctx context.Context, userID int64, name string) (*Tag, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = ? COLLATE NOCASE`, userID, strings.TrimSpace(name)).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.GetTag(ctx, userID, id)
}

// EnsureTag returns the id of a person's tag, creating it.
func (s *Store) EnsureTag(ctx context.Context, userID int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = ? COLLATE NOCASE`, userID, name).Scan(&id); err == nil {
		return id, nil
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO tags (user_id, name) VALUES (?, ?)`, userID, name)
	if err != nil {
		return 0, conflict(err)
	}
	return res.LastInsertId()
}

// UpdateTag renames / recolours a person's tag.
func (s *Store) UpdateTag(ctx context.Context, t *Tag) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE tags SET name = ?, color = ?, archived = ?, external_id = ? WHERE id = ? AND user_id = ?`,
		strings.TrimSpace(t.Name), t.Color, boolInt(t.Archived), t.ExternalID, t.ID, t.UserID)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTag removes a person's tag from everything.
func (s *Store) DeleteTag(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MergeTags moves every use of from onto into (both the person's) and deletes from.
func (s *Store) MergeTags(ctx context.Context, userID, from, into int64) error {
	if _, err := s.GetTag(ctx, userID, from); err != nil {
		return err
	}
	if _, err := s.GetTag(ctx, userID, into); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO transaction_tags (transaction_id, tag_id) SELECT transaction_id, ? FROM transaction_tags WHERE tag_id = ?`, into, from); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, from, userID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
