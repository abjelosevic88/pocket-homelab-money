package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFireflyDisconnectClearsStatusAndStopsMirror(t *testing.T) {
	s, owner, member, _ := newPerUserServer(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()
	setup := map[string]any{"firefly_url": upstream.URL, "firefly_token": "expired", "firefly_mirror": true}
	if code, _ := owner.call("PUT", "/api/v1/settings", setup, nil); code != 200 {
		t.Fatal(code)
	}
	var result ImportResult
	if code, _ := owner.call("POST", "/api/v1/import/firefly", map[string]any{"wait": true}, &result); code != 200 || result.Error == "" {
		t.Fatalf("import %d %+v", code, result)
	}
	var status struct {
		Running bool          `json:"running"`
		Last    *ImportResult `json:"last"`
	}
	owner.call("GET", "/api/v1/import/firefly/status", nil, &status)
	if status.Last == nil || status.Last.Error == "" {
		t.Fatal("expected failed import status")
	}
	// Clearing the token alone is sufficient; a stale mirror flag must not keep jobs active.
	var settings struct {
		Mirror bool `json:"firefly_mirror"`
	}
	owner.call("PUT", "/api/v1/settings", map[string]any{"firefly_token": ""}, &settings)
	if settings.Mirror {
		t.Fatal("disconnect retained mirror flag")
	}
	owner.call("GET", "/api/v1/import/firefly/status", nil, &status)
	if status.Last != nil || status.Running {
		t.Fatalf("stale status %+v", status)
	}
	before := calls
	s.runJobs(context.Background(), true)
	if calls != before {
		t.Fatal("disconnected mirror contacted Firefly")
	}
	if code, _ := owner.call("POST", "/api/v1/import/firefly", map[string]any{"wait": true}, nil); code != 400 {
		t.Fatalf("disconnected import accepted: %d", code)
	}
	// Reconnecting must not resurrect the previous connection's failure.
	owner.call("PUT", "/api/v1/settings", map[string]any{"firefly_token": "replacement"}, nil)
	owner.call("GET", "/api/v1/import/firefly/status", nil, &status)
	if status.Last != nil {
		t.Fatal("reconnection restored old failure")
	}
	member.call("GET", "/api/v1/import/firefly/status", nil, &status)
	if status.Last != nil || status.Running {
		t.Fatal("another person's import status leaked")
	}
}

func TestFireflyOldRequestCannotRestoreClearedFailure(t *testing.T) {
	_, owner, _, _ := newPerUserServer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()
	owner.call("PUT", "/api/v1/settings", map[string]any{"firefly_url": upstream.URL, "firefly_token": "expired"}, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		owner.call("POST", "/api/v1/import/firefly", map[string]any{"wait": true}, nil)
	}()
	<-entered
	owner.call("PUT", "/api/v1/settings", map[string]any{"firefly_token": ""}, nil)
	owner.call("PUT", "/api/v1/settings", map[string]any{"firefly_token": "replacement"}, nil)
	close(release)
	<-done
	var status struct {
		Last *ImportResult `json:"last"`
	}
	owner.call("GET", "/api/v1/import/firefly/status", nil, &status)
	if status.Last != nil {
		t.Fatal("old in-flight request restored cleared failure")
	}
}
