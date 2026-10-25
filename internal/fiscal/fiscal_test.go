package fiscal

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"os"
	"testing"
	"time"
)

// a v3 SUF payload: devices, counters, total 30.64, time; the encrypted tail is filler
func sufLink(host string) string {
	b := make([]byte, 60)
	b[0] = 3
	copy(b[1:9], "ABCD1234")
	copy(b[9:17], "ABCD1234")
	binary.LittleEndian.PutUint32(b[17:21], 12158)
	binary.LittleEndian.PutUint32(b[21:25], 12158)
	binary.LittleEndian.PutUint64(b[25:33], 306400)
	binary.BigEndian.PutUint64(b[33:41], uint64(time.Date(2026, 10, 25, 8, 24, 16, 0, time.UTC).UnixMilli()))
	for i := 43; i < len(b); i++ {
		b[i] = byte(i * 7) // makes "+" and "/" show up in the base64
	}
	return "https://" + host + "/v/?vl=" + base64.StdEncoding.EncodeToString(b)
}

func TestSUFDecode(t *testing.T) {
	p, u, err := Find("  " + sufLink("suf.poreskaupravars.org") + "\n")
	if err != nil || p.ID() != "ba-rs" {
		t.Fatalf("find: %v %v", p, err)
	}
	r, err := p.Decode(u)
	if err != nil || r == nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Key != "ABCD1234-ABCD1234-12158" || r.Total != 3064 || r.Currency != "BAM" {
		t.Fatalf("decoded %+v", r)
	}
	if got := r.IssuedAt.Format("2006-01-02 15:04"); got != "2026-10-25 09:24" {
		t.Fatalf("local time %s", got)
	}
	if p2, _, _ := Find(sufLink("suf.purs.gov.rs")); p2 == nil || p2.ID() != "rs" {
		t.Fatal("serbia not matched")
	}
}

const sufJSON = `{"invoiceRequest":{"taxId":"4400000000000","businessName":"\"PRIMJER MARKET\" d.o.o. Banja Luka","locationName":"PJ Centar","address":"Glavna 1","city":"Banja Luka","cashier":"7","requestedBy":"ABCD1234","referentDocumentNumber":null,"invoiceType":0,"transactionType":0,
"payments":[{"paymentType":2,"amount":12.50}],
"items":[{"name":"MLIJEKO 2.8% 1L/KO","quantity":2.0,"unitPrice":2.15,"totalPrice":4.30,"gtin":" 1"},{"name":"Jabuka BIO/KG","quantity":1.504,"unitPrice":5.45,"totalPrice":8.20,"gtin":" 2"}]},
"invoiceResult":{"totalAmount":12.50,"invoiceNumber":"ABCD1234-ABCD1234-7","signedBy":"ABCD1234","sdcTime":"2026-10-25T08:24:16.542Z"},
"journal":"MLIJEKO 2.8% 1L/KO (Е)\n   2,15   2   4,30\nУкупан износ пореза:                1,82\n","isValid":true}`

func TestSUFParse(t *testing.T) {
	p, u, _ := Find(sufLink("suf.poreskaupravars.org"))
	r, err := p.Parse(u, []byte(sufJSON))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || r.Total != 1250 || r.TaxTotal != 182 || r.Method() != "card" || len(r.Items) != 2 {
		t.Fatalf("parsed %+v", r)
	}
	if r.Items[0].TaxLabel != "Е" || r.Items[0].Unit != "ko" || r.Items[1].Total != 820 {
		t.Fatalf("items %+v", r.Items)
	}
	if MerchantName(r.Merchant.Name) != "Primjer Market" {
		t.Fatalf("merchant name %q", MerchantName(r.Merchant.Name))
	}
	if _, err := p.Parse(u, []byte(`{"isValid":false}`)); err != ErrInvalid {
		t.Fatalf("invalid: %v", err)
	}
}

func TestMontenegroAndCroatia(t *testing.T) {
	p, u, err := Find("https://mapr.tax.gov.me/ic/#/verify?iic=0a1b2c3d4e5f60718293a4b5c6d7e8f9&tin=02000000&crtd=2026-10-01T10:00:00+02:00&ord=12&bu=ab123cd456&cr=xy789&sw=sw1&prc=12.50")
	if err != nil || p.ID() != "me" {
		t.Fatalf("me find: %v", err)
	}
	r, _ := p.Decode(u)
	if r.Total != 1250 || r.IssuedAt.Hour() != 10 || r.Key != "0A1B2C3D4E5F60718293A4B5C6D7E8F9" {
		t.Fatalf("me decoded %+v", r)
	}
	full, err := p.Parse(u, []byte(`{"iic":"0a1b2c3d4e5f60718293a4b5c6d7e8f9","dateTimeCreated":"2026-10-01T10:00:00+02:00","invoiceOrderNumber":12,"businessUnit":"ab123cd456","cashRegister":"xy789","totalPrice":12.5,"totalVATAmount":1.9,
	"seller":{"idNum":"02000000","name":"PRIMJER DOO","address":"Bulevar 1","town":"Podgorica"},
	"items":[{"name":"Hljeb","code":"1","unit":"KOM","quantity":1,"unitPriceAfterVat":1.0,"priceAfterVat":1.0,"vatRate":7},{"name":"Kafa","code":"2","unit":"KOM","quantity":1,"unitPriceAfterVat":11.5,"priceAfterVat":11.5,"vatRate":21}],
	"paymentMethod":[{"type":"BANKNOTE","amount":12.5}]}`))
	if err != nil || full.Method() != "cash" || len(full.Items) != 2 || full.Merchant.City != "Podgorica" || full.InvoiceNumber != "12/ab123cd456/xy789" {
		t.Fatalf("me parsed %+v %v", full, err)
	}
	if _, err := p.Parse(u, []byte("")); err != ErrNotYet {
		t.Fatalf("me empty: %v", err)
	}

	p, u, err = Find("https://porezna.gov.hr/rn?jir=11111111-2222-3333-4444-555555555555&datv=20261025_0924&izn=3064")
	if err != nil || p.ID() != "hr" {
		t.Fatalf("hr find: %v", err)
	}
	r, _ = p.Decode(u)
	if r.Total != 3064 || r.IssuedAt.Format("15:04") != "09:24" || !r.Complete || r.HasItems {
		t.Fatalf("hr %+v", r)
	}
	if _, _, err := Find("https://example.com/?vl=abc"); err != ErrUnsupported {
		t.Fatal("example.com matched")
	}
}

func TestNames(t *testing.T) {
	cases := map[string]string{"KEFIR 2.8% BALANS+/KO": "kefir 2.8% balans+", "МЛИЈЕКО 1Л/КОМ (Е)": "mlijeko 1l", "Jabuka zlatni delišes/KG": "jabuka zlatni delises"}
	for in, want := range cases {
		if got := ProductKey(in); got != want {
			t.Errorf("ProductKey(%q) = %q, want %q", in, got, want)
		}
	}
	if ProductName("KEFIR 2.8% BALANS+/KO") != "Kefir 2.8% Balans+" {
		t.Error(ProductName("KEFIR 2.8% BALANS+/KO"))
	}
	if !MatchesKeyword("trajno mlijeko 2.8%", "mlijeko") || MatchesKeyword("xmlijeko", "mlijeko") || !MatchesKeyword("caj kamilica", "caj") {
		t.Error("keyword match")
	}
	if got := ShortLocation(`10024402700780031-"TROPIC MALOPRODAJA" d.o.o. Banja Luka,PJ "TROPIC CENTAR"`, `"TROPIC MALOPRODAJA" d.o.o. Banja Luka`); got != `PJ "TROPIC CENTAR"` {
		t.Error(got)
	}
	if MerchantName("KONZUM d.o.o. Sarajevo") != "Konzum" {
		t.Error(MerchantName("KONZUM d.o.o. Sarajevo"))
	}
}

// FISCAL_LIVE_URL=<a real receipt link> go test -run Live ./internal/fiscal
func TestLive(t *testing.T) {
	link := os.Getenv("FISCAL_LIVE_URL")
	if link == "" {
		t.Skip("FISCAL_LIVE_URL not set")
	}
	p, u, err := Find(link)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := p.Fetch(context.Background(), &http.Client{Timeout: 20 * time.Second}, u)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := p.Decode(u)
	t.Logf("%s %s %d %s items=%d method=%s decoded=%+v", r.Key, r.Merchant.Name, r.Total, r.IssuedAt, len(r.Items), r.Method(), d)
	if d != nil && (d.Key != r.Key || d.Total != r.Total) {
		t.Fatalf("offline decode disagrees: %s/%d vs %s/%d", d.Key, d.Total, r.Key, r.Total)
	}
}
