package server

import (
	"context"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"net/http"
)

func (s *Server) inferenceFor(ctx context.Context, uid int64, provider string, directReady bool) (*core.InferenceClient, core.AIStatus) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	return s.core.SelectAI(ctx, email, provider, directReady)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

func (s *Server) handleInferenceModels(w http.ResponseWriter, r *http.Request) {
	// Use the caller's link even while the provider form has unsaved changes.
	c, status := s.inferenceFor(r.Context(), userFrom(r.Context()).ID, "pocket", false)
	if !status.Ready {
		writeErr(w, 503, status.Detail)
		return
	}
	models, err := c.Models(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, models)
}
