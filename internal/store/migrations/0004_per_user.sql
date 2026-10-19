-- Everything is per person. Accounts, subscriptions, templates, budgets,
-- goals, rules and webhooks already carried owner_id; categories, tags,
-- transactions, AI usage and the personal settings get an owner here. All
-- existing rows belong to the owner account: the first admin (else the
-- first user). Members never had a way to own these.
--
-- Runs with foreign keys switched off (see Store.migrate) so the tags table
-- can be rebuilt without its DROP cascading into transaction_tags.

-- categories: unique per person instead of per server
ALTER TABLE categories ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
UPDATE categories SET user_id = (SELECT id FROM users ORDER BY role != 'admin', id LIMIT 1) WHERE user_id IS NULL;
DROP INDEX IF EXISTS categories_name;
CREATE UNIQUE INDEX categories_name ON categories(user_id, lower(name), coalesce(parent_id, 0));
CREATE INDEX categories_user ON categories(user_id);

-- tags: the name was UNIQUE for the whole server (a column constraint, so
-- the table has to be rebuilt to make it unique per person)
CREATE TABLE tags_new (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL COLLATE NOCASE,
  color       TEXT NOT NULL DEFAULT '',
  archived    INTEGER NOT NULL DEFAULT 0,
  external_id TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO tags_new (id, user_id, name, color, archived, external_id, created_at)
  SELECT id, (SELECT id FROM users ORDER BY role != 'admin', id LIMIT 1), name, color, archived, external_id, created_at FROM tags;
DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;
CREATE UNIQUE INDEX tags_user_name ON tags(user_id, name COLLATE NOCASE);

-- transactions: the ledger owner is the owner of the account it is booked on
-- (created_by stays who typed it, e.g. a member on a shared account). The
-- triggers keep it right when a transaction moves to another account.
ALTER TABLE transactions ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
UPDATE transactions SET user_id = (SELECT a.owner_id FROM accounts a WHERE a.id = transactions.account_id);
CREATE INDEX tx_user ON transactions(user_id, date);
CREATE TRIGGER tx_user_insert AFTER INSERT ON transactions BEGIN
  UPDATE transactions SET user_id = (SELECT owner_id FROM accounts WHERE id = NEW.account_id) WHERE id = NEW.id;
END;
CREATE TRIGGER tx_user_move AFTER UPDATE OF account_id ON transactions BEGIN
  UPDATE transactions SET user_id = (SELECT owner_id FROM accounts WHERE id = NEW.account_id) WHERE id = NEW.id;
END;
CREATE TRIGGER account_owner_change AFTER UPDATE OF owner_id ON accounts BEGIN
  UPDATE transactions SET user_id = NEW.owner_id WHERE account_id = NEW.id;
END;

-- AI usage is counted per person
ALTER TABLE ai_usage ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
UPDATE ai_usage SET user_id = (SELECT id FROM users ORDER BY role != 'admin', id LIMIT 1) WHERE user_id IS NULL;
CREATE INDEX ai_usage_user ON ai_usage(user_id, created_at);

CREATE INDEX IF NOT EXISTS activity_user ON activity(user_id, id);
CREATE INDEX IF NOT EXISTS subscriptions_owner ON subscriptions(owner_id);
CREATE INDEX IF NOT EXISTS templates_owner ON templates(owner_id);
CREATE INDEX IF NOT EXISTS budgets_owner ON budgets(owner_id);
CREATE INDEX IF NOT EXISTS goals_owner ON goals(owner_id);
CREATE INDEX IF NOT EXISTS rules_owner ON rules(owner_id);
CREATE INDEX IF NOT EXISTS webhooks_owner ON webhooks(owner_id);
CREATE INDEX IF NOT EXISTS account_members_user ON account_members(user_id);

-- Personal settings (preferences, integration URLs/tokens/keys, reminders)
-- move out of the server-wide table. title, home_currency, fx_auto and
-- setup_complete stay server-wide.
CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);
INSERT INTO user_settings (user_id, key, value)
  SELECT (SELECT id FROM users ORDER BY role != 'admin', id LIMIT 1), key, value FROM settings
  WHERE EXISTS (SELECT 1 FROM users) AND key IN ('date_format', 'time_format', 'week_start', 'month_start_day', 'default_account_id', 'quick_lang',
    'firefly_url', 'firefly_token', 'firefly_mirror', 'pico_url', 'notify_url', 'notify_format', 'notify_hour', 'notify_digest',
    'ai_url', 'ai_key', 'ai_model', 'ai_transcribe_model', 'ai_language', 'ai_context',
    'travel_url', 'travel_token', 'travel_public_url', 'travel_auto_tag');
DELETE FROM settings WHERE EXISTS (SELECT 1 FROM users) AND key IN ('date_format', 'time_format', 'week_start', 'month_start_day', 'default_account_id', 'quick_lang',
    'firefly_url', 'firefly_token', 'firefly_mirror', 'pico_url', 'notify_url', 'notify_format', 'notify_hour', 'notify_digest',
    'ai_url', 'ai_key', 'ai_model', 'ai_transcribe_model', 'ai_language', 'ai_context',
    'travel_url', 'travel_token', 'travel_public_url', 'travel_auto_tag');

-- per-person caches: reminder bookkeeping and the Pocket Travel trip list
UPDATE cache SET key = key || ':' || (SELECT id FROM users ORDER BY role != 'admin', id LIMIT 1)
  WHERE key IN ('notify:last_day', 'notify:digest_day', 'travel:trips') AND EXISTS (SELECT 1 FROM users);
