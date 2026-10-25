-- Fiscal receipts read from their QR codes.

-- Every scan, as it came in: the raw link, the tax office's raw answer and
-- what became of it. Rows are never rewritten by hand, so a parser fix can
-- re-read old answers.
CREATE TABLE receipt_scans (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider    TEXT NOT NULL DEFAULT '',  -- ba-rs | rs | me | hr; '' = not a receipt link
  country     TEXT NOT NULL DEFAULT '',
  url         TEXT NOT NULL,
  receipt_key TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending', -- pending | done | partial | duplicate | invalid | skipped | unsupported | failed
  message     TEXT NOT NULL DEFAULT '',
  attempts    INTEGER NOT NULL DEFAULT 0,
  next_try_at TEXT NOT NULL DEFAULT '',
  raw         TEXT NOT NULL DEFAULT '',
  receipt_id  INTEGER REFERENCES receipts(id) ON DELETE SET NULL,
  source      TEXT NOT NULL DEFAULT '',  -- web | camera | shortcut | quick | api
  account_id  INTEGER,                   -- the account asked for at scan time
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX receipt_scans_user ON receipt_scans(user_id, id);
CREATE INDEX receipt_scans_retry ON receipt_scans(status, next_try_at);

-- Shops, one per person and tax id. display_name/category/account are the
-- person's choices and survive later receipts.
CREATE TABLE merchants (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  country      TEXT NOT NULL DEFAULT '',
  tax_id       TEXT NOT NULL DEFAULT '',
  name         TEXT NOT NULL DEFAULT '',  -- as the tax office has it
  display_name TEXT NOT NULL DEFAULT '',  -- the payee on transactions
  address      TEXT NOT NULL DEFAULT '',
  city         TEXT NOT NULL DEFAULT '',
  category_id  INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  account_id   INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, country, tax_id)
);

CREATE TABLE receipts (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider       TEXT NOT NULL,
  country        TEXT NOT NULL DEFAULT '',
  receipt_key    TEXT NOT NULL,
  invoice_number TEXT NOT NULL DEFAULT '',
  merchant_id    INTEGER REFERENCES merchants(id) ON DELETE SET NULL,
  location       TEXT NOT NULL DEFAULT '',
  issued_at      TEXT NOT NULL DEFAULT '',  -- RFC3339 with the shop's offset
  date           TEXT NOT NULL DEFAULT '',  -- local YYYY-MM-DD
  time           TEXT NOT NULL DEFAULT '',  -- local HH:MM
  currency       TEXT NOT NULL DEFAULT '',
  total          INTEGER NOT NULL DEFAULT 0,
  tax_total      INTEGER NOT NULL DEFAULT 0,
  method         TEXT NOT NULL DEFAULT '',  -- cash | card | transfer | voucher | check | other
  payments       TEXT NOT NULL DEFAULT '[]',
  kind           TEXT NOT NULL DEFAULT 'sale',
  invoice_type   TEXT NOT NULL DEFAULT 'normal',
  referent       TEXT NOT NULL DEFAULT '',
  cashier        TEXT NOT NULL DEFAULT '',
  device         TEXT NOT NULL DEFAULT '',
  journal        TEXT NOT NULL DEFAULT '',
  url            TEXT NOT NULL DEFAULT '',
  complete       INTEGER NOT NULL DEFAULT 0,  -- shop and items known
  has_items      INTEGER NOT NULL DEFAULT 0,
  transaction_id INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, provider, receipt_key)
);
CREATE INDEX receipts_tx ON receipts(transaction_id);
CREATE INDEX receipts_user_date ON receipts(user_id, date);
CREATE INDEX receipts_device ON receipts(user_id, provider, device);

-- Product groups: "Milk" = every product whose name has one of the keywords.
CREATE TABLE product_groups (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  icon        TEXT NOT NULL DEFAULT '',
  keywords    TEXT NOT NULL DEFAULT '',
  category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  sort_order  INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, name)
);

-- Products: the same article across receipts (by folded name).
-- group_locked = the person put it in (or out of) a group by hand.
CREATE TABLE products (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key          TEXT NOT NULL,
  name         TEXT NOT NULL,
  unit         TEXT NOT NULL DEFAULT '',
  group_id     INTEGER REFERENCES product_groups(id) ON DELETE SET NULL,
  group_locked INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, key)
);
CREATE INDEX products_group ON products(group_id);

CREATE TABLE receipt_items (
  id         INTEGER PRIMARY KEY,
  receipt_id INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
  line       INTEGER NOT NULL DEFAULT 0,
  name       TEXT NOT NULL,
  norm       TEXT NOT NULL DEFAULT '',  -- folded name, for search
  gtin       TEXT NOT NULL DEFAULT '',
  quantity   REAL NOT NULL DEFAULT 1,
  unit       TEXT NOT NULL DEFAULT '',
  unit_price INTEGER NOT NULL DEFAULT 0,
  total      INTEGER NOT NULL DEFAULT 0,
  tax_label  TEXT NOT NULL DEFAULT '',
  product_id INTEGER REFERENCES products(id) ON DELETE SET NULL
);
CREATE INDEX receipt_items_receipt ON receipt_items(receipt_id);
CREATE INDEX receipt_items_product ON receipt_items(product_id);

-- Per-person receipt preferences.
CREATE TABLE receipt_settings (
  user_id         INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  card_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  cash_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  split_items     INTEGER NOT NULL DEFAULT 0, -- split a receipt by its items' group categories
  attach_manual   INTEGER NOT NULL DEFAULT 1  -- link to a matching hand-typed transaction instead of booking a second one
);
