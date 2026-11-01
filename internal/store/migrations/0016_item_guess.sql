-- Items for hand-typed expenses: right after an expense is typed the AI
-- reads it ("pica i krofna", 8 KM) and writes receipt lines for it (Pica 5.00,
-- Krofna 3.00) that add up to the amount. They are stored as a receipt with
-- provider 'ai' linked to the transaction, so products, groups and their
-- statistics count them like scanned ones. A scanned receipt for the same
-- purchase replaces them.
ALTER TABLE receipt_settings ADD COLUMN guess_items INTEGER NOT NULL DEFAULT 1;

-- What the AI was asked, so a transaction is asked about once per amount and
-- description (editing either asks again; status error is retried).
CREATE TABLE item_guesses (
  transaction_id INTEGER PRIMARY KEY REFERENCES transactions(id) ON DELETE CASCADE,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount         INTEGER NOT NULL DEFAULT 0,
  description    TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'items', -- items | none | error
  note           TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX item_guesses_user ON item_guesses(user_id);
