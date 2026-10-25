package fiscal

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// sufProvider reads the SUF verification service used by Serbia and, with
// the same software, Republika Srpska: https://<host>/v/?vl=<base64>. Asked
// for JSON, the page returns the whole receipt.
type sufProvider struct {
	id, country, name, host, currency, zone string
}

func (p sufProvider) ID() string      { return p.id }
func (p sufProvider) Country() string { return p.country }
func (p sufProvider) Name() string    { return p.name }

func (p sufProvider) Match(u *url.URL) bool {
	return hostIs(u, p.host) && rawParam(u.RawQuery, "vl") != ""
}

func (p sufProvider) vl(u *url.URL) string {
	// a "+" printed unescaped is a "+", never a space
	return strings.ReplaceAll(rawParam(u.RawQuery, "vl"), " ", "+")
}

func (p sufProvider) loc() *time.Location {
	if l, err := time.LoadLocation(p.zone); err == nil {
		return l
	}
	return time.UTC
}

// Decode reads the binary verification payload: version, the requesting and
// signing device ids, the counters, the total (in 1/10000) and the time. The
// rest is encrypted for the tax office.
func (p sufProvider) Decode(u *url.URL) (*Receipt, error) {
	b, err := base64.StdEncoding.DecodeString(p.vl(u))
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(p.vl(u), "=")); err != nil {
			return nil, fmt.Errorf("unreadable receipt code: %w", err)
		}
	}
	if len(b) < 43 || b[0] != 3 {
		return nil, nil // another layout version: only the online check can read it
	}
	requested := strings.TrimSpace(string(b[1:9]))
	signed := strings.TrimSpace(string(b[9:17]))
	total := binary.LittleEndian.Uint32(b[17:21])
	amount := binary.LittleEndian.Uint64(b[25:33])
	ms := binary.BigEndian.Uint64(b[33:41])
	r := &Receipt{Provider: p.id, Country: p.country, Currency: p.currency,
		InvoiceNumber: fmt.Sprintf("%s-%s-%d", requested, signed, total),
		Total:         int64((amount + 50) / 100), // 1/10000 → 1/100
		IssuedAt:      time.UnixMilli(int64(ms)).In(p.loc()),
		Device:        signed,
		InvoiceType:   sufInvoiceType(int(b[41])),
		Kind:          sufKind(int(b[42])),
	}
	r.Key = r.InvoiceNumber
	return r, nil
}

func (p sufProvider) Fetch(ctx context.Context, hc *http.Client, u *url.URL) (*Receipt, []byte, error) {
	target := fmt.Sprintf("https://%s/v/?vl=%s", p.host, url.QueryEscape(p.vl(u)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, nil, err
	}
	switch {
	case res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusNotFound:
		return nil, raw, ErrInvalid
	case res.StatusCode != http.StatusOK:
		return nil, raw, fmt.Errorf("tax office answered HTTP %d", res.StatusCode)
	}
	rec, err := p.Parse(u, raw)
	return rec, raw, err
}

type sufResponse struct {
	InvoiceRequest struct {
		TaxID                  string `json:"taxId"`
		BusinessName           string `json:"businessName"`
		LocationName           string `json:"locationName"`
		Address                string `json:"address"`
		City                   string `json:"city"`
		Cashier                string `json:"cashier"`
		RequestedBy            string `json:"requestedBy"`
		ReferentDocumentNumber string `json:"referentDocumentNumber"`
		InvoiceType            int    `json:"invoiceType"`
		TransactionType        int    `json:"transactionType"`
		Payments               []struct {
			PaymentType int     `json:"paymentType"`
			Amount      float64 `json:"amount"`
		} `json:"payments"`
		Items []struct {
			Name       string  `json:"name"`
			Quantity   float64 `json:"quantity"`
			UnitPrice  float64 `json:"unitPrice"`
			TotalPrice float64 `json:"totalPrice"`
			GTIN       string  `json:"gtin"`
		} `json:"items"`
	} `json:"invoiceRequest"`
	InvoiceResult struct {
		TotalAmount   float64 `json:"totalAmount"`
		InvoiceNumber string  `json:"invoiceNumber"`
		SignedBy      string  `json:"signedBy"`
		SdcTime       string  `json:"sdcTime"`
	} `json:"invoiceResult"`
	Journal string `json:"journal"`
	IsValid *bool  `json:"isValid"`
}

var (
	sufTaxTotal = regexp.MustCompile(`(?m)^(?:Укупан износ пореза|Ukupan iznos poreza):\s*([\d.,]+)\s*$`)
	sufItemTax  = regexp.MustCompile(`\s\(([^()]{1,3})\)\s*$`)
	sufUnit     = regexp.MustCompile(`(?i)/\s*([a-zа-я]{1,4})\s*$`)
)

func (p sufProvider) Parse(u *url.URL, raw []byte) (*Receipt, error) {
	var d sufResponse
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("unexpected answer from the tax office: %w", err)
	}
	if d.IsValid != nil && !*d.IsValid {
		return nil, ErrInvalid
	}
	q, res := d.InvoiceRequest, d.InvoiceResult
	if res.InvoiceNumber == "" {
		return nil, fmt.Errorf("unexpected answer from the tax office: no receipt number")
	}
	r := &Receipt{Provider: p.id, Country: p.country, Currency: p.currency,
		Key: res.InvoiceNumber, InvoiceNumber: res.InvoiceNumber,
		Total:   minor(res.TotalAmount),
		Cashier: strings.TrimSpace(q.Cashier), Device: strings.TrimSpace(res.SignedBy),
		Kind: sufKind(q.TransactionType), InvoiceType: sufInvoiceType(q.InvoiceType),
		Referent: strings.TrimSpace(q.ReferentDocumentNumber),
		Journal:  d.Journal, Complete: true, HasItems: true,
		Merchant: Merchant{TaxID: strings.TrimSpace(q.TaxID), Name: strings.TrimSpace(q.BusinessName),
			Location: strings.TrimSpace(q.LocationName), Address: strings.TrimSpace(q.Address), City: strings.TrimSpace(q.City)},
	}
	if t, err := time.Parse(time.RFC3339Nano, res.SdcTime); err == nil {
		r.IssuedAt = t.In(p.loc())
	}
	for _, pm := range q.Payments {
		r.Payments = append(r.Payments, Payment{Method: sufPayment(pm.PaymentType), Amount: minor(pm.Amount)})
	}
	// the journal shows each item's tax label: "KEFIR 2.8%/KO (Е)"
	labels := map[string]string{}
	for _, line := range strings.Split(d.Journal, "\n") {
		line = strings.TrimRight(line, " ")
		if m := sufItemTax.FindStringSubmatch(line); m != nil {
			labels[strings.TrimSpace(line[:len(line)-len(m[0])])] = m[1]
		}
	}
	for _, it := range q.Items {
		name := strings.TrimSpace(it.Name)
		item := Item{Name: name, GTIN: strings.TrimSpace(it.GTIN), Quantity: it.Quantity,
			UnitPrice: minor(it.UnitPrice), Total: minor(it.TotalPrice), TaxLabel: labels[name]}
		if m := sufUnit.FindStringSubmatch(name); m != nil {
			item.Unit = strings.ToLower(m[1])
		}
		r.Items = append(r.Items, item)
	}
	if m := sufTaxTotal.FindStringSubmatch(d.Journal); m != nil {
		r.TaxTotal = parseLocalAmount(m[1])
	}
	if r.InvoiceType == "copy" && r.Referent != "" {
		r.Key = r.Referent // a copy is the same purchase as its original
	}
	return r, nil
}

// parseLocalAmount reads "1.234,56" or "4,45".
func parseLocalAmount(s string) int64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ".", "")
	s = strings.ReplaceAll(s, ",", ".")
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return minor(f)
}

func sufPayment(t int) string {
	switch t {
	case 1:
		return "cash"
	case 2:
		return "card"
	case 3:
		return "check"
	case 4, 6:
		return "transfer"
	case 5:
		return "voucher"
	}
	return "other"
}

func sufInvoiceType(t int) string {
	switch t {
	case 1:
		return "proforma"
	case 2:
		return "copy"
	case 3:
		return "training"
	case 4:
		return "advance"
	}
	return "normal"
}

func sufKind(t int) string {
	if t == 1 {
		return "refund"
	}
	return "sale"
}
