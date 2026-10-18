package fx

import "testing"

func TestPegged(t *testing.T) {
	if r, ok := Pegged("EUR", "BAM"); !ok || r != 1.95583 {
		t.Fatalf("EUR→BAM %v %v", r, ok)
	}
	if r, ok := Pegged("BAM", "EUR"); !ok || r < 0.5112 || r > 0.5113 {
		t.Fatalf("BAM→EUR %v", r)
	}
	if _, ok := Pegged("USD", "BAM"); ok {
		t.Fatal("USD is not pegged")
	}
}
