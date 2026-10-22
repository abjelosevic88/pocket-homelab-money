package server

import (
	"context"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

func (s *Server) inferenceFor(ctx context.Context, uid int64, provider string, directReady bool) (*core.InferenceClient, core.AIStatus) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	return s.core.SelectAI(ctx, email, provider, directReady)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }
