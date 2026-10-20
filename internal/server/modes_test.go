package server

import (
	"context"
	"testing"
	"time"
)

// waitModes waits for report number n (1-based) the fake Core received and
// returns it as "user target" → mode.
func (f *fakeLinkCore) waitModes(t *testing.T, n int) map[string]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.modes) >= n {
			out := map[string]string{}
			for _, c := range f.modes[n-1] {
				out[c.User+" "+c.Target] = c.Mode
			}
			f.mu.Unlock()
			return out
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no connection-mode report #%d from Money", n)
	return nil
}

func (f *fakeLinkCore) modeReports() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.modes)
}

func TestConnectionModesReportedToCore(t *testing.T) {
	travel := newFakeTravel(t)
	fc := &fakeLinkCore{travelURL: travel.URL}
	s, base, me := newLinkServer(t, fc.handler(), nil)

	// Right after registering: nothing chosen and no token is Pocket Core.
	if got := fc.waitModes(t, 1); len(got) != 1 || got["owner@example.com travel"] != "core" {
		t.Fatalf("first report %v", got)
	}

	// A saved token with no mode chosen is Direct; everyone goes in one batch.
	member, _ := s.firstOrSystemUser(context.Background(), "member@example.com", "Member")
	mtok, _, _ := s.store.CreateAPIToken(context.Background(), member.ID, "m")
	them := &peer{t: t, base: base, token: mtok}
	if code, raw := them.call("PUT", "/api/v1/settings", map[string]any{"travel_url": travel.URL, "travel_token": "member-travel-token"}, nil); code != 200 {
		t.Fatalf("save token %d %s", code, raw)
	}
	got := fc.waitModes(t, 2)
	if len(got) != 2 || got["member@example.com travel"] != "direct" || got["owner@example.com travel"] != "core" {
		t.Fatalf("after token %v", got)
	}

	// A person switching the connection off.
	if code, raw := me.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "off"}, nil); code != 200 {
		t.Fatalf("off %d %s", code, raw)
	}
	if got := fc.waitModes(t, 3); got["owner@example.com travel"] != "off" || got["member@example.com travel"] != "direct" {
		t.Fatalf("after off %v", got)
	}

	// Disconnecting removes the token: back to Pocket Core.
	if code, raw := them.call("PUT", "/api/v1/settings", map[string]any{"travel_url": "", "travel_token": ""}, nil); code != 200 {
		t.Fatalf("disconnect %d %s", code, raw)
	}
	if got := fc.waitModes(t, 4); got["member@example.com travel"] != "core" {
		t.Fatalf("after disconnect %v", got)
	}

	// Saving something unrelated sends nothing.
	n := fc.modeReports()
	me.call("PUT", "/api/v1/settings", map[string]any{"notify_hour": 7}, nil)
	time.Sleep(100 * time.Millisecond)
	if fc.modeReports() != n {
		t.Fatal("an unrelated setting should not report modes")
	}
}

// Without Pocket Core nothing is reported (and nothing breaks).
func TestConnectionModesWithoutCore(t *testing.T) {
	s, a, _, _ := newPerUserServer(t)
	s.modesChanged()
	s.reportModes(context.Background())
	if code, raw := a.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "off"}, nil); code != 200 {
		t.Fatalf("mode %d %s", code, raw)
	}
}
