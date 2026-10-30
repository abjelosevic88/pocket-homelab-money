-- The day review: once a day (00:00 by default) the AI looks over the past
-- day's bookings and flags categories, descriptions, duplicates and amounts
-- that look wrong. mode report only lists them; fix also corrects category
-- and description itself (duplicates and amounts are always left to the person).
CREATE TABLE day_review_settings (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  mode    TEXT NOT NULL DEFAULT 'report', -- off | report | fix
  hour    INTEGER NOT NULL DEFAULT 0,
  notify  INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE day_reviews (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day         TEXT NOT NULL,                  -- the day that was looked over
  trigger     TEXT NOT NULL DEFAULT 'daily',  -- daily | manual
  mode        TEXT NOT NULL DEFAULT 'report',
  started_at  TEXT NOT NULL,
  finished_at TEXT NOT NULL DEFAULT '',
  checked     INTEGER NOT NULL DEFAULT 0,
  note        TEXT NOT NULL DEFAULT '',       -- the AI's one-line summary
  error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX day_reviews_user ON day_reviews(user_id, id);

CREATE TABLE day_review_findings (
  id                 INTEGER PRIMARY KEY,
  review_id          INTEGER NOT NULL REFERENCES day_reviews(id) ON DELETE CASCADE,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  transaction_id     INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
  note               TEXT NOT NULL DEFAULT '',
  category_id        INTEGER REFERENCES categories(id) ON DELETE SET NULL, -- suggested
  description        TEXT NOT NULL DEFAULT '',                             -- suggested, '' = none
  duplicate_of       INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
  odd_amount         INTEGER NOT NULL DEFAULT 0,
  status             TEXT NOT NULL DEFAULT 'open', -- open | applied | fixed | ignored | undone | replaced
  before_category_id INTEGER,                      -- what applying replaced, for undo
  before_description TEXT NOT NULL DEFAULT '',
  updated_at         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX day_review_findings_review ON day_review_findings(review_id);
CREATE INDEX day_review_findings_open ON day_review_findings(user_id, status);
