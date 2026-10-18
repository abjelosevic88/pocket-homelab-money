package store

import (
	"context"
	"encoding/json"
)

const webhookCols = `id, owner_id, name, url, secret, events, format, active, last_status, last_error, last_at, created_at`

func scanWebhook(row interface{ Scan(...any) error }) (*Webhook, error) {
	var h Webhook
	var events string
	var active int
	if err := row.Scan(&h.ID, &h.OwnerID, &h.Name, &h.URL, &h.Secret, &events, &h.Format, &active, &h.LastStatus, &h.LastError, &h.LastAt, &h.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(events), &h.Events)
	if h.Events == nil {
		h.Events = []string{}
	}
	h.Active = active == 1
	return &h, nil
}

// ListWebhooks returns a user's webhooks (ownerID 0 = everyone's).
func (s *Store) ListWebhooks(ctx context.Context, ownerID int64) ([]Webhook, error) {
	q, args := `SELECT `+webhookCols+` FROM webhooks`, []any{}
	if ownerID > 0 {
		q += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		h, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// GetWebhook returns one webhook.
func (s *Store) GetWebhook(ctx context.Context, id int64) (*Webhook, error) {
	return scanWebhook(s.DB.QueryRowContext(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id = ?`, id))
}

// CreateWebhook inserts one.
func (s *Store) CreateWebhook(ctx context.Context, h *Webhook) error {
	ev, _ := json.Marshal(h.Events)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO webhooks (owner_id, name, url, secret, events, format, active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.OwnerID, h.Name, h.URL, h.Secret, string(ev), h.Format, boolInt(h.Active))
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	return nil
}

// UpdateWebhook saves one.
func (s *Store) UpdateWebhook(ctx context.Context, h *Webhook) error {
	ev, _ := json.Marshal(h.Events)
	_, err := s.DB.ExecContext(ctx, `UPDATE webhooks SET name = ?, url = ?, secret = ?, events = ?, format = ?, active = ? WHERE id = ?`,
		h.Name, h.URL, h.Secret, string(ev), h.Format, boolInt(h.Active), h.ID)
	return err
}

// DeleteWebhook removes one with its log.
func (s *Store) DeleteWebhook(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	return err
}

// LogDelivery records an attempt and keeps the last 200 per webhook.
func (s *Store) LogDelivery(ctx context.Context, d *Delivery) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO webhook_deliveries (webhook_id, event, subject, status, error, attempts, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.WebhookID, d.Event, d.Subject, d.Status, d.Error, d.Attempts, d.DurationMS)
	_, _ = s.DB.ExecContext(ctx, `UPDATE webhooks SET last_status = ?, last_error = ?, last_at = ? WHERE id = ?`, d.Status, d.Error, Now(), d.WebhookID)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE webhook_id = ? AND id NOT IN (SELECT id FROM webhook_deliveries WHERE webhook_id = ? ORDER BY id DESC LIMIT 200)`, d.WebhookID, d.WebhookID)
}

// ListDeliveries returns the newest attempts of a webhook.
func (s *Store) ListDeliveries(ctx context.Context, webhookID int64, limit int) ([]Delivery, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, webhook_id, event, subject, status, error, attempts, duration_ms, created_at FROM webhook_deliveries WHERE webhook_id = ? ORDER BY id DESC LIMIT ?`, webhookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Subject, &d.Status, &d.Error, &d.Attempts, &d.DurationMS, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
