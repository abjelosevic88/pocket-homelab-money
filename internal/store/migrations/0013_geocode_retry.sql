-- Shops whose address was not found get one more try with the better
-- address clean-up (no "Ulica", "7 A" → "7a", street-only fallback).
UPDATE merchants SET geocoded = 0 WHERE lat IS NULL;
