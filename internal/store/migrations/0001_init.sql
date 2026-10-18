CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

-- An account holds money in ONE currency. Its balance is never stored: it is
-- opening_balance plus every transaction that touches it, so an edit can
-- never leave a stale balance behind.
CREATE TABLE accounts (
  id                INTEGER PRIMARY KEY,
  owner_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  kind              TEXT NOT NULL DEFAULT 'checking',   -- checking | savings | cash | credit | investment | loan | other
  currency          TEXT NOT NULL DEFAULT 'EUR',
  opening_balance   INTEGER NOT NULL DEFAULT 0,         -- minor units
  opening_date      TEXT NOT NULL DEFAULT '',
  institution       TEXT NOT NULL DEFAULT '',
  number            TEXT NOT NULL DEFAULT '',           -- IBAN or last digits, shown masked
  color             TEXT NOT NULL DEFAULT '',
  icon              TEXT NOT NULL DEFAULT '',
  credit_limit      INTEGER NOT NULL DEFAULT 0,
  include_net_worth INTEGER NOT NULL DEFAULT 1,
  archived          INTEGER NOT NULL DEFAULT 0,
  sort_order        INTEGER NOT NULL DEFAULT 0,
  notes             TEXT NOT NULL DEFAULT '',
  external_id       TEXT NOT NULL DEFAULT '',           -- firefly:<id>
  created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX accounts_owner ON accounts(owner_id);

CREATE TABLE account_members (
  account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL DEFAULT 'editor',            -- editor | viewer
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (account_id, user_id)
);

-- Categories are shared by everyone on the server; one level of children.
CREATE TABLE categories (
  id          INTEGER PRIMARY KEY,
  parent_id   INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL DEFAULT 'expense',          -- expense | income | both
  icon        TEXT NOT NULL DEFAULT '',
  color       TEXT NOT NULL DEFAULT '',
  archived    INTEGER NOT NULL DEFAULT 0,
  sort_order  INTEGER NOT NULL DEFAULT 0,
  external_id TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX categories_name ON categories(lower(name), coalesce(parent_id, 0));

CREATE TABLE tags (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  color       TEXT NOT NULL DEFAULT '',
  archived    INTEGER NOT NULL DEFAULT 0,
  external_id TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE subscriptions (
  id            INTEGER PRIMARY KEY,
  owner_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'expense',        -- expense | income | transfer
  amount        INTEGER NOT NULL DEFAULT 0,             -- expected, minor units of currency
  amount_max    INTEGER NOT NULL DEFAULT 0,             -- 0 = fixed amount
  currency      TEXT NOT NULL DEFAULT 'EUR',
  account_id    INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  to_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  category_id   INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  tags          TEXT NOT NULL DEFAULT '[]',             -- JSON array of tag names
  payee         TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  freq          TEXT NOT NULL DEFAULT 'monthly',        -- daily | weekly | monthly | yearly
  every         INTEGER NOT NULL DEFAULT 1,
  start_date    TEXT NOT NULL DEFAULT '',
  next_date     TEXT NOT NULL DEFAULT '',               -- the next due date not yet paid
  end_date      TEXT NOT NULL DEFAULT '',
  mode          TEXT NOT NULL DEFAULT 'remind',         -- remind | auto (book it on the day) | track (only match)
  notify_days   INTEGER NOT NULL DEFAULT -1,            -- remind N days before; -1 = off
  match_text    TEXT NOT NULL DEFAULT '',               -- words that link a transaction to it
  url           TEXT NOT NULL DEFAULT '',               -- manage / cancel page
  icon          TEXT NOT NULL DEFAULT '',
  notes         TEXT NOT NULL DEFAULT '',
  active        INTEGER NOT NULL DEFAULT 1,
  notified_for  TEXT NOT NULL DEFAULT '',               -- the due date the last reminder was for
  external_id   TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- One row per money movement. amount is always positive and in the account's
-- currency; kind says which way it goes. A transfer also carries to_amount in
-- the destination's currency. foreign_* is what the receipt said when it was
-- paid in another currency. amount_home is the value in the home currency at
-- that day's rate, so reports never have to look rates up again.
CREATE TABLE transactions (
  id               INTEGER PRIMARY KEY,
  kind             TEXT NOT NULL DEFAULT 'expense',     -- expense | income | transfer
  date             TEXT NOT NULL,
  time             TEXT NOT NULL DEFAULT '',            -- HH:MM wall clock
  account_id       INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  amount           INTEGER NOT NULL DEFAULT 0,
  to_account_id    INTEGER REFERENCES accounts(id) ON DELETE CASCADE,
  to_amount        INTEGER NOT NULL DEFAULT 0,
  foreign_amount   INTEGER NOT NULL DEFAULT 0,
  foreign_currency TEXT NOT NULL DEFAULT '',
  amount_home      INTEGER NOT NULL DEFAULT 0,
  fx_rate          REAL NOT NULL DEFAULT 1,             -- account currency → home currency
  description      TEXT NOT NULL DEFAULT '',
  payee            TEXT NOT NULL DEFAULT '',
  category_id      INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  notes            TEXT NOT NULL DEFAULT '',
  subscription_id  INTEGER REFERENCES subscriptions(id) ON DELETE SET NULL,
  source           TEXT NOT NULL DEFAULT 'manual',      -- manual | quick | template | ai | subscription | import | csv | api | firefly | adjustment
  external_id      TEXT NOT NULL DEFAULT '',
  lat              REAL,
  lng              REAL,
  created_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX tx_account_date ON transactions(account_id, date);
CREATE INDEX tx_to_account ON transactions(to_account_id);
CREATE INDEX tx_date ON transactions(date, time);
CREATE INDEX tx_category ON transactions(category_id);
CREATE INDEX tx_subscription ON transactions(subscription_id);
CREATE INDEX tx_external ON transactions(external_id);

CREATE TABLE transaction_tags (
  transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  tag_id         INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (transaction_id, tag_id)
);
CREATE INDEX tx_tags_tag ON transaction_tags(tag_id);

CREATE TABLE attachments (
  id             INTEGER PRIMARY KEY,
  transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  name           TEXT NOT NULL DEFAULT '',
  mime           TEXT NOT NULL DEFAULT '',
  size           INTEGER NOT NULL DEFAULT 0,
  path           TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX attachments_tx ON attachments(transaction_id);

-- One-tap entries ("Kafa", "cg hrana"). currency '' = the account's.
CREATE TABLE templates (
  id            INTEGER PRIMARY KEY,
  owner_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  aliases       TEXT NOT NULL DEFAULT '',               -- comma separated extra names for quick add
  kind          TEXT NOT NULL DEFAULT 'expense',
  amount        INTEGER,                                -- NULL = ask
  currency      TEXT NOT NULL DEFAULT '',
  account_id    INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  to_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  category_id   INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  tags          TEXT NOT NULL DEFAULT '[]',
  description   TEXT NOT NULL DEFAULT '',
  payee         TEXT NOT NULL DEFAULT '',
  notes         TEXT NOT NULL DEFAULT '',
  pinned        INTEGER NOT NULL DEFAULT 0,
  use_count     INTEGER NOT NULL DEFAULT 0,
  last_used_at  TEXT NOT NULL DEFAULT '',
  sort_order    INTEGER NOT NULL DEFAULT 0,
  external_id   TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- A budget caps spending in some categories (and/or tags) per period, in the
-- home currency. With rollover, what was left (or overspent) carries over.
CREATE TABLE budgets (
  id           INTEGER PRIMARY KEY,
  owner_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  amount       INTEGER NOT NULL DEFAULT 0,
  period       TEXT NOT NULL DEFAULT 'monthly',         -- weekly | monthly | yearly
  rollover     INTEGER NOT NULL DEFAULT 0,
  category_ids TEXT NOT NULL DEFAULT '[]',              -- JSON; children of a listed parent count too
  tag_names    TEXT NOT NULL DEFAULT '[]',
  start_date   TEXT NOT NULL DEFAULT '',
  alert_pct    INTEGER NOT NULL DEFAULT 80,
  notified     TEXT NOT NULL DEFAULT '',                -- "<period start>:<level>"
  color        TEXT NOT NULL DEFAULT '',
  icon         TEXT NOT NULL DEFAULT '',
  archived     INTEGER NOT NULL DEFAULT 0,
  sort_order   INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Savings goals. Linked to an account the saved amount is that balance;
-- otherwise it is the sum of the entries.
CREATE TABLE goals (
  id           INTEGER PRIMARY KEY,
  owner_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  target       INTEGER NOT NULL DEFAULT 0,
  currency     TEXT NOT NULL DEFAULT 'EUR',
  target_date  TEXT NOT NULL DEFAULT '',
  account_id   INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  icon         TEXT NOT NULL DEFAULT '',
  color        TEXT NOT NULL DEFAULT '',
  notes        TEXT NOT NULL DEFAULT '',
  archived     INTEGER NOT NULL DEFAULT 0,
  completed_at TEXT NOT NULL DEFAULT '',
  external_id  TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE goal_entries (
  id         INTEGER PRIMARY KEY,
  goal_id    INTEGER NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
  date       TEXT NOT NULL,
  amount     INTEGER NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Rules run on every new transaction (and on demand over old ones).
CREATE TABLE rules (
  id          INTEGER PRIMARY KEY,
  owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL DEFAULT '',
  match       TEXT NOT NULL DEFAULT 'all',              -- all | any
  conditions  TEXT NOT NULL DEFAULT '[]',
  actions     TEXT NOT NULL DEFAULT '{}',
  active      INTEGER NOT NULL DEFAULT 1,
  stop        INTEGER NOT NULL DEFAULT 0,
  sort_order  INTEGER NOT NULL DEFAULT 0,
  hits        INTEGER NOT NULL DEFAULT 0,
  last_hit_at TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE activity (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  action     TEXT NOT NULL,
  subject    TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE ai_usage (
  id                INTEGER PRIMARY KEY,
  feature           TEXT NOT NULL,
  model             TEXT NOT NULL DEFAULT '',
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  fetched_at TEXT NOT NULL
);

CREATE TABLE fx_cache (
  base   TEXT NOT NULL,
  quote  TEXT NOT NULL,
  date   TEXT NOT NULL,
  rate   REAL NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (base, quote, date)
);
