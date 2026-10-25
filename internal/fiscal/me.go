package fiscal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// meProvider reads Montenegro's fiscal receipts. The QR link points at the
// public verification app, with everything in the fragment:
// https://mapr.tax.gov.me/ic/#/verify?iic=…&tin=…&crtd=2026-10-01T10:00:00+02:00&prc=12.50…
// The app itself posts iic, dateTimeCreated and tin to /ic/api/verifyInvoice.
type meProvider struct{}

func (meProvider) ID() string      { return "me" }
func (meProvider) Country() string { return "ME" }
func (meProvider) Name() string    { return "Montenegro" }

func meQuery(u *url.URL) string {
	if _, q, ok := strings.Cut(u.Fragment, "?"); ok {
		return q
	}
	return u.RawQuery
}

func meParam(u *url.URL, name string) string {
	return strings.ReplaceAll(rawParam(meQuery(u), name), " ", "+")
}

func (p meProvider) Match(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return (h == "mapr.tax.gov.me" || h == "efi.porezi.me") && meParam(u, "iic") != "" && meParam(u, "tin") != ""
}

func meLoc() *time.Location {
	if l, err := time.LoadLocation("Europe/Podgorica"); err == nil {
		return l
	}
	return time.UTC
}

func (p meProvider) Decode(u *url.URL) (*Receipt, error) {
	iic := strings.ToUpper(meParam(u, "iic"))
	r := &Receipt{Provider: p.ID(), Country: p.Country(), Currency: "EUR", Key: iic, InvoiceNumber: meParam(u, "ord"),
		Kind: "sale", InvoiceType: "normal", Merchant: Merchant{TaxID: meParam(u, "tin")}, Device: meParam(u, "cr")}
	if t, err := time.Parse(time.RFC3339, meParam(u, "crtd")); err == nil {
		r.IssuedAt = t.In(meLoc())
	}
	if f, err := strconv.ParseFloat(meParam(u, "prc"), 64); err == nil {
		r.Total = minor(f)
		if r.Total < 0 {
			r.Total, r.Kind = -r.Total, "refund"
		}
	}
	if bu := meParam(u, "bu"); bu != "" {
		r.Merchant.Location = bu
	}
	return r, nil
}

func (p meProvider) Fetch(ctx context.Context, hc *http.Client, u *url.URL) (*Receipt, []byte, error) {
	form := url.Values{"iic": {meParam(u, "iic")}, "dateTimeCreated": {meParam(u, "crtd")}, "tin": {meParam(u, "tin")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://mapr.tax.gov.me/ic/api/verifyInvoice", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	if res.StatusCode != http.StatusOK {
		return nil, raw, fmt.Errorf("tax office answered HTTP %d", res.StatusCode)
	}
	rec, err := p.Parse(u, raw)
	return rec, raw, err
}

type meResponse struct {
	IIC             string  `json:"iic"`
	DateTimeCreated string  `json:"dateTimeCreated"`
	InvoiceOrderNum any     `json:"invoiceOrderNumber"`
	BusinessUnit    string  `json:"businessUnit"`
	CashRegister    string  `json:"cashRegister"`
	OperatorCode    string  `json:"operatorCode"`
	TotalPrice      float64 `json:"totalPrice"`
	TotalVATAmount  float64 `json:"totalVATAmount"`
	IICReference    string  `json:"iicReference"`
	Seller          struct {
		IDNum   string `json:"idNum"`
		Name    string `json:"name"`
		Address string `json:"address"`
		Town    string `json:"town"`
	} `json:"seller"`
	Items []struct {
		Name              string  `json:"name"`
		Code              string  `json:"code"`
		Unit              string  `json:"unit"`
		Quantity          float64 `json:"quantity"`
		UnitPriceAfterVat float64 `json:"unitPriceAfterVat"`
		PriceAfterVat     float64 `json:"priceAfterVat"`
		VATRate           any     `json:"vatRate"`
	} `json:"items"`
	PaymentMethod []struct {
		Type   string  `json:"type"`
		Amount float64 `json:"amount"`
	} `json:"paymentMethod"`
}

func (p meProvider) Parse(u *url.URL, raw []byte) (*Receipt, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, ErrNotYet
	}
	var d meResponse
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("unexpected answer from the tax office: %w", err)
	}
	r, _ := p.Decode(u)
	r.Complete, r.HasItems = true, true
	if d.IIC != "" {
		r.Key = strings.ToUpper(d.IIC)
	}
	if d.InvoiceOrderNum != nil {
		r.InvoiceNumber = strings.TrimSuffix(fmt.Sprint(d.InvoiceOrderNum), ".0")
		if d.BusinessUnit != "" {
			r.InvoiceNumber = fmt.Sprintf("%s/%s/%s", r.InvoiceNumber, d.BusinessUnit, d.CashRegister)
		}
	}
	if t, err := time.Parse(time.RFC3339, d.DateTimeCreated); err == nil {
		r.IssuedAt = t.In(meLoc())
	} else if t, err := time.ParseInLocation("2006-01-02T15:04:05", d.DateTimeCreated, meLoc()); err == nil {
		r.IssuedAt = t
	}
	r.Total = minor(d.TotalPrice)
	if r.Total < 0 {
		r.Total, r.Kind = -r.Total, "refund"
	}
	r.TaxTotal = minor(d.TotalVATAmount)
	if r.TaxTotal < 0 {
		r.TaxTotal = -r.TaxTotal
	}
	r.Referent = d.IICReference
	r.Cashier = d.OperatorCode
	r.Merchant = Merchant{TaxID: firstNonEmpty(d.Seller.IDNum, r.Merchant.TaxID), Name: strings.TrimSpace(d.Seller.Name),
		Location: d.BusinessUnit, Address: strings.TrimSpace(d.Seller.Address), City: strings.TrimSpace(d.Seller.Town)}
	for _, pm := range d.PaymentMethod {
		amt := minor(pm.Amount)
		if amt < 0 {
			amt = -amt
		}
		r.Payments = append(r.Payments, Payment{Method: mePayment(pm.Type), Amount: amt})
	}
	for _, it := range d.Items {
		item := Item{Name: strings.TrimSpace(it.Name), GTIN: strings.TrimSpace(it.Code), Unit: strings.ToLower(strings.TrimSpace(it.Unit)),
			Quantity: it.Quantity, UnitPrice: minor(it.UnitPriceAfterVat), Total: minor(it.PriceAfterVat)}
		if it.VATRate != nil {
			item.TaxLabel = strings.TrimSuffix(fmt.Sprint(it.VATRate), ".0") + "%"
		}
		if r.Kind == "refund" {
			item.Quantity, item.Total = abs(item.Quantity), absI(item.Total)
		}
		r.Items = append(r.Items, item)
	}
	return r, nil
}

func mePayment(t string) string {
	switch strings.ToUpper(t) {
	case "BANKNOTE":
		return "cash"
	case "CARD", "BUSINESSCARD":
		return "card"
	case "SVOUCHER", "MVOUCHER":
		return "voucher"
	case "ACCOUNT", "ORDER", "FACTORING":
		return "transfer"
	}
	return "other"
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func absI(i int64) int64 {
	if i < 0 {
		return -i
	}
	return i
}
