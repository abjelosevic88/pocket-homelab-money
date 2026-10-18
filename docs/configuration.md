# Configuration

Everything is read from environment variables; every value has a default so a bare `docker run -v ./data:/data` works. Integration values seed the settings on first start and can be changed later in the UI (the UI values win afterwards).

| Variable | Default | What it does |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL, used in notification links |
| `DATA_DIR` | `./data` (`/data` in the image) | SQLite database, receipts and backups |
| `DB_PATH` / `FILES_DIR` | `$DATA_DIR/pocket-money.db` / `$DATA_DIR/files` | Override locations |
| `TZ` | – | Timezone: decides what “today”, “yesterday” and a month are |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`, `warn`, `error`; `json` |
| `TRUST_PROXY` | `true` | Honour `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | Prometheus `/metrics` |
| `MAX_UPLOAD_BYTES` | `52428800` | Receipt / CSV upload limit |
| `AUTH_MODE` | `local` | `local` (built-in accounts), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (single user, LAN only) |
| `AUTH_PROXY_HEADER` / `AUTH_PROXY_EMAIL_HEADER` | `Remote-User` / `Remote-Email` | Headers for proxy mode |
| `SESSION_TTL` | `720h` | Cookie lifetime |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | Create the first admin on an empty database (otherwise the UI asks) |
| `DEFAULT_CURRENCY` | `EUR` | Home currency: reports, budgets, net worth |
| `FX_PROVIDER` | `auto` | `auto`, `frankfurter`, `erapi`, `none`. Euro pegs (BAM, BGN, XOF…) are built in and also give dated cross rates (USD→BAM = USD→EUR × 1.95583). |
| `NOTIFY_URL` / `NOTIFY_FORMAT` / `NOTIFY_HOUR` | – / guessed / `9` | Apprise `…/notify/<key>`, ntfy topic URL or JSON webhook; `apprise`, `ntfy`, `json`; hour of the daily check |
| `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL` | – / `gpt-5.4-mini` / OpenAI | Optional AI: free text and voice entry, “Ask” |
| `TRAVEL_URL` / `TRAVEL_TOKEN` / `TRAVEL_PUBLIC_URL` | – | Pocket Travel: new expenses during a running trip get its tag |
| `FIREFLY_URL` / `FIREFLY_TOKEN` | – | Firefly III base URL and personal access token, for the import |
| `PICO_URL` | – | Firefly Pico base URL, to import its templates (uses the Firefly token) |
| `FIREFLY_IMPORT_ON_START` | `true` | Import everything once when the database is still empty and a Firefly URL is set |
| `FIREFLY_MIRROR` | `false` | Keep pulling Firefly every 30 minutes (parallel run) |
| `USER_AGENT` | `PocketMoney/1.0 (…)` | Sent to the exchange-rate providers |
| `DEMO_DATA` | `false` | Seed half a year of a sample household on an empty database |

## Auth modes

- **local** — accounts with email + password. The first account created becomes admin.
- **proxy** — trust a forward-auth proxy (Authelia, Authentik, Tinyauth…). Users are created on first visit. Keep `/firefly/api/*` and `/cal/*` outside the forward-auth rule — their clients send a bearer token.
- **none** — no login at all; one implicit admin. Only for a trusted LAN.

Bearer tokens (Settings → API & apps) work in every mode, for the native API and the Firefly-compatible one.

## Settings in the UI

- **Month starts on day** — a “payday month” (e.g. 25 → 25th to 24th) for budgets, reports and the forecast.
- **Default account** — where quick add books unless told otherwise; empty = the one you used last.
- **Week start**, date and time formats, home currency (changing it re-values every transaction at its day's rate).

## CLI

```
pocket-money                       start the server
pocket-money backup [-o FILE]      consistent SQLite copy (VACUUM INTO)
pocket-money reset-password [-email USER] [-password NEW]
pocket-money version
```
