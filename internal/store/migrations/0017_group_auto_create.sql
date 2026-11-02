-- The AI's new groups are created right away instead of waiting in the
-- check's report for a click ("Kifla" sat without a group while "Bakery" was
-- suggested three times). Off = suggest only, as before.
ALTER TABLE receipt_settings ADD COLUMN group_auto_create INTEGER NOT NULL DEFAULT 1;

-- Products the AI already looked at and left without a group are asked once
-- more, so the groups it suggested for them now get made.
UPDATE products SET group_ai_checked = 0 WHERE group_id IS NULL AND group_locked = 0;
