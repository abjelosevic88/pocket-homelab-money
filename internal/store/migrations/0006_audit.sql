-- Per-account audit log: who added, changed or deleted what on an account,
-- readable by the account's owner only. Entries outlive the transactions
-- they describe (transaction_id is a plain number) and go with the account.
CREATE TABLE account_audit (
  id             INTEGER PRIMARY KEY,
  account_id     INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  actor_user_id  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  actor_label    TEXT NOT NULL DEFAULT '',
  source         TEXT NOT NULL DEFAULT 'web',   -- web | api | firefly_api | quick | template | import | mirror | schedule | bulk | reconcile
  action         TEXT NOT NULL,                 -- transaction.created | transaction.updated | ... | import.summary
  transaction_id INTEGER,
  summary        TEXT NOT NULL DEFAULT '',
  changes        TEXT NOT NULL DEFAULT '{}',    -- field -> {from, to}
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX account_audit_account ON account_audit(account_id, created_at);
