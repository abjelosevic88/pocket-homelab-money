-- Monthly schedules pick their day explicitly instead of inheriting it from the start date.
ALTER TABLE subscriptions ADD COLUMN month_day INTEGER NOT NULL DEFAULT 0; -- 1..31 (31 = the last day of every month); 0 = the start date's day
ALTER TABLE subscriptions ADD COLUMN weekday INTEGER NOT NULL DEFAULT 0;   -- with nth: 1 = Monday … 7 = Sunday; 0 = the start date's weekday
