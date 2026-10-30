package fiscal

import "testing"

func TestLiquidSize(t *testing.T) {
	for key, want := range map[string]bool{
		ProductKey("Exotic kruška 0.25"):  true,
		ProductKey("VODA PR 0,5L VIVIA"):  true,
		ProductKey("SOK JABUKA 1.5L"):     true,
		ProductKey("COCA COLA 330ML"):     true,
		ProductKey("ULJE SUNCOKRET 1L"):   true,
		ProductKey("JABUKA CRVENA/KG"):    false,
		ProductKey("KRUSKA"):              false,
		ProductKey("KEFIR 2.8% BALANS+"):  false,
		ProductKey("SALAMA PILECA 0,5KG"): false,
		ProductKey("BRUFEN 400MG"):        false,
	} {
		if got := HasLiquidSize(key); got != want {
			t.Errorf("%q: %v", key, got)
		}
	}
	if HoldsLiquids("jabuka, kruska, banana") || !HoldsLiquids("Sok, nektar") || !HoldsLiquids("voda, mineralna") {
		t.Error("HoldsLiquids")
	}
}
