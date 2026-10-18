package currency

import "testing"

func TestConvert(t *testing.T) {
	cases := []struct {
		amount   int64
		from, to string
		rate     float64
		want     int64
	}{
		{1000, "EUR", "EUR", 1, 1000},
		{1000, "EUR", "USD", 1.1, 1100},
		{1000, "JPY", "EUR", 0.0062, 620},     // 1000 ¥ → 6.20 €
		{1000, "EUR", "JPY", 161.3, 1613},     // 10 € → 1613 ¥
		{1000, "BHD", "EUR", 2.45, 245},       // 1.000 BHD → 2.45 €
		{19558, "BAM", "EUR", 0.51129, 10000}, // 195.58 KM → 100.00 €
	}
	for _, c := range cases {
		if got := Convert(c.amount, c.from, c.to, c.rate); got != c.want {
			t.Errorf("Convert(%d %s→%s @%v) = %d, want %d", c.amount, c.from, c.to, c.rate, got, c.want)
		}
	}
	if !Valid("eur") || Valid("XXX") || Decimals("JPY") != 0 || Decimals("KWD") != 3 || Decimals("???") != 2 {
		t.Error("lookup")
	}
	if len(All()) < 150 {
		t.Errorf("only %d currencies", len(All()))
	}
}
