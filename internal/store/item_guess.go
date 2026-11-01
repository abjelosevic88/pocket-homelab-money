package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
)

// AIProvider is the receipt provider of items the AI guessed for a
// hand-typed expense (no shop, no tax office).
const AIProvider = "ai"

// GuessSources are the transaction sources that count as typed by the
// person: the web app, quick add, templates, the phone app and Firefly/Pico
// (where the older ones were typed). Receipts, subscriptions, imports and
// other Pocket apps' bookings are left alone.
var GuessSources = []string{"", "manual", "web", "quick", "template", "api", "firefly_api", "firefly"}

// ItemGuess is what the AI was asked about one transaction.
type ItemGuess struct {
	TransactionID int64  `json:"transaction_id"`
	UserID        int64  `json:"user_id"`
	Amount        int64  `json:"amount"`
	Description   string `json:"description"`
	Status        string `json:"status"` // items | none | error
	Note          string `json:"note"`
	CreatedAt     string `json:"created_at"`
}

// dropStaleGuess removes guessed items once the transaction no longer has
// the amount and description they were guessed for (or is no expense).
func dropStaleGuess(ctx context.Context, tx *sql.Tx, t *Transaction) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM receipts WHERE transaction_id = ? AND provider = '`+AIProvider+`'
		AND (? != 'expense' OR NOT EXISTS (SELECT 1 FROM item_guesses g WHERE g.transaction_id = ? AND g.amount = ? AND g.description = ?))`,
		t.ID, t.Kind, t.ID, t.Amount, t.Description)
	return err
}

// ItemGuessCandidates are the person's typed expenses between from and to
// that have no scanned receipt and were not asked about with their current
// amount and description (asking failed: asked again).
func (s *Store) ItemGuessCandidates(ctx context.Context, userID int64, from, to string, limit int) ([]int64, error) {
	src, args := inStrings(GuessSources)
	q := `SELECT t.id FROM transactions t WHERE t.user_id = ? AND t.kind = 'expense' AND t.date BETWEEN ? AND ? AND t.source IN ` + src + `
		AND trim(t.description) != '' AND t.amount > 0
		AND NOT EXISTS (SELECT 1 FROM receipts r WHERE r.transaction_id = t.id AND r.provider != '` + AIProvider + `')
		AND NOT EXISTS (SELECT 1 FROM item_guesses g WHERE g.transaction_id = t.id AND g.amount = t.amount AND g.description = t.description AND g.status != 'error')
		ORDER BY t.date, t.time, t.id LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, q, append(append([]any{userID, from, to}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func inStrings(xs []string) (string, []any) {
	ph := make([]string, len(xs))
	args := make([]any, len(xs))
	for i, x := range xs {
		ph[i], args[i] = "?", x
	}
	return "(" + strings.Join(ph, ",") + ")", args
}

// SaveItemGuess records the answer for a transaction (one row per transaction).
func (s *Store) SaveItemGuess(ctx context.Context, g ItemGuess) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO item_guesses (transaction_id, user_id, amount, description, status, note, created_at) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (transaction_id) DO UPDATE SET amount = excluded.amount, description = excluded.description, status = excluded.status,
		note = excluded.note, created_at = excluded.created_at`, g.TransactionID, g.UserID, g.Amount, g.Description, g.Status, g.Note, Now())
	return err
}

// Asked says whether the AI already answered for the transaction as it is now.
func (g *ItemGuess) Asked(t *Transaction) bool {
	return g != nil && g.Status != "error" && g.Amount == t.Amount && g.Description == t.Description
}

// GetItemGuess returns what the AI said about a transaction last.
func (s *Store) GetItemGuess(ctx context.Context, txID int64) (*ItemGuess, error) {
	var g ItemGuess
	err := s.DB.QueryRowContext(ctx, `SELECT transaction_id, user_id, amount, description, status, note, created_at FROM item_guesses WHERE transaction_id = ?`, txID).
		Scan(&g.TransactionID, &g.UserID, &g.Amount, &g.Description, &g.Status, &g.Note, &g.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &g, nil
}

// SaveGuessedItems replaces the transaction's guessed items with these. The
// receipt is in the currency the person typed (the foreign one when set).
func (s *Store) SaveGuessedItems(ctx context.Context, t *Transaction, currency string, total int64, items []fiscal.Item) (*Receipt, error) {
	if err := s.DeleteGuessedItems(ctx, t.ID); err != nil {
		return nil, err
	}
	txID := t.ID
	r := &Receipt{UserID: t.UserID, Provider: AIProvider, Key: "tx:" + strconv.FormatInt(t.ID, 10), Date: t.Date, Time: t.Time, Currency: currency,
		Total: total, Kind: "sale", InvoiceType: "normal", Complete: true, HasItems: len(items) > 0, TransactionID: &txID}
	if err := s.SaveReceipt(ctx, r, items); err != nil {
		return nil, err
	}
	return s.GetReceipt(ctx, r.ID)
}

// DeleteGuessedItems removes the transaction's guessed items (a scanned
// receipt for it is not touched).
func (s *Store) DeleteGuessedItems(ctx context.Context, txID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM receipts WHERE transaction_id = ? AND provider = '`+AIProvider+`'`, txID)
	return err
}

// KnownProduct is a product the person bought before, for the AI to reuse.
type KnownProduct struct {
	Name     string
	Bought   int
	Price    int64 // the last unit price, scanned receipts first
	Currency string
}

// KnownProducts lists the person's most bought products with their last price.
func (s *Store) KnownProducts(ctx context.Context, userID int64, limit int) ([]KnownProduct, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.name, COUNT(i.id),
		COALESCE((SELECT i2.unit_price FROM receipt_items i2 JOIN receipts r2 ON r2.id = i2.receipt_id WHERE i2.product_id = p.id AND r2.kind = 'sale'
			ORDER BY r2.provider = '`+AIProvider+`', r2.date DESC, r2.id DESC LIMIT 1), 0),
		COALESCE((SELECT r2.currency FROM receipt_items i2 JOIN receipts r2 ON r2.id = i2.receipt_id WHERE i2.product_id = p.id AND r2.kind = 'sale'
			ORDER BY r2.provider = '`+AIProvider+`', r2.date DESC, r2.id DESC LIMIT 1), '')
		FROM products p JOIN receipt_items i ON i.product_id = p.id WHERE p.user_id = ?
		GROUP BY p.id ORDER BY COUNT(i.id) DESC, p.name LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnownProduct
	for rows.Next() {
		var k KnownProduct
		if err := rows.Scan(&k.Name, &k.Bought, &k.Price, &k.Currency); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
