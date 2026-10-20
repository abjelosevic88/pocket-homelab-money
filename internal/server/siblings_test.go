package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// fakeTravel is a Pocket Travel with one trip running today; it records the
// bearer of every call.
type fakeTravel struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
}

func newFakeTravel(t *testing.T) *fakeTravel {
	ft := &fakeTravel{}
	day := func(d int) string { return time.Now().AddDate(0, 0, d).Format("2006-01-02") }
	ft.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok","version":"9.9"}`))
			return
		}
		ft.mu.Lock()
		ft.auths = append(ft.auths, r.Header.Get("Authorization"))
		ft.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/auth/me":
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/api/v1/trips":
			if r.URL.Query().Get("archived") == "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 4, "name": "Rome", "destination": "Rome", "start_date": day(-1), "end_date": day(1), "firefly_tag": "Rome 2026"}})
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(ft.Close)
	return ft
}

func (ft *fakeTravel) calls() []string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return slices.Clone(ft.auths)
}

// fakeLinkCore is Pocket Core for the acting side: links/status and
// links/token for Money → Travel (trips:read). The owner has the link (until
// linkOff), anyone else is not in Travel's audience.
type fakeLinkCore struct {
	travelURL string
	linkOff   atomic.Bool
	tokens    atomic.Int32
	mu        sync.Mutex
	manifest  map[string]any
	modes     [][]core.ConnMode // every PUT /api/v1/links/modes
}

func (f *fakeLinkCore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state := func(user string) string {
			switch {
			case user != "owner@example.com":
				return "not_in_audience"
			case f.linkOff.Load():
				return "off_person"
			}
			return "on"
		}
		switch r.URL.Path {
		case "/api/v1/links/modes":
			if r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer pca_money" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var in struct {
				Modes []core.ConnMode `json:"modes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.mu.Lock()
			f.modes = append(f.modes, in.Modes)
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"recorded":1}`))
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			f.mu.Lock()
			f.manifest = m
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"app":{},"token":"pca_money"}`))
		case "/api/v1/links/status":
			st := state(r.URL.Query().Get("user"))
			_ = json.NewEncoder(w).Encode([]map[string]any{{"app": "travel", "scope": "trips:read", "state": st, "on": st == "on"}})
		case "/api/v1/links/token":
			var in struct {
				User   string   `json:"user"`
				App    string   `json:"app"`
				Scopes []string `json:"scopes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.App != "travel" || !slices.Equal(in.Scopes, []string{"trips:read"}) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"not declared","reason":"not_declared"}`))
				return
			}
			if st := state(in.User); st != "on" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "no link", "reason": st})
				return
			}
			f.tokens.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "pcx_travel_" + in.User, "expires_in": 300, "url": f.travelURL, "public_url": "https://travel.example"})
		default:
			http.NotFound(w, r)
		}
	})
}

func settingsOf(t *testing.T, p *peer) map[string]any {
	t.Helper()
	var st map[string]any
	if code, raw := p.call("GET", "/api/v1/settings", nil, &st); code != 200 {
		t.Fatalf("settings %d %s", code, raw)
	}
	return st
}

// expense books one expense today and returns its tags.
func expense(t *testing.T, p *peer, acct int64) []string {
	t.Helper()
	var tx store.Transaction
	if code, raw := p.call("POST", "/api/v1/transactions", map[string]any{"kind": "expense", "account_id": acct, "amount": 500, "description": "Gelato"}, &tx); code != 201 {
		t.Fatalf("expense %d %s", code, raw)
	}
	return tx.Tags
}

func TestTravelThroughCore(t *testing.T) {
	travel := newFakeTravel(t)
	fc := &fakeLinkCore{travelURL: travel.URL}
	s, base, me := newLinkServer(t, fc.handler(), nil)
	fc.mu.Lock()
	uses, _ := json.Marshal(fc.manifest["uses"])
	fc.mu.Unlock()
	if !strings.Contains(string(uses), `"app":"travel"`) || !strings.Contains(string(uses), `"trips:read"`) {
		t.Fatalf("manifest uses %s", uses)
	}
	var acct store.Account
	me.call("POST", "/api/v1/accounts", map[string]any{"name": "Cash", "currency": "BAM"}, &acct)
	last := func() string {
		c := travel.calls()
		if len(c) == 0 {
			return ""
		}
		return c[len(c)-1]
	}

	// No token anywhere: Pocket Core, as the person, and it works.
	st := settingsOf(t, me)
	if st["core_configured"] != true || st["travel_mode"] != "core" || st["travel_via"] != "core" || st["travel_configured"] != true || st["travel_detail"] != "" {
		t.Fatalf("default settings %v", st)
	}
	if tags := expense(t, me, acct.ID); !slices.Contains(tags, "Rome 2026") {
		t.Fatalf("core mode: no trip tag %v", tags)
	}
	if last() != "Bearer pcx_travel_owner@example.com" {
		t.Fatalf("core mode called travel with %q", last())
	}
	var res struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	me.call("POST", "/api/v1/integrations/test", map[string]any{"which": "travel"}, &res)
	if !res.OK || !strings.Contains(res.Message, "through Pocket Core") {
		t.Fatalf("core test %+v", res)
	}
	var trips struct {
		Public  string         `json:"public_url"`
		Running map[string]any `json:"running"`
	}
	me.call("GET", "/api/v1/travel/trips", nil, &trips)
	if trips.Public != "https://travel.example" || trips.Running["name"] != "Rome" {
		t.Fatalf("trips %+v", trips)
	}

	// A token typed into Settings: direct, and that token is what Travel sees.
	me.call("PUT", "/api/v1/settings", map[string]any{"travel_url": travel.URL, "travel_token": "manual-travel-token"}, nil)
	if st := settingsOf(t, me); st["travel_mode"] != "direct" || st["travel_via"] != "direct" {
		t.Fatalf("a typed token should make it direct: %v", st)
	}
	if tags := expense(t, me, acct.ID); !slices.Contains(tags, "Rome 2026") || last() != "Bearer manual-travel-token" {
		t.Fatalf("direct mode: tags %v, travel called with %q", tags, last())
	}
	me.call("POST", "/api/v1/integrations/test", map[string]any{"which": "travel"}, &res)
	if !res.OK || !strings.Contains(res.Message, "direct connection") {
		t.Fatalf("direct test %+v", res)
	}

	// Choosing Pocket Core ignores the saved token (it stays, for switching back).
	me.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "core"}, nil)
	if tags := expense(t, me, acct.ID); !slices.Contains(tags, "Rome 2026") || last() != "Bearer pcx_travel_owner@example.com" {
		t.Fatalf("core again: tags %v, travel called with %q", tags, last())
	}

	// Off: Travel is not asked at all, and Settings says so; the token is kept.
	me.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "off"}, nil)
	n, tok := len(travel.calls()), fc.tokens.Load()
	if tags := expense(t, me, acct.ID); slices.Contains(tags, "Rome 2026") {
		t.Fatalf("off still tags %v", tags)
	}
	me.call("GET", "/api/v1/home", nil, nil)
	me.call("GET", "/api/v1/travel/trips", nil, nil)
	if len(travel.calls()) != n || fc.tokens.Load() != tok {
		t.Fatalf("off still called travel or core: %d→%d calls, %d→%d tokens", n, len(travel.calls()), tok, fc.tokens.Load())
	}
	if st := settingsOf(t, me); st["travel_mode"] != "off" || st["travel_via"] != "" || st["travel_detail"] != "switched off" || st["travel_token"] == "" {
		t.Fatalf("off settings %v", st)
	}

	// The person switches the link off in Core: not connected, says why, nothing tagged.
	fc.linkOff.Store(true)
	me.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "core"}, nil)
	st = settingsOf(t, me)
	if st["travel_mode"] != "core" || st["travel_via"] != "" || st["travel_configured"] != false || !strings.Contains(st["travel_detail"].(string), "switched the Money → Pocket Travel link off") {
		t.Fatalf("link off settings %v", st)
	}
	n = len(travel.calls())
	if tags := expense(t, me, acct.ID); slices.Contains(tags, "Rome 2026") || len(travel.calls()) != n {
		t.Fatalf("link off: tags %v, travel calls %d→%d", tags, n, len(travel.calls()))
	}
	me.call("POST", "/api/v1/integrations/test", map[string]any{"which": "travel"}, &res)
	if res.OK || !strings.Contains(res.Error, "link off") {
		t.Fatalf("link off test %+v", res)
	}
	fc.linkOff.Store(false)

	// Disconnect removes the URL and token.
	me.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "direct", "travel_url": "", "travel_token": "", "travel_public_url": ""}, nil)
	if st := settingsOf(t, me); st["travel_token"] != "" || st["travel_url"] != "" || st["travel_detail"] != "no token yet" {
		t.Fatalf("disconnected %v", st)
	}

	// Per person: someone else has their own mode, and no access to Travel in Core.
	member, _ := s.firstOrSystemUser(context.Background(), "member@example.com", "Member")
	mtok, _, _ := s.store.CreateAPIToken(context.Background(), member.ID, "m")
	them := &peer{t: t, base: base, token: mtok}
	if st := settingsOf(t, them); st["travel_mode"] != "core" || st["travel_via"] != "" || !strings.Contains(st["travel_detail"].(string), "do not have access to Pocket Travel") {
		t.Fatalf("member settings %v", st)
	}
}

// A Travel URL and token from the environment are copied into the owner's
// settings once: cleared in Settings, they stay cleared after a restart.
func TestTravelEnvSeededOnce(t *testing.T) {
	travel := newFakeTravel(t)
	fc := &fakeLinkCore{travelURL: travel.URL}
	s, _, me := newLinkServer(t, fc.handler(), func(c *config.Config) {
		c.TravelURL, c.TravelToken = travel.URL, "env-travel-token"
	})
	if st := settingsOf(t, me); st["travel_mode"] != "direct" || st["travel_via"] != "direct" || st["travel_url"] != travel.URL {
		t.Fatalf("env token should keep working directly: %v", st)
	}
	me.call("PUT", "/api/v1/settings", map[string]any{"travel_mode": "core", "travel_url": "", "travel_token": ""}, nil)
	ctx := context.Background()
	owner, _ := s.store.GetUserByEmail(ctx, "owner@example.com")
	if _, err := s.seedOwnerSettings(ctx, owner.ID); err != nil { // what the next start does
		t.Fatal(err)
	}
	if st := settingsOf(t, me); st["travel_token"] != "" || st["travel_url"] != "" || st["travel_mode"] != "core" || st["travel_via"] != "core" {
		t.Fatalf("a cleared env value came back: %v", st)
	}
}
