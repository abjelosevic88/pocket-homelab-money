-- The daily product check: products without a group are placed by
-- keywords, by similar products already grouped, or by the AI, and every
-- run leaves a report.

-- How a product got its group: '' keywords | you | similar | ai.
-- similar and ai are locked like a hand placement, so keyword regrouping
-- does not undo them; "back to keywords" clears them.
ALTER TABLE products ADD COLUMN group_source TEXT NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN group_ai_checked INTEGER NOT NULL DEFAULT 0;
UPDATE products SET group_source = 'you' WHERE group_locked = 1;

ALTER TABLE receipt_settings ADD COLUMN group_job TEXT NOT NULL DEFAULT 'keywords'; -- off | keywords | ai
ALTER TABLE receipt_settings ADD COLUMN group_job_hour INTEGER NOT NULL DEFAULT 4;
ALTER TABLE receipt_settings ADD COLUMN group_job_notify INTEGER NOT NULL DEFAULT 1;

CREATE TABLE product_check_runs (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  trigger         TEXT NOT NULL DEFAULT 'daily', -- daily | manual
  mode            TEXT NOT NULL DEFAULT '',
  started_at      TEXT NOT NULL,
  finished_at     TEXT NOT NULL DEFAULT '',
  checked         INTEGER NOT NULL DEFAULT 0,
  by_keywords     INTEGER NOT NULL DEFAULT 0,
  by_similar      INTEGER NOT NULL DEFAULT 0,
  by_ai           INTEGER NOT NULL DEFAULT 0,
  categorized     INTEGER NOT NULL DEFAULT 0,
  still_ungrouped INTEGER NOT NULL DEFAULT 0,
  report          TEXT NOT NULL DEFAULT '{}', -- {moves, suggestions, left}
  error           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX product_check_runs_user ON product_check_runs(user_id, id);
