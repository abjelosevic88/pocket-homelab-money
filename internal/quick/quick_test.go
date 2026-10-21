package quick

import (
	"testing"
	"time"
)

func ctx() Context {
	five := int64(500)
	return Context{
		Today:          time.Date(2026, 10, 18, 12, 0, 0, 0, time.Local), // a Sunday
		DefaultAccount: 1,
		Accounts:       []Account{{1, "UniCredit Bank a.d.", "BAM"}, {2, "SP devizni eur", "EUR"}, {3, "Cash", "BAM"}, {4, "Yen wallet", "JPY"}},
		Categories:     []Named{{1, "hrana"}, {2, "restoran"}, {3, "automobil"}},
		Templates: []Template{
			{ID: 1, Names: []string{"Kifle"}, Kind: "expense", Amount: &five, CategoryID: 1},
			{ID: 2, Names: []string{"cg hrana"}, Kind: "expense", Currency: "EUR", CategoryID: 1, Tags: []string{"crna gora"}},
			{ID: 3, Names: []string{"cg restoran", "cgr"}, Kind: "expense", Currency: "EUR", CategoryID: 2},
			{ID: 4, Names: []string{"Plata Athena"}, Kind: "income", AccountID: 2},
			{ID: 5, Names: []string{"Gorivo"}, Kind: "expense", CategoryID: 3},
		},
		Learn: func(t string) int64 {
			if t == "Kafa" {
				return 2
			}
			return 0
		},
	}
}

func amt(d Draft) int64 {
	if d.Amount == nil {
		return -1
	}
	return *d.Amount
}

func TestParse(t *testing.T) {
	cases := []struct {
		in     string
		kind   string
		amount int64
		cur    string
		date   string
		tpl    int64
		cat    int64
		desc   string
		acct   int64
	}{
		{"kafa 6", "expense", 600, "", "2026-10-18", 0, 2, "Kafa", 1},
		{"Kifle", "expense", 500, "", "2026-10-18", 1, 1, "Kifle", 1},
		{"kifle 3,5", "expense", 350, "", "2026-10-18", 1, 1, "Kifle", 1},
		{"cg h 20", "expense", 2000, "EUR", "2026-10-18", 2, 1, "cg hrana", 1},
		{"cgr 35 usd juce", "expense", 3500, "USD", "2026-10-17", 3, 2, "cg restoran", 1},
		{"+1500 plata", "income", 150000, "", "2026-10-18", 0, 0, "Plata", 1},
		{"plata athena 8400", "income", 840000, "", "2026-10-18", 4, 0, "Plata Athena", 2},
		{"gorivo 80 -2d", "expense", 8000, "", "2026-10-16", 5, 3, "Gorivo", 1},
		{"gorivo 80 pumpa petrol", "expense", 8000, "", "2026-10-18", 5, 3, "pumpa petrol", 1},
		{"taxi 12 eur @cash #lisbon", "expense", 1200, "EUR", "2026-10-18", 0, 0, "Taxi", 3},
		{"sushi 1500 @yen", "expense", 150000, "", "2026-10-18", 0, 0, "Sushi", 4},
		{"pivo 1.500 din", "expense", 150000, "RSD", "2026-10-18", 0, 0, "Pivo", 1},
		{"hrana 20 maraka prekjuce", "expense", 2000, "BAM", "2026-10-16", 0, 1, "Hrana", 1},
		{"ručak 25 petak", "expense", 2500, "", "2026-10-16", 0, 0, "Ručak", 1},
		{"poklon 50 12.10.", "expense", 5000, "", "2026-10-12", 0, 0, "Poklon", 1},
		{"3x kafa 9", "expense", 900, "", "2026-10-18", 0, 0, "3x kafa", 1},
		{"€4.20 coffee", "expense", 420, "EUR", "2026-10-18", 0, 0, "Coffee", 1},
		{"100 >sp_devizni", "transfer", 10000, "", "2026-10-18", 0, 0, "", 1},
		{"plata athena 4800-10.23", "income", 478977, "", "2026-10-18", 4, 0, "Plata Athena", 2},
		{"plata athena 4800 - 10,23 juce", "income", 478977, "", "2026-10-17", 4, 0, "Plata Athena", 2},
		{"kafa 3x2,5", "expense", 750, "", "2026-10-18", 0, 2, "Kafa", 1},
		{"cg h (12+8)/2", "expense", 1000, "EUR", "2026-10-18", 2, 1, "cg hrana", 1},
		{"taxi 20 + 5 eur", "expense", 2500, "EUR", "2026-10-18", 0, 0, "Taxi", 1},
		{"pivo 1.500+500 din", "expense", 200000, "RSD", "2026-10-18", 0, 0, "Pivo", 1},
		{"gorivo 80 -2d", "expense", 8000, "", "2026-10-16", 5, 3, "Gorivo", 1},
		{"poklon 50 2026-10-12", "expense", 5000, "", "2026-10-12", 0, 0, "Poklon", 1},
		{"kafa 10-20", "expense", -1, "", "2026-10-18", 0, 0, "Kafa 10-20", 1},
		{"kafa 5/0", "expense", -1, "", "2026-10-18", 0, 0, "Kafa 5/0", 1},
	}
	for _, c := range cases {
		d := Parse(c.in, ctx())
		if d.Kind != c.kind || amt(d) != c.amount || d.Currency != c.cur || d.Date != c.date || d.TemplateID != c.tpl || d.CategoryID != c.cat || d.Description != c.desc || d.AccountID != c.acct {
			t.Errorf("%q → kind=%s amount=%d cur=%q date=%s tpl=%d cat=%d desc=%q acct=%d (hints %v)", c.in, d.Kind, amt(d), d.Currency, d.Date, d.TemplateID, d.CategoryID, d.Description, d.AccountID, d.Recognized)
		}
	}
	d := Parse("taxi 12 eur @cash #lisbon", ctx())
	if len(d.Tags) != 1 || d.Tags[0] != "lisbon" {
		t.Errorf("tags %v", d.Tags)
	}
	d = Parse("cg hrana 20", ctx())
	if len(d.Tags) != 1 || d.Tags[0] != "crna gora" {
		t.Errorf("template tags %v", d.Tags)
	}
	d = Parse("parking 2 14:30", ctx())
	if d.Time != "14:30" || amt(d) != 200 {
		t.Errorf("time %q amount %d", d.Time, amt(d))
	}
}

func TestSum(t *testing.T) {
	d := Parse("plata athena 4800-10.23", ctx())
	if d.Calc != "4800 − 10.23" {
		t.Errorf("calc %q", d.Calc)
	}
	if d = Parse("kafa 3,5", ctx()); d.Calc != "" {
		t.Errorf("plain amount has calc %q", d.Calc)
	}
	if d = Parse("cg h (12+8)/2", ctx()); d.Calc != "(12 + 8) ÷ 2" {
		t.Errorf("calc %q", d.Calc)
	}
}

func TestSuggest(t *testing.T) {
	c := ctx()
	c.Templates = append(c.Templates, Template{ID: 6, Names: []string{"Plata"}, Kind: "income"}, Template{ID: 7, Names: []string{"Struja", "elektro"}, Kind: "expense"})
	ids := func(s []Suggestion) []int64 {
		out := []int64{}
		for _, x := range s {
			out = append(out, x.TemplateID)
		}
		return out
	}
	cases := []struct {
		in    string
		want  []int64
		line0 string
	}{
		{"pla", []int64{4, 6}, "Plata Athena"},
		{"plata", []int64{6, 4}, "Plata"}, // exact first
		{"athena 4800-10", []int64{4}, "Plata Athena 4800-10"},
		{"plata athena 4800-10.23 #x", []int64{4, 6}, "Plata Athena 4800-10.23 #x"}, // the server drops 6: "Plata athena …" parses as 4
		{"cg", []int64{2, 3}, "cg hrana"},
		{"ele", []int64{7}, "Struja"},                      // alias match books under the name
		{"gorivo pumpa 80", []int64{5}, "Gorivo pumpa 80"}, // name, then more words
		{"+ plata 1500", []int64{6, 4}, "+Plata 1500"},
		{"4800", []int64{}, ""},
		{"zzz", []int64{}, ""},
	}
	for _, tc := range cases {
		_, got := Suggest(tc.in, c)
		g := ids(got)
		if len(g) != len(tc.want) {
			t.Errorf("%q → %v, want %v", tc.in, g, tc.want)
			continue
		}
		for i := range g {
			if g[i] != tc.want[i] {
				t.Errorf("%q → %v, want %v", tc.in, g, tc.want)
				break
			}
		}
		if len(got) > 0 && got[0].Line != tc.line0 {
			t.Errorf("%q → line %q, want %q", tc.in, got[0].Line, tc.line0)
		}
	}
	_, got := Suggest("ele", c)
	if got[0].Alias != "elektro" {
		t.Errorf("alias %q", got[0].Alias)
	}
}
