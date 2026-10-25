package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- scanning fiscal receipts ----
//
// A scan is logged first (receipt_scans), then the link is decoded offline
// and the receipt fetched from the tax office. A complete receipt is stored
// with its shop and items and booked as one transaction (or linked to the
// hand-typed one it duplicates). When the tax office cannot be reached, what
// the QR code itself carries (total, time, receipt number) is booked right
// away and the scan is retried in the background to fill in shop and items.

// scanResult is what a scan became.
type scanResult struct {
	Status      string             `json:"status"` // booked | linked | duplicate | partial | pending | invalid | skipped | unsupported | failed
	Message     string             `json:"message"`
	Scan        *store.ReceiptScan `json:"scan"`
	Receipt     *store.Receipt     `json:"receipt,omitempty"`
	Transaction *store.Transaction `json:"transaction,omitempty"`
}

type scanOptions struct {
	Source    string
	AccountID int64
}

// receiptTransport replaces the network in tests.
var receiptTransport http.RoundTripper

func (s *Server) receiptHTTP() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, Transport: receiptTransport}
}

// retry back-off after the nth failed attempt; give up after the last
var scanBackoff = []time.Duration{10 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 24 * time.Hour, 24 * time.Hour}

func (s *Server) scanReceipt(ctx context.Context, text string, opt scanOptions) (*scanResult, error) {
	uid := actorID(ctx)
	scan := &store.ReceiptScan{UserID: uid, URL: strings.TrimSpace(text), Status: "pending", Source: opt.Source}
	if opt.AccountID > 0 {
		scan.AccountID = &opt.AccountID
	}
	p, u, err := fiscal.Find(text)
	if err != nil {
		scan.Status, scan.Message = "unsupported", fiscal.ErrUnsupported.Error()
		if len(scan.URL) > 2000 {
			scan.URL = scan.URL[:2000]
		}
		if err := s.store.CreateScan(ctx, scan); err != nil {
			return nil, err
		}
		return &scanResult{Status: scan.Status, Message: scan.Message, Scan: scan}, nil
	}
	scan.Provider, scan.Country = p.ID(), p.Country()
	scan.URL = u.String()
	if err := s.store.CreateScan(ctx, scan); err != nil {
		return nil, err
	}
	return s.processScan(ctx, scan, p)
}

// processScan runs (or re-runs) a logged scan.
func (s *Server) processScan(ctx context.Context, scan *store.ReceiptScan, p fiscal.Provider) (*scanResult, error) {
	_, u, err := fiscal.Find(scan.URL)
	if err != nil {
		return nil, err
	}
	decoded, _ := p.Decode(u)
	// the receipt may be stored already: by this scan (a retry) or an earlier one
	var have *store.Receipt
	if scan.ReceiptID != nil {
		have, _ = s.store.GetReceipt(ctx, *scan.ReceiptID)
	}
	if have == nil && decoded != nil && decoded.Key != "" {
		scan.ReceiptKey = decoded.Key
		have = s.existingReceipt(ctx, scan.UserID, decoded.Provider, decoded.Key)
	}
	if have != nil && have.Complete {
		return s.alreadyHave(ctx, scan, have)
	}
	scan.Attempts++
	fctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	rec, raw, ferr := p.Fetch(fctx, s.receiptHTTP(), u)
	cancel()
	if len(raw) > 0 {
		scan.Raw = string(raw)
	}
	switch {
	case ferr == nil:
		scan.ReceiptKey = rec.Key
		if rec.InvoiceType == "proforma" || rec.InvoiceType == "training" {
			scan.Status, scan.Message, scan.NextTryAt = "skipped", fmt.Sprintf("This is a %s receipt: nothing was paid, nothing booked.", rec.InvoiceType), ""
			return s.finishScan(ctx, scan, nil, nil)
		}
		if have == nil {
			have = s.existingReceipt(ctx, scan.UserID, rec.Provider, rec.Key)
		}
		switch {
		case have == nil:
			return s.storeAndBook(ctx, scan, rec)
		case have.Complete || !rec.Complete:
			return s.alreadyHave(ctx, scan, have)
		default:
			// booked offline before; the tax office now knows it in full
			return s.completeReceipt(ctx, scan, have, rec)
		}
	case errors.Is(ferr, fiscal.ErrInvalid):
		scan.Status, scan.Message, scan.NextTryAt = "invalid", ferr.Error(), ""
		return s.finishScan(ctx, scan, nil, nil)
	}
	// the tax office is unreachable or does not have it yet: book what the code says
	scan.Message = ferr.Error()
	scan.Status = "partial"
	if scan.Attempts > len(scanBackoff) {
		scan.Status, scan.NextTryAt = "failed", ""
		scan.Message = "gave up after " + strconv.Itoa(scan.Attempts) + " tries: " + ferr.Error()
	} else {
		scan.NextTryAt = time.Now().UTC().Add(scanBackoff[scan.Attempts-1]).Format(time.RFC3339)
	}
	if have != nil {
		scan.ReceiptID = &have.ID
		var t *store.Transaction
		if have.TransactionID != nil {
			t, _ = s.store.GetTransaction(ctx, *have.TransactionID)
		}
		return s.finishScan(ctx, scan, have, t)
	}
	if decoded != nil && decoded.Total > 0 && !decoded.IssuedAt.IsZero() {
		return s.storeAndBook(ctx, scan, decoded)
	}
	if scan.Status != "failed" {
		scan.Status = "pending"
	}
	return s.finishScan(ctx, scan, nil, nil)
}

func (s *Server) existingReceipt(ctx context.Context, uid int64, provider, key string) *store.Receipt {
	r, err := s.store.ReceiptByKey(ctx, uid, provider, key)
	if err != nil {
		return nil
	}
	full, err := s.store.GetReceipt(ctx, r.ID)
	if err != nil {
		return nil
	}
	return full
}

// alreadyHave answers a scan of a receipt that is stored already: a
// duplicate, or booked again when its transaction was deleted since.
func (s *Server) alreadyHave(ctx context.Context, scan *store.ReceiptScan, full *store.Receipt) (*scanResult, error) {
	own := scan.ReceiptID != nil && *scan.ReceiptID == full.ID
	scan.ReceiptID, scan.NextTryAt = &full.ID, ""
	if full.TransactionID == nil {
		t, how, err := s.bookReceipt(ctx, full, scan.AccountID)
		if err != nil {
			return nil, err
		}
		scan.Status, scan.Message = "done", how
		full, _ = s.store.GetReceipt(ctx, full.ID)
		return s.finishScan(ctx, scan, full, t)
	}
	var t *store.Transaction
	if x, err := s.store.GetTransaction(ctx, *full.TransactionID); err == nil && s.canSee(ctx, x.AccountID) {
		t = x
	}
	if own {
		if scan.Status != "done" {
			scan.Status, scan.Message = "done", "Complete."
		}
		return s.finishScan(ctx, scan, full, t)
	}
	scan.Status = "duplicate"
	scan.Message = "Already scanned on " + full.CreatedAt[:10] + "."
	return s.finishScan(ctx, scan, full, t)
}

func (s *Server) finishScan(ctx context.Context, scan *store.ReceiptScan, r *store.Receipt, t *store.Transaction) (*scanResult, error) {
	if err := s.store.UpdateScan(ctx, scan); err != nil {
		return nil, err
	}
	status := scan.Status
	switch {
	case status == "done" && strings.HasPrefix(scan.Message, "Linked"):
		status = "linked"
	case status == "done":
		status = "booked"
	}
	return &scanResult{Status: status, Message: scan.Message, Scan: scan, Receipt: r, Transaction: t}, nil
}

// receiptRow turns a fiscal receipt into the stored row (shop included).
func (s *Server) receiptRow(ctx context.Context, uid int64, rec *fiscal.Receipt, scanURL string) (*store.Receipt, error) {
	r := &store.Receipt{UserID: uid, Provider: rec.Provider, Country: rec.Country, Key: rec.Key, InvoiceNumber: rec.InvoiceNumber,
		Location: fiscal.Latinize(fiscal.ShortLocation(rec.Merchant.Location, rec.Merchant.Name)), Currency: rec.Currency, Total: rec.Total, TaxTotal: rec.TaxTotal, Method: rec.Method(), Payments: rec.Payments,
		Kind: rec.Kind, InvoiceType: rec.InvoiceType, Referent: rec.Referent, Cashier: rec.Cashier, Device: rec.Device, Journal: rec.Journal,
		URL: scanURL, Complete: rec.Complete, HasItems: rec.HasItems}
	if !rec.IssuedAt.IsZero() {
		r.IssuedAt = rec.IssuedAt.Format(time.RFC3339)
		r.Date, r.Time = rec.IssuedAt.Format("2006-01-02"), rec.IssuedAt.Format("15:04")
	} else {
		r.Date = store.Today()
	}
	switch {
	case rec.Merchant.TaxID != "" && rec.Merchant.Name != "":
		shop := rec.Merchant
		shop.Address, shop.City = fiscal.Latinize(shop.Address), fiscal.Latinize(shop.City)
		m, err := s.store.UpsertMerchant(ctx, uid, rec.Country, shop)
		if err != nil {
			return nil, err
		}
		s.categorizeShop(ctx, m, r.Location)
		r.MerchantID = &m.ID
	default:
		// an offline scan: the device that signed it names the shop
		if m, err := s.store.MerchantForDevice(ctx, uid, rec.Provider, rec.Device); err == nil {
			r.MerchantID = &m.ID
		}
	}
	return r, nil
}

func (s *Server) storeAndBook(ctx context.Context, scan *store.ReceiptScan, rec *fiscal.Receipt) (*scanResult, error) {
	r, err := s.receiptRow(ctx, scan.UserID, rec, scan.URL)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveReceipt(ctx, r, rec.Items); err != nil {
		if errors.Is(err, store.ErrConflict) { // the same receipt scanned twice at once
			if have := s.existingReceipt(ctx, scan.UserID, rec.Provider, rec.Key); have != nil {
				return s.alreadyHave(ctx, scan, have)
			}
		}
		return nil, err
	}
	scan.ReceiptID = &r.ID
	full, err := s.store.GetReceipt(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	t, how, err := s.bookReceipt(ctx, full, scan.AccountID)
	if err != nil {
		scan.Status, scan.Message = "failed", "stored, but booking failed: "+err.Error()
		return s.finishScan(ctx, scan, full, nil)
	}
	if scan.Status == "partial" || scan.Status == "failed" {
		scan.Message = how + " Shop and items will be filled in once the tax office answers (" + scan.Message + ")."
	} else {
		scan.Status, scan.Message, scan.NextTryAt = "done", how, ""
	}
	full, _ = s.store.GetReceipt(ctx, r.ID)
	return s.finishScan(ctx, scan, full, t)
}

// completeReceipt fills an offline-booked receipt with what the tax office
// returned, and its transaction's payee/category while they are still the
// placeholders the offline booking wrote.
func (s *Server) completeReceipt(ctx context.Context, scan *store.ReceiptScan, old *store.Receipt, rec *fiscal.Receipt) (*scanResult, error) {
	r, err := s.receiptRow(ctx, scan.UserID, rec, old.URL)
	if err != nil {
		return nil, err
	}
	r.ID, r.TransactionID, r.CreatedAt = old.ID, old.TransactionID, old.CreatedAt
	if err := s.store.SaveReceipt(ctx, r, rec.Items); err != nil {
		return nil, err
	}
	full, err := s.store.GetReceipt(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	var t *store.Transaction
	if full.TransactionID != nil {
		if cur, err := s.store.GetTransaction(ctx, *full.TransactionID); err == nil && s.canWrite(ctx, cur.AccountID) {
			t = cur
			if cur.Source == "receipt" && (untouched(cur, old) || strings.TrimPrefix(cur.Description, "Refund · ") == s.guessReceipt(ctx, old).Description) {
				t = s.refreshFromReceipt(ctx, cur, full)
			}
		}
	}
	scan.ReceiptID = &full.ID
	scan.Status, scan.NextTryAt = "done", ""
	scan.Message = "Shop and items filled in."
	// the earlier scan that booked it offline is done too
	return s.finishScan(ctx, scan, full, t)
}

// untouched: the transaction still has the description the booking wrote.
func untouched(t *store.Transaction, r *store.Receipt) bool {
	d := strings.TrimPrefix(t.Description, "Refund · ")
	return d == placeholderDescription(r) || (d != "" && d == payeeFor(r))
}

func placeholderDescription(r *store.Receipt) string {
	return "Receipt " + r.InvoiceNumber
}

func payeeFor(r *store.Receipt) string {
	if r.Merchant != nil {
		if r.Merchant.DisplayName != "" {
			return r.Merchant.DisplayName
		}
		return fiscal.MerchantName(r.Merchant.Name)
	}
	return ""
}

func receiptNotes(r *store.Receipt) string {
	parts := []string{"Fiscal receipt " + r.InvoiceNumber}
	if r.ItemCount > 0 || len(r.Items) > 0 {
		n := len(r.Items)
		if n == 0 {
			n = r.ItemCount
		}
		parts = append(parts, fmt.Sprintf("%d items", n))
	}
	if r.Location != "" {
		parts = append(parts, r.Location)
	}
	return strings.Join(parts, " · ")
}

// receiptSplits divides a receipt by its items' group categories (items
// without one, and any rounding or discount, go to the main category).
func receiptSplits(r *store.Receipt, main *int64) []splitIn {
	type bucket struct {
		cat *int64
		amt int64
	}
	var buckets []*bucket
	find := func(c *int64) *bucket {
		for _, b := range buckets {
			if (b.cat == nil && c == nil) || (b.cat != nil && c != nil && *b.cat == *c) {
				return b
			}
		}
		b := &bucket{cat: c}
		buckets = append(buckets, b)
		return b
	}
	var sum int64
	for _, it := range r.Items {
		c := it.CategoryID
		if c == nil {
			c = main
		}
		find(c).amt += it.Total
		sum += it.Total
	}
	if len(buckets) < 2 {
		return nil
	}
	find(main).amt += r.Total - sum
	out := []splitIn{}
	for _, b := range buckets {
		if b.amt <= 0 {
			return nil // discounts bigger than a part: keep it whole
		}
		out = append(out, splitIn{CategoryID: b.cat, Amount: b.amt})
	}
	return out
}

// receiptAccount picks where a receipt is booked: the account asked for at
// scan time, the shop's account, the account for the payment method, a cash
// account for cash, else the default.
func (s *Server) receiptAccount(ctx context.Context, r *store.Receipt, hint *int64) (int64, []store.Account) {
	_, writable := s.quickContext(ctx)
	ok := func(id *int64) bool {
		if id == nil {
			return false
		}
		for _, a := range writable {
			if a.ID == *id {
				return true
			}
		}
		return false
	}
	if ok(hint) {
		return *hint, writable
	}
	if r.Merchant != nil && ok(r.Merchant.AccountID) {
		return *r.Merchant.AccountID, writable
	}
	rs := s.store.GetReceiptSettings(ctx, actorID(ctx))
	if r.Method == "cash" {
		if ok(rs.CashAccountID) {
			return *rs.CashAccountID, writable
		}
		for _, a := range writable {
			if a.Kind == "cash" && a.Currency == r.Currency {
				return a.ID, writable
			}
		}
		for _, a := range writable {
			if a.Kind == "cash" {
				return a.ID, writable
			}
		}
	} else if ok(rs.CardAccountID) {
		return *rs.CardAccountID, writable
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	return s.defaultAccount(ctx, st, writable), writable
}

// prepareReceipt does the slow lookups before booking: the shop's map pin
// and, when switched on, the AI sorting of products nobody categorised yet.
func (s *Server) prepareReceipt(ctx context.Context, r *store.Receipt, rs store.ReceiptSettings) *store.Receipt {
	s.placeShop(ctx, r.Merchant)
	if rs.GroupOnScan {
		var ids []int64
		for _, it := range r.Items {
			if it.GroupID == nil && it.ProductID != nil {
				ids = append(ids, *it.ProductID)
			}
		}
		if len(ids) > 0 {
			if run, err := s.runProductCheck(ctx, "scan", ids); err != nil {
				s.log.Warn("receipt: grouping", "receipt", r.ID, "err", err)
			} else if run.ByKeywords+run.BySimilar+run.ByAI > 0 {
				if full, err := s.store.GetReceipt(ctx, r.ID); err == nil {
					r = full
				}
			}
		}
	}
	if !rs.AIItems {
		return r
	}
	var ids []int64
	for _, it := range r.Items {
		if it.CategoryID == nil && it.ProductID != nil {
			ids = append(ids, *it.ProductID)
		}
	}
	if len(ids) == 0 {
		return r
	}
	if _, n, err := s.sortProducts(ctx, ids, 200); err != nil {
		s.log.Warn("receipt: AI sorting", "receipt", r.ID, "err", err)
	} else if n > 0 {
		if full, err := s.store.GetReceipt(ctx, r.ID); err == nil {
			return full
		}
	}
	return r
}

// bookReceipt books a stored receipt as a transaction, or links it to the
// hand-typed transaction for the same purchase. how says which, for people.
func (s *Server) bookReceipt(ctx context.Context, r *store.Receipt, hint *int64) (*store.Transaction, string, error) {
	ctx = withAuditSource(ctx, "receipt")
	acct, writable := s.receiptAccount(ctx, r, hint)
	if acct == 0 {
		return nil, "", errors.New("no account you can book on")
	}
	kind := "expense"
	if r.Kind == "refund" {
		kind = "income"
	}
	rs := s.store.GetReceiptSettings(ctx, actorID(ctx))
	r = s.prepareReceipt(ctx, r, rs)
	if rs.AttachManual && r.Total > 0 {
		ids := make([]int64, 0, len(writable))
		for _, a := range writable {
			ids = append(ids, a.ID)
		}
		if id, err := s.store.UnreceiptedMatch(ctx, ids, r.Date, r.Total, r.Currency, kind); err == nil {
			if err := s.store.SetReceiptTransaction(ctx, r.ID, &id); err != nil {
				return nil, "", err
			}
			t, err := s.store.GetTransaction(ctx, id)
			if err != nil {
				return nil, "", err
			}
			t = s.fillFromReceipt(ctx, t, r)
			return t, "Linked to your transaction “" + firstNonBlank(t.Description, t.Payee, "untitled") + "” of " + t.Date + ".", nil
		}
	}
	payee := payeeFor(r)
	g := s.guessReceipt(ctx, r)
	desc := g.Description
	if kind == "income" {
		desc = "Refund · " + desc
	}
	amount, cur := r.Total, r.Currency
	in := txIn{Kind: &kind, Date: &r.Date, AccountID: &acct, Amount: &amount, Currency: &cur, Description: &desc, Payee: &payee, Source: "receipt", SkipRules: false}
	if r.Time != "" {
		in.Time = &r.Time
	}
	notes := receiptNotes(r)
	in.Notes = &notes
	cat := g.CategoryID
	if cat != nil {
		in.CategoryID = cat
	}
	if m := r.Merchant; m != nil && m.Lat != nil && m.Lng != nil {
		in.Lat, in.Lng = m.Lat, m.Lng
	}
	if rs.SplitItems {
		if parts := receiptSplits(r, cat); parts != nil {
			in.Splits = &parts
		}
	}
	link := func(tx *sql.Tx, t *store.Transaction) error { return store.LinkReceiptTx(ctx, tx, r.ID, t.ID) }
	t, msg, err := s.createTx(ctx, &in, link)
	if msg != "" && (in.CategoryID != nil || in.Splits != nil) {
		// a category the account's owner does not have: book it without
		in.CategoryID, in.Splits = nil, nil
		t, msg, err = s.createTx(ctx, &in, link)
	}
	if err != nil {
		return nil, "", err
	}
	if msg != "" {
		return nil, "", errors.New(msg)
	}
	if t.CategoryID != nil {
		_ = s.store.SetReceiptReason(ctx, r.ID, g.Reason)
	}
	how := "Booked " + majorString(r.Total, r.Currency)
	if payee != "" {
		how += " at " + payee
	}
	if t.AccountName != "" {
		how += " on " + t.AccountName
	}
	if t.CategoryName != "" {
		how += " as " + t.CategoryName
	}
	return t, how + ".", nil
}

func firstNonBlank(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// fillFromReceipt fills what a hand-typed transaction left empty (payee,
// category) from the receipt it was linked to.
func (s *Server) fillFromReceipt(ctx context.Context, t *store.Transaction, r *store.Receipt) *store.Transaction {
	if !s.canWrite(ctx, t.AccountID) {
		return t
	}
	var in txIn
	changed := false
	if p := payeeFor(r); t.Payee == "" && p != "" {
		in.Payee = &p
		changed = true
	}
	if t.CategoryID == nil && len(t.Splits) == 0 {
		if g := s.guessReceipt(ctx, r); g.CategoryID != nil {
			in.CategoryID = g.CategoryID
			changed = true
			_ = s.store.SetReceiptReason(ctx, r.ID, g.Reason)
		}
	}
	if t.Lat == nil && r.Merchant != nil && r.Merchant.Lat != nil {
		in.Lat, in.Lng = r.Merchant.Lat, r.Merchant.Lng
		changed = true
	}
	if !changed {
		return t
	}
	return s.updateTxFrom(ctx, t, &in)
}

// refreshFromReceipt rewrites an offline-booked transaction once its
// receipt is complete.
func (s *Server) refreshFromReceipt(ctx context.Context, t *store.Transaction, r *store.Receipt) *store.Transaction {
	payee := payeeFor(r)
	if payee == "" {
		return t
	}
	r = s.prepareReceipt(ctx, r, s.store.GetReceiptSettings(ctx, actorID(ctx)))
	g := s.guessReceipt(ctx, r)
	desc := g.Description
	if t.Kind == "income" {
		desc = "Refund · " + desc
	}
	notes := receiptNotes(r)
	in := txIn{Payee: &payee, Description: &desc, Notes: &notes}
	if t.CategoryID == nil && len(t.Splits) == 0 && g.CategoryID != nil {
		in.CategoryID = g.CategoryID
		_ = s.store.SetReceiptReason(ctx, r.ID, g.Reason)
	}
	if t.Lat == nil && r.Merchant != nil && r.Merchant.Lat != nil {
		in.Lat, in.Lng = r.Merchant.Lat, r.Merchant.Lng
	}
	return s.updateTxFrom(ctx, t, &in)
}

func (s *Server) updateTxFrom(ctx context.Context, t *store.Transaction, in *txIn) *store.Transaction {
	ctx = withAuditSource(ctx, "receipt")
	before := snapTx(t)
	if msg := s.build(ctx, in, t); msg != "" {
		if in.CategoryID == nil {
			return t
		}
		in.CategoryID = nil
		if t2, err := s.store.GetTransaction(ctx, t.ID); err == nil {
			t = t2
		}
		if msg := s.build(ctx, in, t); msg != "" {
			return t
		}
	}
	if err := s.store.UpdateTransaction(ctx, actorID(ctx), t); err != nil {
		s.log.Warn("receipt: update transaction", "id", t.ID, "err", err)
		return t
	}
	full, err := s.store.GetTransaction(ctx, t.ID)
	if err != nil {
		return t
	}
	s.auditTx(ctx, before, snapTx(full))
	s.emitTx(ctx, "transaction.updated", full)
	return full
}

// retryReceiptScans re-asks the tax office for scans still waiting on it.
func (s *Server) retryReceiptScans(ctx context.Context) {
	due, err := s.store.DueScans(ctx, time.Now().UTC().Format(time.RFC3339), 20)
	if err != nil {
		s.log.Warn("receipt retries", "err", err)
		return
	}
	for i := range due {
		scan := &due[i]
		u, err := s.store.GetUser(ctx, scan.UserID)
		if err != nil {
			continue
		}
		p := fiscal.ByID(scan.Provider)
		if p == nil {
			continue
		}
		uctx := context.WithValue(ctx, userKey, u)
		res, err := s.processScan(uctx, scan, p)
		if err != nil {
			s.log.Warn("receipt retry", "scan", scan.ID, "err", err)
			continue
		}
		s.log.Info("receipt retry", "scan", scan.ID, "status", res.Status)
	}
}

// ---- handlers ----

func (s *Server) handleReceiptProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, fiscal.Providers())
}

func (s *Server) handleScanReceipt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL       string `json:"url"`
		Text      string `json:"text"`
		AccountID int64  `json:"account_id"`
		Source    string `json:"source"`
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
	} else {
		// a form, urlencoded or multipart (iPhone Shortcuts sends multipart)
		in.URL, in.Source = r.FormValue("url"), r.FormValue("source")
		in.AccountID, _ = strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	}
	if in.URL == "" {
		in.URL = in.Text
	}
	if strings.TrimSpace(in.URL) == "" {
		writeErr(w, http.StatusBadRequest, "send the receipt link as url")
		return
	}
	if in.Source == "" {
		in.Source = "api"
	}
	res, err := s.scanReceipt(r.Context(), in.URL, scanOptions{Source: in.Source, AccountID: in.AccountID})
	if err != nil {
		s.fail(w, err, "scan receipt")
		return
	}
	code := http.StatusOK
	switch res.Status {
	case "booked", "linked", "partial":
		code = http.StatusCreated
	case "unsupported", "invalid":
		code = http.StatusUnprocessableEntity
	}
	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(scanText(res)))
		return
	}
	writeJSON(w, code, res)
}

// scanText is a one-line answer for an iPhone Shortcut notification.
func scanText(res *scanResult) string {
	what := ""
	if r := res.Receipt; r != nil {
		what = majorString(r.Total, r.Currency)
		if p := payeeFor(r); p != "" {
			what = p + " · " + what
		}
	}
	switch res.Status {
	case "booked":
		line := "✓ " + what
		if t := res.Transaction; t != nil && t.CategoryName != "" {
			line += " · " + t.CategoryName
		}
		return line
	case "linked":
		return "✓ " + what + " · linked to the one you typed"
	case "duplicate":
		return "Already scanned: " + what
	case "partial":
		return "✓ " + what + " booked · shop and items follow when the tax office answers"
	case "pending":
		return "Saved · the tax office is not answering, Pocket Money will retry"
	}
	return res.Message
}

func (s *Server) handleListReceipts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ReceiptFilter{UserID: actorID(r.Context()), From: q.Get("from"), To: q.Get("to"), Q: q.Get("q"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	f.MerchantID, _ = strconv.ParseInt(q.Get("merchant"), 10, 64)
	items, total, err := s.store.ListReceipts(r.Context(), f)
	if err != nil {
		s.fail(w, err, "receipts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// receiptFor returns a receipt the caller may see: their own, or the one
// behind a transaction they can see.
func (s *Server) receiptFor(w http.ResponseWriter, r *http.Request, write bool) *store.Receipt {
	ctx := r.Context()
	rec, err := s.store.GetReceipt(ctx, idParam(r, "id"))
	if err == nil && rec.UserID != actorID(ctx) {
		allowed := false
		if !write && rec.TransactionID != nil {
			if t, err := s.store.GetTransaction(ctx, *rec.TransactionID); err == nil && s.canSee(ctx, t.AccountID) {
				allowed = true
			}
		}
		if !allowed {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		s.fail(w, err, "receipt")
		return nil
	}
	return rec
}

func (s *Server) handleGetReceipt(w http.ResponseWriter, r *http.Request) {
	if rec := s.receiptFor(w, r, false); rec != nil {
		s.withGuess(r.Context(), rec)
		writeJSON(w, http.StatusOK, rec)
	}
}

// withGuess adds what the receipt says today (after the person sorted items),
// so the page can offer it when the transaction has something else.
func (s *Server) withGuess(ctx context.Context, rec *store.Receipt) {
	if rec.UserID != actorID(ctx) {
		return
	}
	g := s.guessReceipt(ctx, rec)
	rec.Guess = map[string]any{"category_id": g.CategoryID, "reason": g.Reason, "description": g.Description}
}

func (s *Server) handleTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	t := s.loadTx(w, r, false)
	if t == nil {
		return
	}
	rec, err := s.store.ReceiptForTransaction(r.Context(), t.ID)
	if err != nil {
		s.fail(w, err, "receipt")
		return
	}
	s.withGuess(r.Context(), rec)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleDeleteReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec := s.receiptFor(w, r, true)
	if rec == nil {
		return
	}
	if queryBool(r, "transaction") && rec.TransactionID != nil {
		if t, err := s.store.GetTransaction(ctx, *rec.TransactionID); err == nil && s.canWrite(ctx, t.AccountID) && t.Source == "receipt" {
			before := snapTx(t)
			if err := s.store.DeleteTransaction(ctx, t.ID); err != nil {
				s.fail(w, err, "delete transaction")
				return
			}
			s.auditTx(withAuditSource(ctx, "receipt"), before, nil)
			s.emitTx(ctx, "transaction.deleted", t)
		}
	}
	if err := s.store.DeleteReceipt(ctx, actorID(ctx), rec.ID); err != nil {
		s.fail(w, err, "delete receipt")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBookReceipt books a receipt that has no transaction (any more).
func (s *Server) handleBookReceipt(w http.ResponseWriter, r *http.Request) {
	rec := s.receiptFor(w, r, true)
	if rec == nil {
		return
	}
	if rec.TransactionID != nil {
		writeErr(w, http.StatusConflict, "this receipt is already booked")
		return
	}
	var in struct {
		AccountID int64 `json:"account_id"`
	}
	_ = decode(r, &in)
	var hint *int64
	if in.AccountID > 0 {
		hint = &in.AccountID
	}
	t, how, err := s.bookReceipt(r.Context(), rec, hint)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"transaction": t, "message": how})
}

// handleSplitReceipt splits the receipt's transaction by its items' group
// categories (or joins it back into one with ?undo=1).
func (s *Server) handleSplitReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec := s.receiptFor(w, r, false)
	if rec == nil {
		return
	}
	if rec.TransactionID == nil {
		writeErr(w, http.StatusBadRequest, "this receipt is not booked")
		return
	}
	t, err := s.store.GetTransaction(ctx, *rec.TransactionID)
	if err != nil || !s.canWrite(ctx, t.AccountID) {
		writeErr(w, http.StatusForbidden, "you cannot change that transaction")
		return
	}
	display := t.Amount
	if t.ForeignAmount > 0 {
		display = t.ForeignAmount
	}
	main := t.CategoryID
	var parts []splitIn
	if !queryBool(r, "undo") {
		if display != rec.Total || (t.ForeignAmount > 0 && t.ForeignCurrency != rec.Currency) || (t.ForeignAmount == 0 && t.Currency != rec.Currency) {
			writeErr(w, http.StatusBadRequest, "the transaction amount differs from the receipt, split it by hand")
			return
		}
		if parts = receiptSplits(rec, main); parts == nil {
			writeErr(w, http.StatusBadRequest, "the items do not span more than one category: give the product groups categories first")
			return
		}
	} else {
		parts = []splitIn{{CategoryID: main, Amount: display}}
	}
	updated := s.updateTxFrom(ctx, t, &txIn{Splits: &parts})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleListScans(w http.ResponseWriter, r *http.Request) {
	items, total, err := s.store.ListScans(r.Context(), actorID(r.Context()), r.URL.Query().Get("status"), queryInt(r, "limit"), queryInt(r, "offset"))
	if err != nil {
		s.fail(w, err, "scans")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleRetryScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scan, err := s.store.GetScan(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "scan")
		return
	}
	p := fiscal.ByID(scan.Provider)
	if p == nil {
		writeErr(w, http.StatusBadRequest, fiscal.ErrUnsupported.Error())
		return
	}
	if scan.Status == "failed" || scan.Status == "invalid" {
		scan.Attempts = 0
	}
	res, err := s.processScan(ctx, scan, p)
	if err != nil {
		s.fail(w, err, "retry scan")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleScanRaw(w http.ResponseWriter, r *http.Request) {
	scan, err := s.store.GetScan(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "scan")
		return
	}
	ct := "text/plain; charset=utf-8"
	if strings.HasPrefix(strings.TrimSpace(scan.Raw), "{") {
		ct = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write([]byte(scan.Raw))
}

// receiptSettingsOut adds what the page shows next to the switches: which
// AI would sort products, and the shop rules in force (defaults included).
func (s *Server) receiptSettingsOut(ctx context.Context) map[string]any {
	rs := s.store.GetReceiptSettings(ctx, actorID(ctx))
	rules, defaults := s.shopRules(ctx, rs)
	client, detail := s.receiptAI(ctx)
	unsorted, _ := s.store.ProductsToSort(ctx, actorID(ctx), nil, 10000)
	return map[string]any{"card_account_id": rs.CardAccountID, "cash_account_id": rs.CashAccountID, "split_items": rs.SplitItems, "attach_manual": rs.AttachManual,
		"ai_items": rs.AIItems, "own_words": rs.OwnWords, "geocode": rs.Geocode, "shop_rules": rules, "shop_rules_default": defaults,
		"ai_ready": client != nil, "ai_detail": detail, "unsorted_products": len(unsorted),
		"group_job": rs.GroupJob, "group_job_hour": rs.GroupJobHour, "group_job_notify": rs.GroupJobNotify, "group_on_scan": rs.GroupOnScan}
}

func (s *Server) handleGetReceiptSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.receiptSettingsOut(r.Context()))
}

func (s *Server) handlePutReceiptSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in store.ReceiptSettings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cur := s.store.GetReceiptSettings(ctx, actorID(ctx))
	// only what was sent changes
	keep := func(k string, dst *bool, v bool) {
		if !keys[k] {
			*dst = v
		}
	}
	keep("split_items", &in.SplitItems, cur.SplitItems)
	keep("attach_manual", &in.AttachManual, cur.AttachManual)
	keep("ai_items", &in.AIItems, cur.AIItems)
	keep("own_words", &in.OwnWords, cur.OwnWords)
	keep("geocode", &in.Geocode, cur.Geocode)
	keep("group_job_notify", &in.GroupJobNotify, cur.GroupJobNotify)
	keep("group_on_scan", &in.GroupOnScan, cur.GroupOnScan)
	if !keys["group_job"] {
		in.GroupJob = cur.GroupJob
	} else if in.GroupJob != "off" && in.GroupJob != "keywords" && in.GroupJob != "ai" {
		writeErr(w, http.StatusBadRequest, "group_job must be off, keywords or ai")
		return
	}
	if !keys["group_job_hour"] {
		in.GroupJobHour = cur.GroupJobHour
	} else if in.GroupJobHour < 0 || in.GroupJobHour > 23 {
		writeErr(w, http.StatusBadRequest, "group_job_hour must be 0–23")
		return
	}
	if !keys["card_account_id"] {
		in.CardAccountID = cur.CardAccountID
	}
	if !keys["cash_account_id"] {
		in.CashAccountID = cur.CashAccountID
	}
	if !keys["shop_rules"] {
		in.ShopRules = cur.ShopRules // unchanged
	} else {
		clean := []store.ShopRule{}
		for _, rule := range in.ShopRules {
			if strings.TrimSpace(rule.Keywords) == "" || rule.CategoryID <= 0 {
				continue
			}
			if _, err := s.store.GetCategory(ctx, actorID(ctx), rule.CategoryID); err != nil {
				writeErr(w, http.StatusBadRequest, "unknown category in a shop rule")
				return
			}
			clean = append(clean, store.ShopRule{Keywords: strings.TrimSpace(rule.Keywords), CategoryID: rule.CategoryID})
		}
		in.ShopRules = clean
	}
	if keys["reset_shop_rules"] { // back to the defaults
		in.ShopRules = nil
	}
	for _, id := range []*int64{in.CardAccountID, in.CashAccountID} {
		if id != nil && *id > 0 && !s.canWrite(ctx, *id) {
			writeErr(w, http.StatusBadRequest, "you cannot book on that account")
			return
		}
	}
	in.CardAccountID, in.CashAccountID = idOrNil(in.CardAccountID), idOrNil(in.CashAccountID)
	if err := s.store.SaveReceiptSettings(ctx, actorID(ctx), in); err != nil {
		s.fail(w, err, "receipt settings")
		return
	}
	writeJSON(w, http.StatusOK, s.receiptSettingsOut(ctx))
}

// ---- merchants ----

func (s *Server) handleListMerchants(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListMerchants(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "merchants")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpdateMerchant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := s.store.GetMerchant(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "merchant")
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Description *string `json:"description"`
		CategoryID  *int64  `json:"category_id"`
		AccountID   *int64  `json:"account_id"`
		Apply       bool    `json:"apply"` // also rename/categorise transactions already booked from its receipts
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	oldName := m.DisplayName
	if in.DisplayName != nil {
		m.DisplayName = strings.TrimSpace(*in.DisplayName)
		if m.DisplayName == "" {
			m.DisplayName = fiscal.MerchantName(m.Name)
		}
	}
	if in.Description != nil {
		m.Description = strings.TrimSpace(*in.Description)
	}
	if in.CategoryID != nil {
		m.CategoryID = idOrNil(in.CategoryID)
		m.CategorySource = "you"
		if m.CategoryID == nil {
			m.CategorySource = ""
		}
		if m.CategoryID != nil {
			if _, err := s.store.GetCategory(ctx, actorID(ctx), *m.CategoryID); err != nil {
				writeErr(w, http.StatusBadRequest, "unknown category")
				return
			}
		}
	}
	if in.AccountID != nil {
		m.AccountID = idOrNil(in.AccountID)
		if m.AccountID != nil && !s.canWrite(ctx, *m.AccountID) {
			writeErr(w, http.StatusBadRequest, "you cannot book on that account")
			return
		}
	}
	if err := s.store.UpdateMerchant(ctx, m); err != nil {
		s.fail(w, err, "merchant")
		return
	}
	updated := 0
	if in.Apply {
		recs, _, _ := s.store.ListReceipts(ctx, store.ReceiptFilter{UserID: actorID(ctx), MerchantID: m.ID, Limit: 500})
		for _, rec := range recs {
			if rec.TransactionID == nil {
				continue
			}
			t, err := s.store.GetTransaction(ctx, *rec.TransactionID)
			if err != nil || t.Source != "receipt" || !s.canWrite(ctx, t.AccountID) {
				continue
			}
			var tin txIn
			changed := false
			if t.Payee == oldName && m.DisplayName != oldName {
				p := m.DisplayName
				tin.Payee = &p
				if t.Description == oldName {
					tin.Description = &p
				} else if t.Description == "Refund · "+oldName {
					d := "Refund · " + p
					tin.Description = &d
				}
				changed = true
			}
			if m.CategoryID != nil && len(t.Splits) == 0 && (t.CategoryID == nil || *t.CategoryID != *m.CategoryID) {
				tin.CategoryID = m.CategoryID
				changed = true
			}
			if changed {
				s.updateTxFrom(ctx, t, &tin)
				updated++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"merchant": m, "updated": updated})
}
