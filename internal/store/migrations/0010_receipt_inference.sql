-- Filling scanned receipts in: categories learned per shop and per product,
-- descriptions in the person's own words, shop locations.

-- A product's own category: chosen by the person or sorted by the AI.
ALTER TABLE products ADD COLUMN category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL;
ALTER TABLE products ADD COLUMN category_source TEXT NOT NULL DEFAULT ''; -- you | ai
ALTER TABLE products ADD COLUMN ai_checked INTEGER NOT NULL DEFAULT 0;    -- the AI was asked once

-- How the shop got its category, the description it books under, where it is.
ALTER TABLE merchants ADD COLUMN category_source TEXT NOT NULL DEFAULT ''; -- you | rule
ALTER TABLE merchants ADD COLUMN description TEXT NOT NULL DEFAULT '';     -- the person's own words, '' = automatic
ALTER TABLE merchants ADD COLUMN lat REAL;
ALTER TABLE merchants ADD COLUMN lng REAL;
ALTER TABLE merchants ADD COLUMN geocoded INTEGER NOT NULL DEFAULT 0;
UPDATE merchants SET category_source = 'you' WHERE category_id IS NOT NULL;

-- Why the receipt's transaction got its category, for people.
ALTER TABLE receipts ADD COLUMN category_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE receipt_settings ADD COLUMN ai_items INTEGER NOT NULL DEFAULT 0;  -- sort new products with the AI
ALTER TABLE receipt_settings ADD COLUMN own_words INTEGER NOT NULL DEFAULT 1; -- describe like the person does
ALTER TABLE receipt_settings ADD COLUMN geocode INTEGER NOT NULL DEFAULT 1;   -- put shops on the map
ALTER TABLE receipt_settings ADD COLUMN shop_rules TEXT NOT NULL DEFAULT '';  -- JSON [{keywords, category_id}]; '' = the defaults
