package ai

import (
	"encoding/json"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPocketFailureNeverCallsSavedOpenAI(t *testing.T) {
	var directCalls atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directCalls.Add(1); w.WriteHeader(500) }))
	defer direct.Close()
	pocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte(`{"error":"model unavailable"}`))
	}))
	defer pocket.Close()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/links/status" {
			json.NewEncoder(w).Encode([]core.LinkState{{Scope: core.InferenceScope, On: true}})
			return
		}
		json.NewEncoder(w).Encode(core.LinkToken{AccessToken: "pcx_private", URL: pocket.URL})
	}))
	defer hub.Close()
	c := New(direct.URL, "saved-openai-key", "test-model", "transcribe")
	c.Pocket, _ = core.New(hub.URL, core.Options{Token: "app"}).SelectAI(t.Context(), "member@test", "auto", true)
	_, _, err := c.Complete(t.Context(), "system", "private", false)
	if err == nil {
		t.Fatal("expected Pocket AI failure")
	}
	if directCalls.Load() != 0 {
		t.Fatal("silently sent private data to OpenAI")
	}
}
