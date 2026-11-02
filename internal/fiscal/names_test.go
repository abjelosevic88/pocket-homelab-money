package fiscal

import "testing"

func TestShortItemName(t *testing.T) {
	for in, want := range map[string]string{
		"028396 VINDIJA PUREA DE LUX UNKA 1 kg/KG/KG":         "Vindija Purea De Lux Unka",
		"021995 GORKI LIST BILJNI LIKER 0,1L FRUTELA/KOM/KOM": "Gorki List Biljni Liker Frutela",
		"KEFIR 2.8% BALANS+/KO":                               "Kefir 2.8% Balans+",
		"Kifla":                                               "Kifla",
	} {
		if got := ShortItemName(in); got != want {
			t.Errorf("ShortItemName(%q) = %q, want %q", in, got, want)
		}
	}
}
