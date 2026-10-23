package server

import "testing"

// Without CORE_URL the settings say there is no Core, so the UI can grey out
// "Pocket Core" instead of offering it.
func TestSettingsCoreFlagsWithoutCore(t *testing.T) {
	c := newTestServer(t)
	var st map[string]any
	if c.do("GET", "/api/v1/settings", nil, &st) != 200 {
		t.Fatal("settings")
	}
	if st["core_configured"] != false || st["core_registered"] != false {
		t.Fatalf("core flags = %v / %v, want false / false", st["core_configured"], st["core_registered"])
	}
}
