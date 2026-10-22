package server

import (
	"encoding/json"
	"testing"
)

func TestPersonalPocketModelPreference(t *testing.T) {
	_, owner, member, _ := newPerUserServer(t)
	call := func(who, method string, body map[string]any) (int, []byte) {
		p := owner
		if who == "other" {
			p = member
		}
		return p.call(method, "/api/v1/settings", body, nil)
	}
	get := func(who string) string {
		t.Helper()
		code, raw := call(who, "GET", nil)
		var data struct {
			Model string `json:"ai_pocket_model"`
		}
		if err := json.Unmarshal(raw, &data); err != nil || code != 200 {
			t.Fatalf("read settings: %d %s", code, raw)
		}
		return data.Model
	}
	if get("owner") != "" || get("other") != "" {
		t.Fatal("existing accounts should follow Pocket AI default")
	}
	for _, model := range []string{"sonnet", "opus", ""} {
		code, raw := call("owner", "PUT", map[string]any{"ai_pocket_model": model})
		if code != 200 {
			t.Fatalf("save: %d %s", code, raw)
		}
		if get("owner") != model {
			t.Fatal("model did not persist")
		}
		if get("other") != "" {
			t.Fatal("model leaked to another person")
		}
		code, raw = call("owner", "PUT", map[string]any{"ai_provider": "auto"})
		if code != 200 || get("owner") != model {
			t.Fatalf("partial save lost model: %d %s", code, raw)
		}
	}
	if code, raw := call("owner", "PUT", map[string]any{"ai_pocket_model": "opus\n--tools"}); code != 400 {
		t.Fatalf("invalid model accepted: %d %s", code, raw)
	}
}
