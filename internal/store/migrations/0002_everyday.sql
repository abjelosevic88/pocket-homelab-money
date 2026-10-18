-- Accounts whose money counts as "everyday" (safe to spend, liquid). Savings,
-- business or foreign-currency accounts can be left out.
ALTER TABLE accounts ADD COLUMN everyday INTEGER NOT NULL DEFAULT 1;
UPDATE accounts SET everyday = 0 WHERE kind IN ('savings', 'investment', 'loan');
