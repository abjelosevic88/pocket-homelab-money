-- The AI looks over every scanned receipt (shop, time, items, how the person
-- books) and picks its category and description. On by default; it only runs
-- when an AI is connected.
ALTER TABLE receipt_settings ADD COLUMN ai_review INTEGER NOT NULL DEFAULT 1;
