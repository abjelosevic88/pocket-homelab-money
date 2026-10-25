package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
)

// ReceiptScan is one row of the scan log.
type ReceiptScan struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Provider   string `json:"provider"`
	Country    string `json:"country"`
	URL        string `json:"url"`
	ReceiptKey string `json:"receipt_key"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Attempts   int    `json:"attempts"`
	NextTryAt  string `json:"next_try_at"`
	Raw        string `json:"-"`
	ReceiptID  *int64 `json:"receipt_id"`
	Source     string `json:"source"`
	AccountID  *int64 `json:"account_id"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`

	HasRaw        bool   `json:"has_raw"`
	TransactionID *int64 `json:"transaction_id"`
	MerchantName  string `json:"merchant_name"`
	Total         int64  `json:"total"`
	Currency      string `json:"currency"`
}

// Merchant is a shop receipts came from.
type Merchant struct {
	ID             int64    `json:"id"`
	UserID         int64    `json:"user_id"`
	Country        string   `json:"country"`
	TaxID          string   `json:"tax_id"`
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name"`
	Address        string   `json:"address"`
	City           string   `json:"city"`
	CategoryID     *int64   `json:"category_id"`
	AccountID      *int64   `json:"account_id"`
	CreatedAt      string   `json:"created_at"`
	CategorySource string   `json:"category_source"` // you | rule | ''
	Description    string   `json:"description"`
	Lat            *float64 `json:"lat"`
	Lng            *float64 `json:"lng"`
	Geocoded       bool     `json:"geocoded"`

	Receipts     int    `json:"receipts"`
	Spent        int64  `json:"spent"` // in Currency, the one most receipts are in
	Currency     string `json:"currency"`
	LastDate     string `json:"last_date"`
	CategoryName string `json:"category_name"`
	CategoryIcon string `json:"category_icon"`
}

// Receipt is a stored fiscal receipt.
type Receipt struct {
	ID             int64             `json:"id"`
	UserID         int64             `json:"user_id"`
	Provider       string            `json:"provider"`
	Country        string            `json:"country"`
	Key            string            `json:"receipt_key"`
	InvoiceNumber  string            `json:"invoice_number"`
	MerchantID     *int64            `json:"merchant_id"`
	Location       string            `json:"location"`
	IssuedAt       string            `json:"issued_at"`
	Date           string            `json:"date"`
	Time           string            `json:"time"`
	Currency       string            `json:"currency"`
	Total          int64             `json:"total"`
	TaxTotal       int64             `json:"tax_total"`
	Method         string            `json:"method"`
	Payments       []fiscal.Payment  `json:"payments"`
	Kind           string            `json:"kind"`
	InvoiceType    string            `json:"invoice_type"`
	Referent       string            `json:"referent"`
	Cashier        string            `json:"cashier"`
	Device         string            `json:"device"`
	Journal        string            `json:"journal,omitempty"`
	URL            string            `json:"url"`
	Complete       bool              `json:"complete"`
	HasItems       bool              `json:"has_items"`
	TransactionID  *int64            `json:"transaction_id"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	CategoryReason string            `json:"category_reason"`
	Items          []ReceiptItem     `json:"items"`
	Merchant       *Merchant         `json:"merchant,omitempty"`
	ItemCount      int               `json:"item_count"`
	Guess          map[string]any    `json:"guess,omitempty"` // what the receipt says today (the server fills it in)
	Extra          map[string]string `json:"extra,omitempty"`
}

// ReceiptItem is one line of a receipt.
type ReceiptItem struct {
	ID        int64   `json:"id"`
	ReceiptID int64   `json:"receipt_id"`
	Line      int     `json:"line"`
	Name      string  `json:"name"`
	Norm      string  `json:"-"`
	GTIN      string  `json:"gtin"`
	Quantity  float64 `json:"quantity"`
	Unit      string  `json:"unit"`
	UnitPrice int64   `json:"unit_price"`
	Total     int64   `json:"total"`
	TaxLabel  string  `json:"tax_label"`
	ProductID *int64  `json:"product_id"`

	ProductName    string `json:"product_name"`
	GroupID        *int64 `json:"group_id"`
	GroupName      string `json:"group_name"`
	GroupIcon      string `json:"group_icon"`
	CategoryID     *int64 `json:"category_id"`     // the product's own, else its group's
	CategorySource string `json:"category_source"` // you | ai | group | ''
	CategoryName   string `json:"category_name"`
	CategoryIcon   string `json:"category_icon"`
}

// ProductGroup collects products ("Milk").
type ProductGroup struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Keywords   string `json:"keywords"`
	CategoryID *int64 `json:"category_id"`
	SortOrder  int    `json:"sort_order"`
	CreatedAt  string `json:"created_at"`
}

// Product is the same article across receipts.
type Product struct {
	ID             int64  `json:"id"`
	UserID         int64  `json:"user_id"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	Unit           string `json:"unit"`
	GroupID        *int64 `json:"group_id"`
	GroupLocked    bool   `json:"group_locked"`
	CreatedAt      string `json:"created_at"`
	CategoryID     *int64 `json:"category_id"`
	CategorySource string `json:"category_source"`
}

// ReceiptSettings are one person's receipt preferences.
type ReceiptSettings struct {
	CardAccountID *int64     `json:"card_account_id"`
	CashAccountID *int64     `json:"cash_account_id"`
	SplitItems    bool       `json:"split_items"`
	AttachManual  bool       `json:"attach_manual"`
	AIItems       bool       `json:"ai_items"`   // sort new products into categories with the AI
	OwnWords      bool       `json:"own_words"`  // describe receipts the way the person does
	Geocode       bool       `json:"geocode"`    // put shops on the map
	ShopRules     []ShopRule `json:"shop_rules"` // nil = the defaults
}

// ShopRule is "shops with one of these words in their name go to this category".
type ShopRule struct {
	Keywords   string `json:"keywords"`
	CategoryID int64  `json:"category_id"`
}

// ---- scan log ----

const scanCols = `s.id, s.user_id, s.provider, s.country, s.url, s.receipt_key, s.status, s.message, s.attempts, s.next_try_at, s.raw, s.receipt_id, s.source, s.account_id, s.created_at, s.updated_at`

func scanScan(row interface{ Scan(...any) error }, extra ...any) (*ReceiptScan, error) {
	var x ReceiptScan
	dst := []any{&x.ID, &x.UserID, &x.Provider, &x.Country, &x.URL, &x.ReceiptKey, &x.Status, &x.Message, &x.Attempts, &x.NextTryAt, &x.Raw, &x.ReceiptID, &x.Source, &x.AccountID, &x.CreatedAt, &x.UpdatedAt}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return nil, notFound(err)
	}
	x.HasRaw = x.Raw != ""
	return &x, nil
}

// CreateScan logs a scan.
func (s *Store) CreateScan(ctx context.Context, x *ReceiptScan) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO receipt_scans (user_id, provider, country, url, receipt_key, status, message, attempts, next_try_at, raw, receipt_id, source, account_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, x.UserID, x.Provider, x.Country, x.URL, x.ReceiptKey, x.Status, x.Message, x.Attempts, x.NextTryAt, x.Raw, nullI(x.ReceiptID), x.Source, nullI(x.AccountID))
	if err != nil {
		return err
	}
	x.ID, _ = res.LastInsertId()
	_ = s.DB.QueryRowContext(ctx, `SELECT created_at, updated_at FROM receipt_scans WHERE id = ?`, x.ID).Scan(&x.CreatedAt, &x.UpdatedAt)
	return nil
}

// UpdateScan saves what became of a scan.
func (s *Store) UpdateScan(ctx context.Context, x *ReceiptScan) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE receipt_scans SET provider=?, country=?, receipt_key=?, status=?, message=?, attempts=?, next_try_at=?, raw=?, receipt_id=?, updated_at=? WHERE id=?`,
		x.Provider, x.Country, x.ReceiptKey, x.Status, x.Message, x.Attempts, x.NextTryAt, x.Raw, nullI(x.ReceiptID), Now(), x.ID)
	return err
}

// GetScan returns one of the person's scans.
func (s *Store) GetScan(ctx context.Context, userID, id int64) (*ReceiptScan, error) {
	return scanScan(s.DB.QueryRowContext(ctx, `SELECT `+scanCols+` FROM receipt_scans s WHERE s.id = ? AND s.user_id = ?`, id, userID))
}

// ListScans returns the person's scan log, newest first.
func (s *Store) ListScans(ctx context.Context, userID int64, status string, limit, offset int) ([]ReceiptScan, int, error) {
	where := ` WHERE s.user_id = ?`
	args := []any{userID}
	if status != "" {
		where += ` AND s.status = ?`
		args = append(args, status)
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM receipt_scans s`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+scanCols+`, r.transaction_id, COALESCE(NULLIF(m.display_name,''), m.name, ''), COALESCE(r.total, 0), COALESCE(r.currency, '')
		FROM receipt_scans s LEFT JOIN receipts r ON r.id = s.receipt_id LEFT JOIN merchants m ON m.id = r.merchant_id`+where+` ORDER BY s.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ReceiptScan{}
	for rows.Next() {
		var x *ReceiptScan
		var txID sql.NullInt64
		var name, cur string
		var tot int64
		if x, err = scanScan(rows, &txID, &name, &tot, &cur); err != nil {
			return nil, 0, err
		}
		if txID.Valid {
			x.TransactionID = &txID.Int64
		}
		x.MerchantName, x.Total, x.Currency = name, tot, cur
		out = append(out, *x)
	}
	return out, total, rows.Err()
}

// DueScans are scans waiting for the tax office whose retry time has come.
func (s *Store) DueScans(ctx context.Context, now string, limit int) ([]ReceiptScan, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+scanCols+` FROM receipt_scans s WHERE s.status IN ('pending','partial') AND s.next_try_at != '' AND s.next_try_at <= ? ORDER BY s.next_try_at LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceiptScan
	for rows.Next() {
		x, err := scanScan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// ---- merchants ----

const merchantCols = `m.id, m.user_id, m.country, m.tax_id, m.name, m.display_name, m.address, m.city, m.category_id, m.account_id, m.created_at,
	m.category_source, m.description, m.lat, m.lng, m.geocoded`

func scanMerchant(row interface{ Scan(...any) error }, extra ...any) (*Merchant, error) {
	var m Merchant
	dst := []any{&m.ID, &m.UserID, &m.Country, &m.TaxID, &m.Name, &m.DisplayName, &m.Address, &m.City, &m.CategoryID, &m.AccountID, &m.CreatedAt,
		&m.CategorySource, &m.Description, &m.Lat, &m.Lng, &m.Geocoded}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return nil, notFound(err)
	}
	return &m, nil
}

// UpsertMerchant records a shop (or refreshes what the tax office says about
// it) and returns it. The person's display name, category and account stay.
func (s *Store) UpsertMerchant(ctx context.Context, userID int64, country string, in fiscal.Merchant) (*Merchant, error) {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO merchants (user_id, country, tax_id, name, display_name, address, city) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (user_id, country, tax_id) DO UPDATE SET name = excluded.name,
			address = CASE WHEN excluded.address != '' THEN excluded.address ELSE merchants.address END,
			city = CASE WHEN excluded.city != '' THEN excluded.city ELSE merchants.city END`,
		userID, country, in.TaxID, in.Name, fiscal.MerchantName(in.Name), in.Address, in.City)
	if err != nil {
		return nil, err
	}
	return scanMerchant(s.DB.QueryRowContext(ctx, `SELECT `+merchantCols+` FROM merchants m WHERE m.user_id = ? AND m.country = ? AND m.tax_id = ?`, userID, country, in.TaxID))
}

// GetMerchant returns one of the person's shops.
func (s *Store) GetMerchant(ctx context.Context, userID, id int64) (*Merchant, error) {
	return scanMerchant(s.DB.QueryRowContext(ctx, `SELECT `+merchantCols+` FROM merchants m WHERE m.id = ? AND m.user_id = ?`, id, userID))
}

// MerchantForDevice is the shop whose earlier receipt the same fiscal device
// signed: it names an offline scan before the tax office answers.
func (s *Store) MerchantForDevice(ctx context.Context, userID int64, provider, device string) (*Merchant, error) {
	if device == "" {
		return nil, ErrNotFound
	}
	return scanMerchant(s.DB.QueryRowContext(ctx, `SELECT `+merchantCols+` FROM receipts r JOIN merchants m ON m.id = r.merchant_id
		WHERE r.user_id = ? AND r.provider = ? AND r.device = ? AND r.complete = 1 ORDER BY r.id DESC LIMIT 1`, userID, provider, device))
}

// ListMerchants returns the person's shops with what was spent there.
func (s *Store) ListMerchants(ctx context.Context, userID int64) ([]Merchant, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+merchantCols+`, COALESCE(c.name, ''), COALESCE(c.icon, ''),
		(SELECT COUNT(1) FROM receipts r WHERE r.merchant_id = m.id),
		COALESCE((SELECT r.currency FROM receipts r WHERE r.merchant_id = m.id GROUP BY r.currency ORDER BY COUNT(1) DESC LIMIT 1), ''),
		COALESCE((SELECT MAX(r.date) FROM receipts r WHERE r.merchant_id = m.id), '')
		FROM merchants m LEFT JOIN categories c ON c.id = m.category_id WHERE m.user_id = ? ORDER BY m.display_name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	out := []Merchant{}
	for rows.Next() {
		var cn, ci, cur, last string
		var n int
		m, err := scanMerchant(rows, &cn, &ci, &n, &cur, &last)
		if err != nil {
			rows.Close()
			return nil, err
		}
		m.CategoryName, m.CategoryIcon, m.Receipts, m.Currency, m.LastDate = cn, ci, n, cur, last
		out = append(out, *m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN kind = 'refund' THEN -total ELSE total END), 0) FROM receipts WHERE merchant_id = ? AND currency = ?`, out[i].ID, out[i].Currency).Scan(&out[i].Spent)
	}
	return out, nil
}

// UpdateMerchant saves the person's choices for a shop.
func (s *Store) UpdateMerchant(ctx context.Context, m *Merchant) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE merchants SET display_name = ?, category_id = ?, account_id = ?, category_source = ?, description = ? WHERE id = ? AND user_id = ?`,
		m.DisplayName, nullI(m.CategoryID), nullI(m.AccountID), m.CategorySource, m.Description, m.ID, m.UserID)
	return err
}

// SetMerchantCategory records how a shop is categorised (source you | rule).
func (s *Store) SetMerchantCategory(ctx context.Context, id int64, cat *int64, source string) error {
	if cat == nil {
		source = ""
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE merchants SET category_id = ?, category_source = ? WHERE id = ?`, nullI(cat), source, id)
	return err
}

// SetMerchantDescription remembers the person's own words for a shop.
func (s *Store) SetMerchantDescription(ctx context.Context, id int64, desc string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE merchants SET description = ? WHERE id = ?`, desc, id)
	return err
}

// SetMerchantPlace stores where a shop is (nil: looked up, not found).
func (s *Store) SetMerchantPlace(ctx context.Context, id int64, lat, lng *float64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE merchants SET lat = ?, lng = ?, geocoded = 1 WHERE id = ?`, nullF(lat), nullF(lng), id)
	return err
}

// ---- receipts ----

const receiptCols = `r.id, r.user_id, r.provider, r.country, r.receipt_key, r.invoice_number, r.merchant_id, r.location, r.issued_at, r.date, r.time, r.currency,
	r.total, r.tax_total, r.method, r.payments, r.kind, r.invoice_type, r.referent, r.cashier, r.device, r.journal, r.url, r.complete, r.has_items, r.transaction_id,
	r.created_at, r.updated_at, (SELECT COUNT(1) FROM receipt_items i WHERE i.receipt_id = r.id), r.category_reason`

func scanReceipt(row interface{ Scan(...any) error }) (*Receipt, error) {
	var r Receipt
	var payments string
	if err := row.Scan(&r.ID, &r.UserID, &r.Provider, &r.Country, &r.Key, &r.InvoiceNumber, &r.MerchantID, &r.Location, &r.IssuedAt, &r.Date, &r.Time, &r.Currency,
		&r.Total, &r.TaxTotal, &r.Method, &payments, &r.Kind, &r.InvoiceType, &r.Referent, &r.Cashier, &r.Device, &r.Journal, &r.URL, &r.Complete, &r.HasItems, &r.TransactionID,
		&r.CreatedAt, &r.UpdatedAt, &r.ItemCount, &r.CategoryReason); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(payments), &r.Payments)
	if r.Payments == nil {
		r.Payments = []fiscal.Payment{}
	}
	r.Items = []ReceiptItem{}
	return &r, nil
}

// ReceiptByKey finds the person's receipt by its provider-unique key.
func (s *Store) ReceiptByKey(ctx context.Context, userID int64, provider, key string) (*Receipt, error) {
	return scanReceipt(s.DB.QueryRowContext(ctx, `SELECT `+receiptCols+` FROM receipts r WHERE r.user_id = ? AND r.provider = ? AND r.receipt_key = ?`, userID, provider, key))
}

// GetReceipt returns a receipt with its items and shop.
func (s *Store) GetReceipt(ctx context.Context, id int64) (*Receipt, error) {
	r, err := scanReceipt(s.DB.QueryRowContext(ctx, `SELECT `+receiptCols+` FROM receipts r WHERE r.id = ?`, id))
	if err != nil {
		return nil, err
	}
	return r, s.fillReceipt(ctx, r)
}

// ReceiptForTransaction returns the receipt a transaction was booked from.
func (s *Store) ReceiptForTransaction(ctx context.Context, txID int64) (*Receipt, error) {
	r, err := scanReceipt(s.DB.QueryRowContext(ctx, `SELECT `+receiptCols+` FROM receipts r WHERE r.transaction_id = ? ORDER BY r.id LIMIT 1`, txID))
	if err != nil {
		return nil, err
	}
	return r, s.fillReceipt(ctx, r)
}

func (s *Store) fillReceipt(ctx context.Context, r *Receipt) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.id, i.receipt_id, i.line, i.name, i.norm, i.gtin, i.quantity, i.unit, i.unit_price, i.total, i.tax_label, i.product_id,
		COALESCE(p.name, ''), p.group_id, COALESCE(g.name, ''), COALESCE(g.icon, ''), COALESCE(p.category_id, g.category_id),
		CASE WHEN p.category_id IS NOT NULL THEN p.category_source WHEN g.category_id IS NOT NULL THEN 'group' ELSE '' END,
		COALESCE(c.name, ''), COALESCE(c.icon, '')
		FROM receipt_items i LEFT JOIN products p ON p.id = i.product_id LEFT JOIN product_groups g ON g.id = p.group_id
		LEFT JOIN categories c ON c.id = COALESCE(p.category_id, g.category_id)
		WHERE i.receipt_id = ? ORDER BY i.line`, r.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var it ReceiptItem
		if err := rows.Scan(&it.ID, &it.ReceiptID, &it.Line, &it.Name, &it.Norm, &it.GTIN, &it.Quantity, &it.Unit, &it.UnitPrice, &it.Total, &it.TaxLabel, &it.ProductID,
			&it.ProductName, &it.GroupID, &it.GroupName, &it.GroupIcon, &it.CategoryID, &it.CategorySource, &it.CategoryName, &it.CategoryIcon); err != nil {
			rows.Close()
			return err
		}
		r.Items = append(r.Items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if r.MerchantID != nil {
		if m, err := s.GetMerchant(ctx, r.UserID, *r.MerchantID); err == nil {
			r.Merchant = m
		}
	}
	return nil
}

// ReceiptFilter narrows the receipt list.
type ReceiptFilter struct {
	UserID     int64
	MerchantID int64
	From, To   string
	Q          string
	Limit      int
	Offset     int
}

// ListReceipts returns the person's receipts, newest first.
func (s *Store) ListReceipts(ctx context.Context, f ReceiptFilter) ([]Receipt, int, error) {
	where := ` WHERE r.user_id = ?`
	args := []any{f.UserID}
	if f.MerchantID > 0 {
		where += ` AND r.merchant_id = ?`
		args = append(args, f.MerchantID)
	}
	if f.From != "" {
		where += ` AND r.date >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		where += ` AND r.date <= ?`
		args = append(args, f.To)
	}
	for _, w := range strings.Fields(fiscal.Fold(f.Q)) {
		where += ` AND (EXISTS (SELECT 1 FROM merchants m WHERE m.id = r.merchant_id AND (lower(m.display_name) LIKE ? OR lower(m.name) LIKE ?)) OR EXISTS (SELECT 1 FROM receipt_items i WHERE i.receipt_id = r.id AND i.norm LIKE ?) OR r.invoice_number LIKE ?)`
		like := "%" + w + "%"
		args = append(args, like, like, like, like)
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM receipts r`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+receiptCols+` FROM receipts r`+where+` ORDER BY r.date DESC, r.time DESC, r.id DESC LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	out := []Receipt{}
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		r.Journal = ""
		out = append(out, *r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	merchants := map[int64]*Merchant{}
	for i := range out {
		if id := out[i].MerchantID; id != nil {
			if _, ok := merchants[*id]; !ok {
				merchants[*id], _ = s.GetMerchant(ctx, f.UserID, *id)
			}
			out[i].Merchant = merchants[*id]
		}
	}
	return out, total, nil
}

// SaveReceipt inserts or (when r.ID is set) rewrites a receipt and its items.
// Every item is matched to a product (created when new) and new products
// are put into the person's groups by keyword.
func (s *Store) SaveReceipt(ctx context.Context, r *Receipt, items []fiscal.Item) error {
	groups, err := s.ListGroups(ctx, r.UserID)
	if err != nil {
		return err
	}
	payments, _ := json.Marshal(r.Payments)
	if r.Payments == nil {
		payments = []byte("[]")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if r.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO receipts (user_id, provider, country, receipt_key, invoice_number, merchant_id, location, issued_at, date, time, currency,
			total, tax_total, method, payments, kind, invoice_type, referent, cashier, device, journal, url, complete, has_items, transaction_id)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.UserID, r.Provider, r.Country, r.Key, r.InvoiceNumber, nullI(r.MerchantID), r.Location, r.IssuedAt, r.Date, r.Time, r.Currency,
			r.Total, r.TaxTotal, r.Method, string(payments), r.Kind, r.InvoiceType, r.Referent, r.Cashier, r.Device, r.Journal, r.URL, boolInt(r.Complete), boolInt(r.HasItems), nullI(r.TransactionID))
		if err != nil {
			return conflict(err)
		}
		r.ID, _ = res.LastInsertId()
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE receipts SET invoice_number=?, merchant_id=?, location=?, issued_at=?, date=?, time=?, currency=?, total=?, tax_total=?, method=?, payments=?,
			kind=?, invoice_type=?, referent=?, cashier=?, device=?, journal=?, url=?, complete=?, has_items=?, transaction_id=?, updated_at=? WHERE id=?`,
			r.InvoiceNumber, nullI(r.MerchantID), r.Location, r.IssuedAt, r.Date, r.Time, r.Currency, r.Total, r.TaxTotal, r.Method, string(payments),
			r.Kind, r.InvoiceType, r.Referent, r.Cashier, r.Device, r.Journal, r.URL, boolInt(r.Complete), boolInt(r.HasItems), nullI(r.TransactionID), Now(), r.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM receipt_items WHERE receipt_id = ?`, r.ID); err != nil {
			return err
		}
	}
	for i, it := range items {
		pid, err := ensureProduct(ctx, tx, r.UserID, it, groups)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO receipt_items (receipt_id, line, name, norm, gtin, quantity, unit, unit_price, total, tax_label, product_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, i+1, it.Name, fiscal.Fold(it.Name), it.GTIN, it.Quantity, it.Unit, it.UnitPrice, it.Total, it.TaxLabel, nullI(pid)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ensureProduct(ctx context.Context, tx *sql.Tx, userID int64, it fiscal.Item, groups []ProductGroup) (*int64, error) {
	key := fiscal.ProductKey(it.Name)
	if key == "" {
		return nil, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM products WHERE user_id = ? AND key = ?`, userID, key).Scan(&id)
	if err == nil {
		return &id, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO products (user_id, key, name, unit, group_id) VALUES (?,?,?,?,?)`,
		userID, key, fiscal.ProductName(it.Name), it.Unit, nullI(GroupFor(key, groups)))
	if err != nil {
		return nil, err
	}
	id, _ = res.LastInsertId()
	return &id, nil
}

// GroupFor picks the group whose longest matching keyword is in the product
// key ("ledeni caj" beats "caj"); nil when none matches.
func GroupFor(key string, groups []ProductGroup) *int64 {
	var best *int64
	bestLen := 0
	for i := range groups {
		for _, kw := range fiscal.Keywords(groups[i].Keywords) {
			if len(kw) > bestLen && fiscal.MatchesKeyword(key, kw) {
				id := groups[i].ID
				best, bestLen = &id, len(kw)
			}
		}
	}
	return best
}

// SetReceiptTransaction links a receipt to the transaction booked from it.
func (s *Store) SetReceiptTransaction(ctx context.Context, receiptID int64, txID *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE receipts SET transaction_id = ?, updated_at = ? WHERE id = ?`, nullI(txID), Now(), receiptID)
	return err
}

// LinkReceiptTx is SetReceiptTransaction inside a running transaction.
func LinkReceiptTx(ctx context.Context, tx *sql.Tx, receiptID, txID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE receipts SET transaction_id = ?, updated_at = ? WHERE id = ?`, txID, Now(), receiptID)
	return err
}

// DeleteReceipt removes a receipt and its items (the transaction stays).
func (s *Store) DeleteReceipt(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM receipts WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UnreceiptedMatch finds a hand-typed expense that is the same purchase as a
// receipt: same amount (in the receipt's currency) within a day, on one of
// the accounts, not yet linked to any receipt. The closest in time wins.
func (s *Store) UnreceiptedMatch(ctx context.Context, accountIDs []int64, date string, amount int64, currency, kind string) (int64, error) {
	if len(accountIDs) == 0 {
		return 0, ErrNotFound
	}
	in, args := inList(accountIDs)
	q := `SELECT t.id FROM transactions t JOIN accounts a ON a.id = t.account_id
		WHERE t.account_id IN ` + in + ` AND t.kind = ? AND t.source != 'receipt'
		AND t.date BETWEEN date(?, '-1 day') AND date(?, '+1 day')
		AND ((a.currency = ? AND t.amount = ? AND t.foreign_amount = 0) OR (t.foreign_currency = ? AND t.foreign_amount = ?))
		AND NOT EXISTS (SELECT 1 FROM receipts r WHERE r.transaction_id = t.id)
		ORDER BY abs(julianday(t.date) - julianday(?)), t.id DESC LIMIT 1`
	args = append(args, kind, date, date, currency, amount, currency, amount, date)
	var id int64
	err := s.DB.QueryRowContext(ctx, q, args...).Scan(&id)
	return id, notFound(err)
}

// ---- settings ----

// GetReceiptSettings returns the person's receipt preferences (defaults when unset).
func (s *Store) GetReceiptSettings(ctx context.Context, userID int64) ReceiptSettings {
	st := ReceiptSettings{AttachManual: true, OwnWords: true, Geocode: true}
	var rules string
	_ = s.DB.QueryRowContext(ctx, `SELECT card_account_id, cash_account_id, split_items, attach_manual, ai_items, own_words, geocode, shop_rules FROM receipt_settings WHERE user_id = ?`, userID).
		Scan(&st.CardAccountID, &st.CashAccountID, &st.SplitItems, &st.AttachManual, &st.AIItems, &st.OwnWords, &st.Geocode, &rules)
	if rules != "" {
		_ = json.Unmarshal([]byte(rules), &st.ShopRules)
	}
	return st
}

// SaveReceiptSettings stores the person's receipt preferences.
func (s *Store) SaveReceiptSettings(ctx context.Context, userID int64, st ReceiptSettings) error {
	rules := ""
	if st.ShopRules != nil {
		b, _ := json.Marshal(st.ShopRules)
		rules = string(b)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO receipt_settings (user_id, card_account_id, cash_account_id, split_items, attach_manual, ai_items, own_words, geocode, shop_rules) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id) DO UPDATE SET card_account_id = excluded.card_account_id, cash_account_id = excluded.cash_account_id,
		split_items = excluded.split_items, attach_manual = excluded.attach_manual, ai_items = excluded.ai_items, own_words = excluded.own_words,
		geocode = excluded.geocode, shop_rules = excluded.shop_rules`,
		userID, nullI(st.CardAccountID), nullI(st.CashAccountID), boolInt(st.SplitItems), boolInt(st.AttachManual), boolInt(st.AIItems), boolInt(st.OwnWords), boolInt(st.Geocode), rules)
	return err
}

// ---- product groups and products ----

// ListGroups returns the person's product groups in their order.
func (s *Store) ListGroups(ctx context.Context, userID int64) ([]ProductGroup, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, name, icon, keywords, category_id, sort_order, created_at FROM product_groups WHERE user_id = ? ORDER BY sort_order, name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProductGroup{}
	for rows.Next() {
		var g ProductGroup
		if err := rows.Scan(&g.ID, &g.UserID, &g.Name, &g.Icon, &g.Keywords, &g.CategoryID, &g.SortOrder, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGroup returns one of the person's groups.
func (s *Store) GetGroup(ctx context.Context, userID, id int64) (*ProductGroup, error) {
	var g ProductGroup
	err := s.DB.QueryRowContext(ctx, `SELECT id, user_id, name, icon, keywords, category_id, sort_order, created_at FROM product_groups WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&g.ID, &g.UserID, &g.Name, &g.Icon, &g.Keywords, &g.CategoryID, &g.SortOrder, &g.CreatedAt)
	return &g, notFound(err)
}

// CreateGroup adds a group.
func (s *Store) CreateGroup(ctx context.Context, g *ProductGroup) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO product_groups (user_id, name, icon, keywords, category_id, sort_order) VALUES (?,?,?,?,?,?)`,
		g.UserID, g.Name, g.Icon, g.Keywords, nullI(g.CategoryID), g.SortOrder)
	if err != nil {
		return conflict(err)
	}
	g.ID, _ = res.LastInsertId()
	return nil
}

// UpdateGroup saves a group.
func (s *Store) UpdateGroup(ctx context.Context, g *ProductGroup) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE product_groups SET name = ?, icon = ?, keywords = ?, category_id = ?, sort_order = ? WHERE id = ? AND user_id = ?`,
		g.Name, g.Icon, g.Keywords, nullI(g.CategoryID), g.SortOrder, g.ID, g.UserID)
	return conflict(err)
}

// DeleteGroup removes a group; its products become ungrouped (and are
// matched again by the remaining groups' keywords).
func (s *Store) DeleteGroup(ctx context.Context, userID, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE products SET group_id = NULL, group_locked = 0 WHERE group_id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	_, err := s.RegroupProducts(ctx, userID)
	return err
}

// RegroupProducts puts every product the person did not place by hand into
// the group its name matches now. It returns how many moved.
func (s *Store) RegroupProducts(ctx context.Context, userID int64) (int, error) {
	groups, err := s.ListGroups(ctx, userID)
	if err != nil {
		return 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, key, group_id FROM products WHERE user_id = ? AND group_locked = 0`, userID)
	if err != nil {
		return 0, err
	}
	type change struct {
		id    int64
		group *int64
	}
	var changes []change
	for rows.Next() {
		var id int64
		var key string
		var cur *int64
		if err := rows.Scan(&id, &key, &cur); err != nil {
			rows.Close()
			return 0, err
		}
		next := GroupFor(key, groups)
		if (cur == nil) != (next == nil) || (cur != nil && *cur != *next) {
			changes = append(changes, change{id, next})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, c := range changes {
		if _, err := s.DB.ExecContext(ctx, `UPDATE products SET group_id = ? WHERE id = ?`, nullI(c.group), c.id); err != nil {
			return 0, err
		}
	}
	return len(changes), nil
}

// GetProduct returns one of the person's products.
func (s *Store) GetProduct(ctx context.Context, userID, id int64) (*Product, error) {
	var p Product
	err := s.DB.QueryRowContext(ctx, `SELECT id, user_id, key, name, unit, group_id, group_locked, created_at, category_id, category_source FROM products WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&p.ID, &p.UserID, &p.Key, &p.Name, &p.Unit, &p.GroupID, &p.GroupLocked, &p.CreatedAt, &p.CategoryID, &p.CategorySource)
	return &p, notFound(err)
}

// UpdateProduct saves a product's name and (hand-picked) group.
func (s *Store) UpdateProduct(ctx context.Context, p *Product) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE products SET name = ?, group_id = ?, group_locked = ? WHERE id = ? AND user_id = ?`,
		p.Name, nullI(p.GroupID), boolInt(p.GroupLocked), p.ID, p.UserID)
	return err
}

// SetProductCategory gives a product its own category (source you | ai).
func (s *Store) SetProductCategory(ctx context.Context, userID, id int64, cat *int64, source string) error {
	if cat == nil {
		source = ""
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE products SET category_id = ?, category_source = ?, ai_checked = 1 WHERE id = ? AND user_id = ?`, nullI(cat), source, id, userID)
	return err
}

// ProductToSort is a product with no category of its own or from its group.
type ProductToSort struct {
	ID   int64
	Name string
	Shop string // where it was last bought
}

// ProductsToSort lists the person's products the AI has not been asked
// about and that have no category (own or group's); only ids when given.
func (s *Store) ProductsToSort(ctx context.Context, userID int64, ids []int64, limit int) ([]ProductToSort, error) {
	q := `SELECT p.id, p.name, COALESCE((SELECT COALESCE(NULLIF(m.display_name, ''), m.name) FROM receipt_items i JOIN receipts r ON r.id = i.receipt_id
			JOIN merchants m ON m.id = r.merchant_id WHERE i.product_id = p.id ORDER BY r.id DESC LIMIT 1), '')
		FROM products p LEFT JOIN product_groups g ON g.id = p.group_id
		WHERE p.user_id = ? AND p.ai_checked = 0 AND p.category_id IS NULL AND g.category_id IS NULL`
	args := []any{userID}
	if ids != nil {
		in, a := inList(ids)
		q += ` AND p.id IN ` + in
		args = append(args, a...)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY p.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductToSort
	for rows.Next() {
		var p ProductToSort
		if err := rows.Scan(&p.ID, &p.Name, &p.Shop); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ForgetAISorting drops the categories the AI gave, so it is asked again
// (the person's own choices stay).
func (s *Store) ForgetAISorting(ctx context.Context, userID int64) (int, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE products SET category_id = NULL, category_source = '', ai_checked = 0 WHERE user_id = ? AND (category_source = 'ai' OR (category_id IS NULL AND ai_checked = 1))`, userID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CategoryUse is how a person uses one category: how often, and the
// descriptions they write in it, most used first.
type CategoryUse struct {
	Count        int
	Descriptions []string // "nabavka za kuci (60)"
}

// CategoryUses summarises the person's last year of expenses per category.
func (s *Store) CategoryUses(ctx context.Context, userID int64, perCategory int) (map[int64]*CategoryUse, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.category_id, lower(t.description), COUNT(1) n FROM transactions t
		WHERE t.user_id = ? AND t.kind = 'expense' AND t.category_id IS NOT NULL AND t.date >= date('now', '-365 days')
		GROUP BY t.category_id, lower(t.description) ORDER BY t.category_id, n DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*CategoryUse{}
	for rows.Next() {
		var id int64
		var d string
		var n int
		if err := rows.Scan(&id, &d, &n); err != nil {
			return nil, err
		}
		u := out[id]
		if u == nil {
			u = &CategoryUse{}
			out[id] = u
		}
		u.Count += n
		if d != "" && len(u.Descriptions) < perCategory {
			u.Descriptions = append(u.Descriptions, fmt.Sprintf("%s (%d)", d, n))
		}
	}
	return out, rows.Err()
}

// MarkProductsAsked records that the AI was asked about products.
func (s *Store) MarkProductsAsked(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	in, args := inList(ids)
	_, err := s.DB.ExecContext(ctx, `UPDATE products SET ai_checked = 1 WHERE id IN `+in, args...)
	return err
}

// SetReceiptReason stores why the receipt's transaction got its category.
func (s *Store) SetReceiptReason(ctx context.Context, id int64, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE receipts SET category_reason = ? WHERE id = ?`, reason, id)
	return err
}

// DescribedAs is how the person described their own bookings in a category.
type DescribedAs struct {
	Description string
	CategoryID  int64
	Count       int
}

// OwnDescriptions are the person's most used descriptions per category over
// the last year (hand-typed and template bookings, at least three times).
func (s *Store) OwnDescriptions(ctx context.Context, userID int64) ([]DescribedAs, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.description, t.category_id, COUNT(1) n FROM transactions t
		WHERE t.user_id = ? AND t.kind = 'expense' AND t.category_id IS NOT NULL AND t.description != '' AND t.source NOT IN ('receipt')
		AND t.date >= date('now', '-365 days')
		GROUP BY lower(t.description), t.category_id HAVING n >= 3 ORDER BY n DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DescribedAs
	for rows.Next() {
		var d DescribedAs
		if err := rows.Scan(&d.Description, &d.CategoryID, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- statistics ----

// ItemStatsFilter narrows the purchase statistics.
type ItemStatsFilter struct {
	UserID     int64
	From, To   string
	GroupID    int64 // with Ungrouped=false: only this group
	ProductID  int64
	Ungrouped  bool
	MerchantID int64
}

// ItemStats sums purchases of a product or group. Spend is in the home
// currency: each line's share of the transaction it was booked as (or the
// receipt total when it was not booked).
type ItemStats struct {
	Key            int64   `json:"key"` // group or product id; 0 = no group
	Name           string  `json:"name"`
	Icon           string  `json:"icon"`
	GroupID        *int64  `json:"group_id,omitempty"`
	Purchases      int     `json:"purchases"` // receipts it was on
	Lines          int     `json:"lines"`
	Quantity       float64 `json:"quantity"`
	Unit           string  `json:"unit"` // when all lines share one
	Spent          int64   `json:"spent"`
	AvgPrice       int64   `json:"avg_price"` // home currency per unit
	LastDate       string  `json:"last_date"`
	LastPrice      int64   `json:"last_price"` // receipt currency
	LastCur        string  `json:"last_currency"`
	Products       int     `json:"products"`
	CategoryID     *int64  `json:"category_id,omitempty"`
	Keywords       string  `json:"keywords,omitempty"`
	Locked         bool    `json:"locked,omitempty"`
	CategorySource string  `json:"category_source,omitempty"` // products: you | ai
}

// share of a line in the home currency
const lineHome = `CAST(ROUND(CASE WHEN t.id IS NOT NULL AND r.total > 0 THEN i.total * 1.0 * t.amount_home / r.total ELSE i.total END) AS INTEGER)`

func (f ItemStatsFilter) where() (string, []any) {
	w := ` WHERE r.user_id = ? AND r.kind = 'sale'`
	args := []any{f.UserID}
	if f.From != "" {
		w += ` AND r.date >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		w += ` AND r.date <= ?`
		args = append(args, f.To)
	}
	if f.MerchantID > 0 {
		w += ` AND r.merchant_id = ?`
		args = append(args, f.MerchantID)
	}
	if f.ProductID > 0 {
		w += ` AND i.product_id = ?`
		args = append(args, f.ProductID)
	} else if f.Ungrouped {
		w += ` AND p.group_id IS NULL`
	} else if f.GroupID > 0 {
		w += ` AND p.group_id = ?`
		args = append(args, f.GroupID)
	}
	return w, args
}

const statsFrom = ` FROM receipt_items i JOIN receipts r ON r.id = i.receipt_id LEFT JOIN transactions t ON t.id = r.transaction_id LEFT JOIN products p ON p.id = i.product_id`

// GroupStats returns one row per group (plus "no group" with key 0), most spent first.
func (s *Store) GroupStats(ctx context.Context, f ItemStatsFilter) ([]ItemStats, error) {
	f.GroupID, f.ProductID, f.Ungrouped = 0, 0, false
	w, args := f.where()
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(p.group_id, 0), COUNT(DISTINCT r.id), COUNT(1), COALESCE(SUM(i.quantity), 0), COALESCE(SUM(`+lineHome+`), 0),
		MAX(r.date), COUNT(DISTINCT i.product_id), CASE WHEN COUNT(DISTINCT i.unit) = 1 THEN MAX(i.unit) ELSE '' END`+statsFrom+w+` GROUP BY COALESCE(p.group_id, 0)`, args...)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*ItemStats{}
	for rows.Next() {
		var x ItemStats
		if err := rows.Scan(&x.Key, &x.Purchases, &x.Lines, &x.Quantity, &x.Spent, &x.LastDate, &x.Products, &x.Unit); err != nil {
			rows.Close()
			return nil, err
		}
		byID[x.Key] = &x
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	groups, err := s.ListGroups(ctx, f.UserID)
	if err != nil {
		return nil, err
	}
	out := []ItemStats{}
	for _, g := range groups {
		x := byID[g.ID]
		if x == nil {
			x = &ItemStats{Key: g.ID}
		}
		x.Name, x.Icon, x.CategoryID, x.Keywords = g.Name, g.Icon, g.CategoryID, g.Keywords
		id := g.ID
		x.GroupID = &id
		out = append(out, *x)
	}
	if x := byID[0]; x != nil {
		x.Name, x.Icon = "No group", "📦"
		out = append(out, *x)
	}
	for i := range out {
		if out[i].Quantity > 0 {
			out[i].AvgPrice = int64(float64(out[i].Spent)/out[i].Quantity + 0.5)
		}
	}
	// most spent first; "no group" always last
	sort.SliceStable(out, func(a, b int) bool {
		if (out[a].Key == 0) != (out[b].Key == 0) {
			return out[b].Key == 0
		}
		return out[a].Spent > out[b].Spent
	})
	return out, nil
}

// ProductStats returns one row per product in the filter, most spent first.
func (s *Store) ProductStats(ctx context.Context, f ItemStatsFilter, q string, limit int) ([]ItemStats, error) {
	w, args := f.where()
	for _, word := range strings.Fields(fiscal.Fold(q)) {
		w += ` AND (p.key LIKE ? OR i.norm LIKE ?)`
		args = append(args, "%"+word+"%", "%"+word+"%")
	}
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(p.id, 0), COALESCE(p.name, MAX(i.name)), p.group_id, COALESCE(p.group_locked, 0), COUNT(DISTINCT r.id), COUNT(1), COALESCE(SUM(i.quantity), 0),
		COALESCE(SUM(`+lineHome+`), 0), MAX(r.date), CASE WHEN COUNT(DISTINCT i.unit) = 1 THEN MAX(i.unit) ELSE '' END, p.category_id, COALESCE(p.category_source, '')`+statsFrom+w+`
		GROUP BY COALESCE(p.id, -i.id) ORDER BY 8 DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	out := []ItemStats{}
	for rows.Next() {
		var x ItemStats
		if err := rows.Scan(&x.Key, &x.Name, &x.GroupID, &x.Locked, &x.Purchases, &x.Lines, &x.Quantity, &x.Spent, &x.LastDate, &x.Unit, &x.CategoryID, &x.CategorySource); err != nil {
			rows.Close()
			return nil, err
		}
		if x.Quantity > 0 {
			x.AvgPrice = int64(float64(x.Spent)/x.Quantity + 0.5)
		}
		out = append(out, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Key > 0 {
			_ = s.DB.QueryRowContext(ctx, `SELECT i.unit_price, r.currency FROM receipt_items i JOIN receipts r ON r.id = i.receipt_id WHERE i.product_id = ? AND r.kind = 'sale' ORDER BY r.date DESC, r.time DESC LIMIT 1`, out[i].Key).
				Scan(&out[i].LastPrice, &out[i].LastCur)
		}
	}
	return out, nil
}

// Purchase is one receipt line, for price history.
type Purchase struct {
	Date          string  `json:"date"`
	Time          string  `json:"time"`
	ReceiptID     int64   `json:"receipt_id"`
	TransactionID *int64  `json:"transaction_id"`
	ProductID     *int64  `json:"product_id"`
	Name          string  `json:"name"`
	Quantity      float64 `json:"quantity"`
	Unit          string  `json:"unit"`
	UnitPrice     int64   `json:"unit_price"`
	Total         int64   `json:"total"`
	Currency      string  `json:"currency"`
	Home          int64   `json:"home"`
	MerchantID    *int64  `json:"merchant_id"`
	Merchant      string  `json:"merchant"`
}

// Purchases lists the lines in the filter, newest first.
func (s *Store) Purchases(ctx context.Context, f ItemStatsFilter, limit int) ([]Purchase, error) {
	w, args := f.where()
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.date, r.time, r.id, r.transaction_id, i.product_id, COALESCE(p.name, i.name), i.quantity, i.unit, i.unit_price, i.total, r.currency, `+lineHome+`,
		r.merchant_id, COALESCE(NULLIF(m.display_name, ''), m.name, '')`+statsFrom+` LEFT JOIN merchants m ON m.id = r.merchant_id`+w+`
		ORDER BY r.date DESC, r.time DESC, i.line LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Purchase{}
	for rows.Next() {
		var x Purchase
		if err := rows.Scan(&x.Date, &x.Time, &x.ReceiptID, &x.TransactionID, &x.ProductID, &x.Name, &x.Quantity, &x.Unit, &x.UnitPrice, &x.Total, &x.Currency, &x.Home,
			&x.MerchantID, &x.Merchant); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
