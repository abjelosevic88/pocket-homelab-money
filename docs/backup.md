# Backup & restore

Everything lives in `DATA_DIR` (`/data` in the container):

- `pocket-money.db` (+ `-wal` / `-shm` while running) — accounts, transactions, categories, tags, subscriptions, budgets, goals, templates, rules, settings, users
- `files/<yyyy-mm>/…` — receipts
- `backups/` — where the CLI writes snapshots

## Consistent copies

Never copy a live WAL database file. Use one of:

- **UI**: Settings → Backup & data → *Download database*
- **API**: `GET /api/v1/backup` with an admin bearer token — only while the admin is the only person on the server (the file holds everyone's money); otherwise back up from the host and let each person use **Export** for their own data
- **CLI**: `docker exec pocket-money pocket-money backup` → prints the path under `/data/backups/`
- **sqlite3**: `sqlite3 data/pocket-money.db "VACUUM INTO 'snap.db'"`

Then back up `files/` with your usual tool (restic, Backrest, rsync). Exclude `pocket-money.db*` from raw file backups and include the snapshot instead.

## Restore

1. Stop the container.
2. Replace `data/pocket-money.db` with the snapshot (remove any stale `-wal` / `-shm`).
3. Restore `files/`.
4. Start the container. Migrations run forward automatically.

## Exports

Settings → Backup & data: every transaction as CSV (with the home-currency value), or everything as one JSON file.
