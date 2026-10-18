package store

import (
	"context"
	"sort"
	"strings"
)

// ListCategories returns all categories with usage counts and full names
// ("Food › Bakery"), parents first.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id, c.parent_id, c.name, c.kind, c.icon, c.color, c.archived, c.sort_order, c.external_id,
		(SELECT COUNT(1) FROM transactions t WHERE t.category_id = c.id) FROM categories c ORDER BY c.sort_order, lower(c.name)`)
	if err != nil {
		return nil, err
	}
	out := []Category{}
	for rows.Next() {
		var c Category
		var arch int
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Kind, &c.Icon, &c.Color, &arch, &c.SortOrder, &c.ExternalID, &c.TxCount); err != nil {
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

// GetCategory returns one category.
func (s *Store) GetCategory(ctx context.Context, id int64) (*Category, error) {
	var c Category
	var arch int
	err := s.DB.QueryRowContext(ctx, `SELECT id, parent_id, name, kind, icon, color, archived, sort_order, external_id FROM categories WHERE id = ?`, id).
		Scan(&c.ID, &c.ParentID, &c.Name, &c.Kind, &c.Icon, &c.Color, &arch, &c.SortOrder, &c.ExternalID)
	if err != nil {
		return nil, notFound(err)
	}
	c.Archived = arch == 1
	c.FullName = c.Name
	return &c, nil
}

// CreateCategory inserts a category.
func (s *Store) CreateCategory(ctx context.Context, c *Category) error {
	if c.Kind == "" {
		c.Kind = "expense"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO categories (parent_id, name, kind, icon, color, archived, sort_order, external_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullI(c.ParentID), strings.TrimSpace(c.Name), c.Kind, c.Icon, c.Color, boolInt(c.Archived), c.SortOrder, c.ExternalID)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

// UpdateCategory saves a category.
func (s *Store) UpdateCategory(ctx context.Context, c *Category) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE categories SET parent_id = ?, name = ?, kind = ?, icon = ?, color = ?, archived = ?, sort_order = ?, external_id = ? WHERE id = ?`,
		nullI(c.ParentID), strings.TrimSpace(c.Name), c.Kind, c.Icon, c.Color, boolInt(c.Archived), c.SortOrder, c.ExternalID, c.ID)
	return conflict(err)
}

// DeleteCategory removes a category; its transactions move to moveTo (or none).
func (s *Store) DeleteCategory(ctx context.Context, id int64, moveTo *int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmts := []struct {
		q    string
		args []any
	}{
		{`UPDATE transactions SET category_id = ? WHERE category_id = ?`, []any{nullI(moveTo), id}},
		{`UPDATE templates SET category_id = ? WHERE category_id = ?`, []any{nullI(moveTo), id}},
		{`UPDATE subscriptions SET category_id = ? WHERE category_id = ?`, []any{nullI(moveTo), id}},
		{`UPDATE categories SET parent_id = NULL WHERE parent_id = ?`, []any{id}},
		{`DELETE FROM categories WHERE id = ?`, []any{id}},
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// EnsureCategory returns the id of the category with that name, creating it.
func (s *Store) EnsureCategory(ctx context.Context, name, kind string) (int64, error) {
	name = strings.TrimSpace(name)
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM categories WHERE lower(name) = lower(?) ORDER BY parent_id IS NOT NULL, id LIMIT 1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	c := &Category{Name: name, Kind: kind}
	if err := s.CreateCategory(ctx, c); err != nil {
		return 0, err
	}
	return c.ID, nil
}

// CategoryChildren returns ids plus the ids of their children.
func (s *Store) CategoryChildren(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, parent_id FROM categories WHERE parent_id IS NOT NULL`)
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

// ListTags returns all tags with usage.
func (s *Store) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id, g.name, g.color, g.archived, g.external_id, g.created_at,
		(SELECT COUNT(1) FROM transaction_tags x WHERE x.tag_id = g.id),
		COALESCE((SELECT MAX(t.date) FROM transaction_tags x JOIN transactions t ON t.id = x.transaction_id WHERE x.tag_id = g.id), '')
		FROM tags g ORDER BY g.archived, lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		var arch int
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &arch, &t.ExternalID, &t.CreatedAt, &t.TxCount, &t.LastUsed); err != nil {
			return nil, err
		}
		t.Archived = arch == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTag finds a tag by id.
func (s *Store) GetTag(ctx context.Context, id int64) (*Tag, error) {
	var t Tag
	var arch int
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, color, archived, external_id, created_at FROM tags WHERE id = ?`, id).Scan(&t.ID, &t.Name, &t.Color, &arch, &t.ExternalID, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	t.Archived = arch == 1
	return &t, nil
}

// GetTagByName finds a tag by name (case-insensitive).
func (s *Store) GetTagByName(ctx context.Context, name string) (*Tag, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, strings.TrimSpace(name)).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.GetTag(ctx, id)
}

// EnsureTag returns the id of a tag, creating it.
func (s *Store) EnsureTag(ctx context.Context, name string) (int64, error) {
	name = strings.TrimSpace(name)
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&id); err == nil {
		return id, nil
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?)`, name)
	if err != nil {
		return 0, conflict(err)
	}
	return res.LastInsertId()
}

// UpdateTag renames / recolours a tag.
func (s *Store) UpdateTag(ctx context.Context, t *Tag) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tags SET name = ?, color = ?, archived = ?, external_id = ? WHERE id = ?`, strings.TrimSpace(t.Name), t.Color, boolInt(t.Archived), t.ExternalID, t.ID)
	return conflict(err)
}

// DeleteTag removes a tag from everything.
func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	return err
}

// MergeTags moves every use of from onto into and deletes from.
func (s *Store) MergeTags(ctx context.Context, from, into int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO transaction_tags (transaction_id, tag_id) SELECT transaction_id, ? FROM transaction_tags WHERE tag_id = ?`, into, from); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, from); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
