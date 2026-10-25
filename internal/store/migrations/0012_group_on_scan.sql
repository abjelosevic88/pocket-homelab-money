-- Group a receipt's new products right when it is scanned (same passes as
-- the daily check: keywords, similar products, AI when the check uses it).
ALTER TABLE receipt_settings ADD COLUMN group_on_scan INTEGER NOT NULL DEFAULT 1;
