package server

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// fakeSUF answers like the Republika Srpska tax office, from receipts keyed
// by their counter; offline=true makes it unreachable.
type fakeSUF struct {
	offline  atomic.Bool
	late     atomic.Bool // receipts it does not know are "not yet" (404), not bad requests
	geocoded atomic.Int32
	receipts map[uint32]string
}

func (f *fakeSUF) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.offline.Load() {
		return nil, errors.New("dial tcp: no route to host")
	}
	if r.URL.Host == "nominatim.openstreetmap.org" {
		f.geocoded.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"lat":"44.7722","lon":"17.1910"}]`)), Header: http.Header{}, Request: r}, nil
	}
	vl, _ := base64.StdEncoding.DecodeString(r.URL.Query().Get("vl"))
	body, ok := f.receipts[binary.LittleEndian.Uint32(vl[17:21])]
	code := 200
	if !ok || r.Header.Get("Accept") != "application/json" {
		code, body = 400, ""
		if f.late.Load() {
			code = 404
		}
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
}

var receiptAt = time.Date(2026, 10, 20, 8, 24, 16, 0, time.UTC) // 10:24 in Banja Luka (summer time)

func sufTestLink(counter uint32, total float64) string {
	return sufLinkAt(counter, total, receiptAt)
}

func sufLinkAt(counter uint32, total float64, at time.Time) string {
	b := make([]byte, 60)
	b[0] = 3
	copy(b[1:9], "TEST0001")
	copy(b[9:17], "TEST0001")
	binary.LittleEndian.PutUint32(b[17:21], counter)
	binary.LittleEndian.PutUint32(b[21:25], counter)
	binary.LittleEndian.PutUint64(b[25:33], uint64(total*10000+0.5))
	binary.BigEndian.PutUint64(b[33:41], uint64(at.UnixMilli()))
	return "https://suf.poreskaupravars.org/v/?vl=" + base64.StdEncoding.EncodeToString(b)
}

func sufTestJSON(counter uint32, payment int, items ...[3]any) string {
	return sufShopJSON(counter, payment, `\"PRIMJER MARKET\" d.o.o. Banja Luka`, "4400000000001", items...)
}

func sufShopJSON(counter uint32, payment int, business, taxID string, items ...[3]any) string {
	var lines []string
	var total float64
	for _, it := range items {
		q, p := it[1].(float64), it[2].(float64)
		lines = append(lines, fmt.Sprintf(`{"name":%q,"quantity":%v,"unitPrice":%v,"totalPrice":%v,"gtin":"1"}`, it[0], q, p, q*p))
		total += q * p
	}
	return fmt.Sprintf(`{"invoiceRequest":{"taxId":%q,"businessName":"%s","locationName":"PJ Centar","address":"Glavna 1","city":"Banja Luka","cashier":"7","requestedBy":"TEST0001","invoiceType":0,"transactionType":0,
"payments":[{"paymentType":%d,"amount":%.2f}],"items":[%s]},
"invoiceResult":{"totalAmount":%.2f,"invoiceNumber":"TEST0001-TEST0001-%d","signedBy":"TEST0001","sdcTime":%q},"journal":"","isValid":true}`,
		taxID, business, payment, total, strings.Join(lines, ","), total, counter, receiptAt.Format(time.RFC3339))
}

type scanOut struct {
	Status      string             `json:"status"`
	Message     string             `json:"message"`
	Scan        store.ReceiptScan  `json:"scan"`
	Receipt     *store.Receipt     `json:"receipt"`
	Transaction *store.Transaction `json:"transaction"`
}

func TestReceiptScanning(t *testing.T) {
	fake := &fakeSUF{receipts: map[uint32]string{
		1: sufTestJSON(1, 2, [3]any{"MLIJEKO 2.8% 1L/KO", 2.0, 2.15}, [3]any{"Jabuka BIO/KG", 1.5, 5.0}),
		2: sufTestJSON(2, 2, [3]any{"MLIJEKO 2.8% 1L/KO", 1.0, 2.25}),
		3: sufTestJSON(3, 2, [3]any{"Hljeb bijeli/KO", 1.0, 1.5}),
		4: sufTestJSON(4, 1, [3]any{"Kafa mljevena 250 g/KO", 1.0, 9.49}),
	}}
	receiptTransport = fake
	defer func() { receiptTransport = nil }()

	c := newTestServer(t)
	var bank, cash store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &bank)
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "Novčanik", "currency": "BAM", "kind": "cash"}, &cash)
	var food store.Category
	c.do("POST", "/api/v1/categories", map[string]any{"name": "Hrana"}, &food)
	var g struct {
		Group store.ProductGroup `json:"group"`
	}
	if c.do("POST", "/api/v1/products/groups", map[string]any{"name": "Milk", "icon": "🥛", "keywords": "mlijeko, mleko", "category_id": food.ID}, &g) != 201 {
		t.Fatal("create group")
	}
	c.do("PUT", "/api/v1/receipts/settings", map[string]any{"card_account_id": bank.ID, "attach_manual": true}, nil)

	// a preview books nothing and logs nothing
	var peek peekOut
	c.do("POST", "/api/v1/receipts/peek", map[string]any{"url": sufTestLink(1, 11.80)}, &peek)
	if !peek.Supported || !peek.Complete || peek.Shop != "Primjer Market" || peek.Total != 1180 || len(peek.Items) != 2 || peek.Items[0].Category != "Hrana" || peek.AccountName != "UniCredit" || peek.Existing != nil {
		t.Fatalf("peek %+v", peek)
	}
	var promo peekOut
	c.do("POST", "/api/v1/receipts/peek", map[string]any{"url": "https://centrumshop.ba/"}, &promo)
	if promo.Supported {
		t.Fatal("a shop's own QR code previewed as a receipt")
	}

	// a card receipt: booked on the card account with shop, time and items
	var res scanOut
	if code := c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(1, 11.80), "source": "test"}, &res); code != 201 || res.Status != "booked" {
		t.Fatalf("scan: %d %+v", code, res)
	}
	tx := res.Transaction
	if tx.Amount != 1180 || tx.AccountID != bank.ID || tx.Payee != "Primjer Market" || tx.Date != "2026-10-20" || tx.Time != "10:24" || tx.Source != "receipt" || tx.ReceiptID == nil {
		t.Fatalf("transaction %+v", tx)
	}
	if res.Receipt == nil || len(res.Receipt.Items) != 2 || res.Receipt.Items[0].GroupName != "Milk" || res.Receipt.Items[1].GroupID != nil {
		t.Fatalf("receipt %+v", res.Receipt)
	}
	var again peekOut
	c.do("POST", "/api/v1/receipts/peek", map[string]any{"url": sufTestLink(1, 11.80)}, &again)
	if again.Existing == nil || again.Existing.TransactionID == nil || *again.Existing.TransactionID != tx.ID {
		t.Fatalf("peek of a stored receipt: %+v", again.Existing)
	}
	// the same QR again: a duplicate, nothing new booked
	var dup scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(1, 11.80)}, &dup)
	if dup.Status != "duplicate" || dup.Transaction == nil || dup.Transaction.ID != tx.ID {
		t.Fatalf("duplicate: %+v", dup)
	}

	// offline: the QR alone books the total, the retry fills shop and items
	fake.offline.Store(true)
	var off scanOut
	if c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(2, 2.25)}, &off); off.Status != "partial" || off.Transaction == nil || off.Transaction.Amount != 225 {
		t.Fatalf("offline: %+v", off)
	}
	if off.Transaction.Payee != "Primjer Market" { // the same fiscal device as receipt 1
		t.Errorf("offline payee %q", off.Transaction.Payee)
	}
	fake.offline.Store(false)
	var retried scanOut
	c.do("POST", fmt.Sprintf("/api/v1/receipts/scans/%d/retry", off.Scan.ID), nil, &retried)
	if retried.Status != "booked" || retried.Receipt == nil || !retried.Receipt.Complete || len(retried.Receipt.Items) != 1 {
		t.Fatalf("retry: %+v", retried)
	}
	if retried.Transaction == nil || retried.Transaction.ID != off.Transaction.ID || !strings.Contains(retried.Transaction.Notes, "1 items") {
		t.Fatalf("retry transaction: %+v", retried.Transaction)
	}

	// a hand-typed 1,50 the same day: the receipt links to it instead of booking twice
	var manual store.Transaction
	c.do("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": bank.ID, "amount": 150, "date": "2026-10-20", "description": "hljeb"}, &manual)
	var linked scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(3, 1.50)}, &linked)
	if linked.Status != "linked" || linked.Transaction == nil || linked.Transaction.ID != manual.ID || linked.Transaction.Payee != "Primjer Market" {
		t.Fatalf("linked: %+v", linked)
	}

	// cash goes to the cash account
	var cashRes scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufTestLink(4, 9.49)}, &cashRes)
	if cashRes.Transaction == nil || cashRes.Transaction.AccountID != cash.ID {
		t.Fatalf("cash: %+v", cashRes)
	}

	// not a receipt
	var bad scanOut
	if c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": "https://example.com/x"}, &bad) != 422 || bad.Status != "unsupported" {
		t.Fatalf("unsupported: %+v", bad)
	}

	// milk: two purchases, three litres, filterable in transactions
	var page struct {
		Items []store.Transaction `json:"items"`
	}
	c.do("GET", fmt.Sprintf("/api/v1/transactions?product_group=%d", g.Group.ID), nil, &page)
	if len(page.Items) != 2 {
		t.Fatalf("milk transactions: %d", len(page.Items))
	}
	c.do("GET", "/api/v1/transactions?q=jabuka", nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != tx.ID {
		t.Fatalf("item search: %+v", page.Items)
	}
	var sum store.ItemStats
	c.do("GET", fmt.Sprintf("/api/v1/products/summary?product_group=%d", g.Group.ID), nil, &sum)
	if sum.Purchases != 2 || sum.Quantity != 3 || sum.Spent != 655 || sum.Unit != "ko" {
		t.Fatalf("milk summary %+v", sum)
	}
	var groups []store.ItemStats
	c.do("GET", "/api/v1/products/groups", nil, &groups)
	if len(groups) != 2 || groups[0].Name != "Milk" {
		t.Fatalf("groups %+v", groups)
	}
	// a new group picks up products already bought
	var g2 struct {
		Moved int `json:"moved"`
	}
	c.do("POST", "/api/v1/products/groups", map[string]any{"name": "Fruit", "keywords": "jabuka"}, &g2)
	if g2.Moved != 1 {
		t.Fatalf("regroup moved %d", g2.Moved)
	}

	// split receipt 1 by category: milk → Hrana, apples → the main (none)
	var split store.Transaction
	if c.do("POST", fmt.Sprintf("/api/v1/receipts/%d/split", *tx.ReceiptID), nil, &split) != 200 || len(split.Splits) != 2 {
		t.Fatalf("split: %+v", split.Splits)
	}

	// naming the shop renames what was booked from it
	var merchants []store.Merchant
	c.do("GET", "/api/v1/merchants", nil, &merchants)
	if len(merchants) != 1 || merchants[0].Receipts != 4 {
		t.Fatalf("merchants %+v", merchants)
	}
	var mu struct {
		Updated int `json:"updated"`
	}
	c.do("PUT", fmt.Sprintf("/api/v1/merchants/%d", merchants[0].ID), map[string]any{"display_name": "Primjer", "apply": true}, &mu)
	if mu.Updated != 3 { // the hand-typed one keeps its name
		t.Fatalf("merchant apply updated %d", mu.Updated)
	}

	// the scan log has every scan, newest first
	var log struct {
		Items []store.ReceiptScan `json:"items"`
		Total int                 `json:"total"`
	}
	c.do("GET", "/api/v1/receipts/scans", nil, &log)
	if log.Total != 6 || log.Items[0].Status != "unsupported" {
		t.Fatalf("log %d %+v", log.Total, log.Items[0])
	}

	// places: the receipt-booked purchases carry the shop's pin (none here: the fake geocoder only answers in the inference test)
	var places struct {
		Items []placeItem `json:"items"`
	}
	if c.do("GET", "/api/v1/places?from=2026-10-01&to=2026-10-31", nil, &places) != 200 {
		t.Fatal("places")
	}
	// quick add takes a receipt link too
	var quick []store.Transaction
	fake.receipts[5] = sufTestJSON(5, 2, [3]any{"Kesa/KO", 1.0, 0.1})
	if c.do("POST", "/api/v1/quick", map[string]any{"text": sufTestLink(5, 0.10)}, &quick) != 201 || len(quick) != 1 || quick[0].Amount != 10 {
		t.Fatalf("quick: %+v", quick)
	}
}

// A receipt the tax office does not have yet waits, booking nothing, until
// a later check finds it; or the person books it from the QR code or rejects it.
func TestReceiptWaitsForTaxOffice(t *testing.T) {
	fake := &fakeSUF{receipts: map[uint32]string{}}
	fake.late.Store(true)
	receiptTransport = fake
	defer func() { receiptTransport = nil }()

	c := newTestServer(t)
	var bank store.Account
	c.do("POST", "/api/v1/accounts", map[string]any{"name": "UniCredit", "currency": "BAM"}, &bank)
	at := time.Now().Add(-30 * time.Minute) // past the few minutes a scan waits for it on the spot
	link := sufLinkAt(21, 7.40, at)

	var peek peekOut
	c.do("POST", "/api/v1/receipts/peek", map[string]any{"url": link}, &peek)
	if !peek.Supported || !peek.NotYet || peek.Total != 740 || peek.WaitingSince != "" {
		t.Fatalf("peek %+v", peek)
	}
	var res scanOut
	if code := c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": link, "mode": "later"}, &res); code != 202 || res.Status != "waiting" || res.Transaction != nil || res.Scan.Total != 740 || res.Scan.NextTryAt == "" {
		t.Fatalf("save for later: %d %+v", code, res)
	}
	id := res.Scan.ID
	c.do("POST", "/api/v1/receipts/peek", map[string]any{"url": link}, &peek)
	if !peek.NotYet || peek.WaitingSince == "" {
		t.Fatalf("peek of a waiting receipt %+v", peek)
	}
	// scanned again: the same scan, not a second one
	var again scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": link}, &again)
	if again.Status != "waiting" || again.Scan.ID != id || again.Scan.Attempts != 1 {
		t.Fatalf("scanned again: %+v", again)
	}
	var list struct {
		Items []store.ReceiptScan `json:"items"`
		Total int                 `json:"total"`
	}
	c.do("GET", "/api/v1/receipts/scans?status=waiting,missing", nil, &list)
	if list.Total != 1 || list.Items[0].Total != 740 || list.Items[0].IssuedAt == "" {
		t.Fatalf("waiting list %+v", list)
	}
	var txs struct {
		Items []store.Transaction `json:"items"`
	}
	c.do("GET", "/api/v1/transactions", nil, &txs)
	if len(txs.Items) != 0 {
		t.Fatalf("a waiting receipt was booked: %+v", txs.Items)
	}

	// the shop reports it; the background check books it with shop and items
	fake.receipts[21] = sufTestJSON(21, 2, [3]any{"Hljeb bijeli/KO", 2.0, 3.7})
	c.srv.retryReceiptScans(context.Background()) // not due yet
	if sc, _ := c.srv.store.GetScan(context.Background(), res.Scan.UserID, id); sc.Status != "waiting" {
		t.Fatalf("checked before it was due: %s", sc.Status)
	}
	if _, err := c.srv.store.DB.Exec(`UPDATE receipt_scans SET next_try_at = '2000-01-01T00:00:00Z' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	c.srv.retryReceiptScans(context.Background())
	sc, _ := c.srv.store.GetScan(context.Background(), res.Scan.UserID, id)
	if sc.Status != "done" || sc.ReceiptID == nil || sc.NextTryAt != "" {
		t.Fatalf("after the tax office has it: %+v", sc)
	}
	c.do("GET", "/api/v1/transactions", nil, &txs)
	if len(txs.Items) != 1 || txs.Items[0].Amount != 740 || txs.Items[0].Payee != "Primjer Market" {
		t.Fatalf("booked %+v", txs.Items)
	}

	// rejected: nothing booked, never asked about again
	var rej scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufLinkAt(22, 3.00, at)}, &rej)
	if rej.Status != "waiting" {
		t.Fatalf("second receipt: %+v", rej)
	}
	if code := c.do("POST", fmt.Sprintf("/api/v1/receipts/scans/%d/reject", rej.Scan.ID), nil, &rej); code != 200 || rej.Status != "rejected" {
		t.Fatalf("reject: %d %+v", code, rej)
	}
	if due, _ := c.srv.store.DueScans(context.Background(), "9999", 10); len(due) != 0 {
		t.Fatalf("a rejected scan is still retried: %+v", due)
	}
	if code := c.do("POST", fmt.Sprintf("/api/v1/receipts/scans/%d/book", rej.Scan.ID), nil, nil); code != 409 {
		t.Fatalf("booked a rejected scan: %d", code)
	}

	// booked from the QR code: total and time now, shop and items later
	var wait, now scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufLinkAt(23, 12.50, at)}, &wait)
	if code := c.do("POST", fmt.Sprintf("/api/v1/receipts/scans/%d/book", wait.Scan.ID), nil, &now); code != 200 || now.Status != "partial" || now.Transaction == nil || now.Transaction.Amount != 1250 || now.Scan.NextTryAt == "" {
		t.Fatalf("book from the QR: %d %+v", code, now)
	}

	// gives up after the last try: missing, still offered to book or reject
	var gone scanOut
	c.do("POST", "/api/v1/receipts/scan", map[string]any{"url": sufLinkAt(24, 1.00, at)}, &gone)
	if _, err := c.srv.store.DB.Exec(`UPDATE receipt_scans SET attempts = ?, next_try_at = '2000-01-01T00:00:00Z' WHERE id = ?`, len(waitBackoff), gone.Scan.ID); err != nil {
		t.Fatal(err)
	}
	c.srv.retryReceiptScans(context.Background())
	if sc, _ := c.srv.store.GetScan(context.Background(), gone.Scan.UserID, gone.Scan.ID); sc.Status != "missing" || sc.NextTryAt != "" {
		t.Fatalf("after the last try: %+v", sc)
	}
}
