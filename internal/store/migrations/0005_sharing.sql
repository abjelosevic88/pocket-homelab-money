-- Owner-controlled sharing. A template is private until its owner switches
-- Share on; then people who can book on its account (and destination) may use
-- it. A category hidden from sharing cannot be picked by anyone but its owner,
-- even on a shared account. Visibility is computed at read time.
ALTER TABLE templates ADD COLUMN shared INTEGER NOT NULL DEFAULT 0;
ALTER TABLE categories ADD COLUMN share_hidden INTEGER NOT NULL DEFAULT 0;
