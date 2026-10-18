-- Split transactions: one receipt across several categories. Amounts are in
-- the account's currency (foreign_amount when the receipt was in another one)
-- and always add up to the transaction.
CREATE TABLE transaction_splits (
  id             INTEGER PRIMARY KEY,
  transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  category_id    INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  amount         INTEGER NOT NULL DEFAULT 0,
  foreign_amount INTEGER NOT NULL DEFAULT 0,
  amount_home    INTEGER NOT NULL DEFAULT 0,
  note           TEXT NOT NULL DEFAULT '',
  sort_order     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX splits_tx ON transaction_splits(transaction_id);
CREATE INDEX splits_cat ON transaction_splits(category_id);

-- What category reports and budgets add up: a transaction without splits is
-- one line, a split transaction is one line per part.
CREATE VIEW tx_lines AS
  SELECT t.id AS transaction_id, t.kind, t.date, t.account_id, t.category_id, t.amount_home
  FROM transactions t WHERE NOT EXISTS (SELECT 1 FROM transaction_splits s WHERE s.transaction_id = t.id)
  UNION ALL
  SELECT s.transaction_id, t.kind, t.date, t.account_id, s.category_id, s.amount_home
  FROM transaction_splits s JOIN transactions t ON t.id = s.transaction_id;

-- Liabilities: loan / mortgage / debt accounts. direction "owe" = I owe it
-- (negative balance), "owed" = someone owes me (positive balance).
ALTER TABLE accounts ADD COLUMN direction TEXT NOT NULL DEFAULT 'owe';
ALTER TABLE accounts ADD COLUMN interest_rate REAL NOT NULL DEFAULT 0;      -- % per year
ALTER TABLE accounts ADD COLUMN interest_auto INTEGER NOT NULL DEFAULT 0;   -- book interest every month
ALTER TABLE accounts ADD COLUMN principal INTEGER NOT NULL DEFAULT 0;       -- original amount
ALTER TABLE accounts ADD COLUMN payment INTEGER NOT NULL DEFAULT 0;         -- regular monthly payment
ALTER TABLE accounts ADD COLUMN payment_day INTEGER NOT NULL DEFAULT 0;     -- 1..28, 0 = not set
ALTER TABLE accounts ADD COLUMN interest_booked TEXT NOT NULL DEFAULT '';   -- YYYY-MM last booked

-- Recurrence options.
ALTER TABLE subscriptions ADD COLUMN weekend TEXT NOT NULL DEFAULT '';      -- '' | before (Friday) | after (Monday) | skip
ALTER TABLE subscriptions ADD COLUMN nth INTEGER NOT NULL DEFAULT 0;        -- monthly on the nth weekday: 1..4, 5 = last; 0 = by date
ALTER TABLE subscriptions ADD COLUMN max_count INTEGER NOT NULL DEFAULT 0;  -- stop after this many; 0 = no limit
ALTER TABLE subscriptions ADD COLUMN done_count INTEGER NOT NULL DEFAULT 0;

-- Outgoing webhooks.
CREATE TABLE webhooks (
  id          INTEGER PRIMARY KEY,
  owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL DEFAULT '',
  url         TEXT NOT NULL,
  secret      TEXT NOT NULL DEFAULT '',
  events      TEXT NOT NULL DEFAULT '[]',   -- transaction.created | transaction.updated | transaction.deleted | budget.alert | subscription.due
  format      TEXT NOT NULL DEFAULT 'native', -- native | firefly
  active      INTEGER NOT NULL DEFAULT 1,
  last_status INTEGER NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT '',
  last_at     TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE webhook_deliveries (
  id          INTEGER PRIMARY KEY,
  webhook_id  INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  event       TEXT NOT NULL,
  subject     TEXT NOT NULL DEFAULT '',
  status      INTEGER NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  attempts    INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX webhook_deliveries_hook ON webhook_deliveries(webhook_id, id);
