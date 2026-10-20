package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrReplayed = errors.New("operation already applied")

// TransactionOperation resolves a retry before rules or other side effects run.
func (s *Store) TransactionOperation(ctx context.Context, key, hash string) (int64, error) {
	var id sql.NullInt64
	var saved string
	err := s.DB.QueryRowContext(ctx, `SELECT transaction_id, request_hash FROM transaction_operations WHERE operation_key = ?`, key).Scan(&id, &saved)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if saved != hash {
		return 0, ErrConflict
	}
	if !id.Valid {
		return 0, ErrNotFound
	}
	return id.Int64, nil
}
