-- Idempotency records deliberately survive deletion of the transaction.
CREATE TABLE transaction_operations (
  operation_key TEXT PRIMARY KEY,
  request_hash TEXT NOT NULL,
  transaction_id INTEGER REFERENCES transactions(id) ON DELETE SET NULL
);
