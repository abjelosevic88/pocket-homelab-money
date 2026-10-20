package server

import (
	"context"
	"database/sql"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// queueEvent must run before the domain transaction commits.
func (s *Server) queueEvent(ctx context.Context, tx *sql.Tx, topic, subject string, payload map[string]any) error {
	if !s.core.Configured() {
		return nil
	}
	return core.QueueEvent(ctx, tx, topic, subject, payload)
}
