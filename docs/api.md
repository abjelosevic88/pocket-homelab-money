# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie (the UI), a bearer token (`Authorization: Bearer pm_…`, create one under Settings → API & apps) or `X-API-Key`. Amounts are **integers in minor units** of their currency (`1250` = 12.50 EUR, `1500` = ¥1500). Dates are `YYYY-MM-DD`, times `HH:MM`.

## Auth & account

| | |
|---|---|
| `POST /auth/login` `{email, password}` · `POST /auth/logout` · `GET /auth/status` · `POST /auth/setup` | session |
| `GET/PUT /auth/me` | profile, password |
| `GET/POST /tokens` · `DELETE /tokens/{id}` | API tokens |
| `GET/POST/PUT/DELETE /users[/{id}]` | users (admin) |

### Pocket Core app links

Another Pocket app can act for a person here with a `pcx_` token from Pocket Core (see `docs/links.md` in pocket-homelab-core). Money offers three scopes:

| Scope | Routes |
|---|---|
| any | `GET /auth/me` |
| `accounts:read` | `GET /accounts`, `GET /accounts/{id}/categories`, `GET /categories` |
| `transactions:write` | `POST /transactions`; `GET` / `PUT` / `DELETE /transactions/{id}` only for transactions that app created |
| `transactions:read` | `GET /transactions`, `GET /transactions/{id}` (any transaction the person can see), `GET /tags`, `GET /accounts`, `GET /categories`; and on the [Firefly-compatible API](#firefly-iii-compatible-api) `GET /firefly/api/v1/` `about`, `about/user`, `accounts`, `accounts/{id}`, `accounts/{id}/transactions`, `categories`, `categories/{id}`, `tags`, `tags/{tag}`, `tags/{tag}/transactions`, `transactions`, `transactions/{id}`, `currencies`. Read only: no write route, native or Firefly, is open to it |

`transactions:read` is what Pocket Travel (trip costs: `/about`, `/tags`, `/tags/{id}/transactions?type=withdrawal&start=&end=`) and Pocket Day (the day's spending: `/about`, `/transactions?type=withdrawal&start=&end=`) use with their Firefly client pointed at `https://<money>/firefly`. Every other route answers `403` to a link token, on the Firefly-compatible API in Firefly's error shape. A transaction created this way gets `source` = the acting app's slug, and its `external_id` is prefixed with `<slug>:`. Audit entries read "<person> via Pocket Garage (Pocket Core link)". `POST /transactions` takes an optional `external_id` from any caller, except the reserved `firefly:` prefix.

## Accounts

| | |
|---|---|
| `GET /accounts[?archived=1]` | with `balance` (account currency) and `balance_home` |
| `POST /accounts` · `GET/PUT/DELETE /accounts/{id}` | `name, kind (checking/savings/cash/credit/investment/loan/mortgage/debt/other), currency, opening_balance, opening_date, institution, number, icon, color, credit_limit, include_net_worth, everyday, archived, notes`; liabilities also `direction (owe/owed), interest_rate (% a year), interest_auto, principal, payment, payment_day (1–28)` |
| `GET /accounts/{id}/history?from=&to=` | end-of-day balances |
| `POST /accounts/{id}/reconcile` `{balance, date}` | books the difference as an adjustment |
| `PUT /accounts/{id}/members` `{members:[{user_id, role: editor|viewer}]}` | share |
| `GET /accounts/{id}/audit?before=&limit=&actor=&action=` | the account's audit log, **owner only** (someone it is shared with gets 403, anyone else — admins included — 404): who added, edited or deleted what, newest first. Each entry: `actor_user_id, actor_name, actor_label` ("Sani via API token 'phone'", "Firefly mirror", "Auto-booking", "Interest"), `source` (web, api, firefly_api, quick, template, import, mirror, schedule, bulk, reconcile), `action` (transaction.created/updated/deleted, attachment.added/deleted, account.updated/shared/unshared/reconciled, import.summary), `transaction_id`, `summary`, `changes` (field → `{from, to}`; a delete keeps the full snapshot). `before` = the `next_before` cursor; `actor` = a user id or a job label; `action` = exact or a prefix (`transaction`). Also returns `actors` for filters |
| `GET /audit` (same filters, plus `account=`) | the audit logs of every account the caller owns, merged, each entry with `account_name` |
| `GET /accounts/{id}/categories` | the categories bookings on this account use: the owner's (a shared account books in its owner's categories; a caller's own category with the same name is mapped onto it) |
| `GET /accounts/{id}/payoff` | liabilities: `outstanding, principal, repaid, pct, payment (payment_estimated = 3-month average), month_interest, months_left, payoff_date, interest_left, total_left, never` and a 36-month `schedule` |
| `POST /accounts/{id}/interest` | books this month's interest (expense when you owe it, income when it is owed to you); with `interest_auto` it happens on the payment day |
| `PUT /accounts/reorder` `{ids}` | |

## Transactions

| | |
|---|---|
| `GET /transactions` | filters: `account, kind, category (ids, comma separated, or none), tag, from, to, q, payee, min, max (home minor units), subscription, source, product_group, product, receipt (1 = booked from a receipt), limit (≤1000), offset, oldest`. `q` also matches the item names of the transaction's receipt. Every item has `receipt_id` when it was booked from (or linked to) a scanned receipt. Returns `{items, totals:{count, income, expense, transfers, net}, has_more}`. With `account`, every item has `balance_after` — the account's balance after it. A category filter matches split parts and totals count only those parts |
| `POST /transactions` | `kind (expense/income/transfer), date, time, account_id, amount, currency, account_amount, to_account_id, to_amount, description, payee, category_id, tags, notes, subscription_id, lat, lng, template_id, skip_rules, splits` — see below |
| `GET/PUT/DELETE /transactions/{id}` · `POST /transactions/{id}/duplicate` | |
| `POST /transactions/bulk` `{ids, action: delete|category|add_tags|remove_tags, category_id, tags}` | |
| `POST /transactions/{id}/attachments` (multipart `file`) · `GET/DELETE /attachments/{id}` | receipts |
| `GET /suggest?q=` · `GET /payees` | autocomplete with the usual category |

**Currencies.** `amount` is in `currency` (default: the account's). When it differs, it is kept as the foreign amount and converted at that day's rate into the account's currency — or pass `account_amount` with what the bank actually charged. A transfer between currencies converts `to_amount` the same way unless given. Every transaction stores `amount_home` at the day's rate, so reports never look rates up again.

**Splits.** `splits: [{category_id, amount, note}, …]` (two or more) puts one receipt across categories. Part amounts are in the transaction's `currency` and must add up to `amount`; the transaction's own `category_id` becomes the biggest part's. On PUT, `splits: []` removes the split; leaving `splits` out keeps the parts and rescales them when the amount changes. Transfers cannot be split. Reports, budgets and the category filter count each part under its own category.

**Pipeline on create:** rules → running Pocket Travel trip tag (see [Pocket Travel](#pocket-travel)) → insert → link to the subscription it pays (name/match words, amount ±20 %, around the due date) → template use counter.

## Fiscal receipts

| | |
|---|---|
| `POST /receipts/scan` `{url, account_id?, source?}` (JSON or a form) | reads a receipt's QR link, books it; `?format=text` answers one line for a phone notification. Returns `{status, message, scan, receipt, transaction}` with status `booked` (201) · `linked` (to a matching hand-typed transaction, 201) · `partial` (booked from the QR code alone, the tax office is retried, 201) · `duplicate` · `pending` · `skipped` (proforma/training) · `invalid` (422) · `unsupported` (422) · `failed` |
| `GET /receipts/providers` | supported countries |
| `GET /receipts` `?q, merchant, from, to, limit, offset` · `GET /receipts/{id}` · `DELETE /receipts/{id}[?transaction=1]` | receipts with items, shop and payments |
| `POST /receipts/{id}/book` `{account_id?}` | book a receipt whose transaction was deleted |
| `POST /receipts/{id}/split[?undo=1]` | split its transaction by the categories of the items' product groups (or join it back) |
| `GET /transactions/{id}/receipt` | the receipt behind a transaction |
| `GET /receipts/scans` `?status, limit, offset` · `POST /receipts/scans/{id}/retry` · `GET /receipts/scans/{id}/raw` | the scan log, every scan as it came in, with the tax office's raw answer |
| `GET/PUT /receipts/settings` | `card_account_id, cash_account_id, split_items, attach_manual, ai_items, own_words, geocode, shop_rules [{keywords, category_id}]` (`reset_shop_rules: true` = back to the suggested rules); PUT changes only the keys sent. GET adds `ai_ready, ai_detail, unsorted_products, shop_rules_default` |
| `POST /receipts/{id}/apply` | set its transaction's category (and, when booked from the receipt, description) to what the receipt says now — `guess` on `GET /receipts/{id}` — without teaching the shop |
| `POST /products/sort` | ask the AI about every product still without a category (needs `ai_items`) |
| `GET /merchants` · `PUT /merchants/{id}` `{display_name, description, category_id, account_id, apply}` | shops; `apply` also renames and re-categorises what was booked from them |

**How a scan is booked.** The link is decoded offline first (Serbia/Republika Srpska: receipt number, total and time are in the code; Montenegro and Croatia: in the URL). The receipt is fetched from the tax office (`/v/?vl=…` with `Accept: application/json`, Montenegro's `/ic/api/verifyInvoice`); a receipt already stored is a duplicate. The account is: the one passed, else the shop's, else the card or cash account from the settings (cash: the first cash account), else the default. The category is, in order: the shop's category the person chose (in Shops, or by changing the category of one of its booked transactions); the shop's category from a shop rule matching its registered name (`apoteka` → the person's pharmacy category); the category most of the money went to among the items (at least 40 %) — each product's own category (set by the person, or sorted once by the AI) or its group's; what that payee was booked as before. Why is stored as `category_reason`. The description is the shop's own words (set in Shops, or by changing the description of one of its bookings), else — with `own_words` — the person's most used description for that category over the last year, else the shop's name. Shop names, addresses and item names are written in Latin letters. With `geocode` the shop's address is looked up once on OpenStreetMap and its transactions get `lat`/`lng`.

**AI sorting** (`ai_items`, off by default): before booking, products with no category of their own or from their group are sent once — names only, with the category list and up to 30 of the person's “description → category” pairs as examples — to the person's AI the way the rest of the app picks it: Pocket AI when this app's Core link is on, else their own OpenAI key. Each product is asked about once; the answer is remembered and changeable. With `attach_manual`, an unlinked expense with the same amount (in the receipt's currency) within a day is linked instead of booking a second one, and its empty payee/category are filled in. Scans the tax office could not answer are retried by the background job after 10 min, 30 min, 2 h, 6 h, 12 h, then daily, eight times.

**Adding a country** is one file in `internal/fiscal` implementing `Provider` (`Match`, `Decode`, `Fetch`, `Parse`) and a line in the `providers` list.

## Products (receipt items)

| | |
|---|---|
| `GET /products/groups` `?from, to, merchant` | every group with `purchases, quantity, unit, spent (home), avg_price, products, last_date`; “no group” is `key: 0` |
| `POST /products/groups` `{name, icon, keywords, category_id}` · `PUT/DELETE /products/groups/{id}` · `POST /products/groups/starter` | groups; saving regroups the products not placed by hand (`moved`) |
| `GET /products` `?group (id or none), merchant, from, to, q` · `PUT /products/{id}` `{name, group_id, auto}` | products with the same statistics; `group_id` places one by hand, `auto` hands it back to keyword matching |
| `GET /products/purchases` `?group, product, merchant, from, to` | every line, newest first (price history) |
| `GET /products/summary` `?product_group | product, from, to` | one total, for a filtered transaction list |

A product is an item name folded to lowercase Latin without diacritics and without the unit suffix (`MLIJEKO 2.8% 1L/KO` → `mlijeko 2.8% 1l`). A product joins the group whose keyword starts a word in its name; the longest matching keyword wins.

## Quick add

| | |
|---|---|
| `POST /quick/parse` `{text}` | one draft per line (`;` or newline): kind, amount, currency, date, account, category, tags, template, what was recognised, `account_amount`, `ready` |
| `POST /quick` `{text}` or `{drafts}` | books them; a line that is a fiscal receipt link is scanned like `POST /receipts/scan` |

Grammar: `[template or words] amount[currency] [currency word] [date] [@account] [>to-account] [#tag]`, e.g. `kafa 3,5`, `cg hrana 20 eur`, `+1500 plata`, `gorivo 80 juče`, `taxi 12 usd @cash #lisbon`, `100 >savings`. Dates: `today/danas`, `yesterday/juče`, `prekjuče`, weekday names, `-2d`, `12.10.`, `2026-10-12`. Templates match by the start of their name or an alias.

## Catalog

| | |
|---|---|
| `GET/POST /categories` · `PUT/DELETE /categories/{id}[?move_to=]` | `name, kind (expense/income/both), icon, color, parent_id, archived` |
| `GET/POST /tags` · `PUT/DELETE /tags/{id}` · `POST /tags/{id}/merge {into}` | |
| `GET/POST /templates` · `PUT/DELETE /templates/{id}` · `POST /templates/{id}/use {amount?, date?, description?}` | one-tap entries |
| `GET/POST /rules` · `PUT/DELETE /rules/{id}` · `POST /rules/test` · `POST /rules/{id}/apply {since, force}` · `PUT /rules/reorder` | conditions on `text, description, payee, notes, amount, account, kind, currency` with `contains, not_contains, equals, starts, ends, regex, gt, lt, between`; actions `category_id, add_tags, payee, description, append_note` |

## Subscriptions & bills

| | |
|---|---|
| `GET /subscriptions` | items with `status, days_left, monthly_home, yearly_home, last_paid, last_amount, paid_count`, totals and `upcoming` (60 days) |
| `POST /subscriptions` · `GET/PUT/DELETE /subscriptions/{id}` | `name, kind, amount, amount_max, currency, account_id, to_account_id, category_id, tags, payee, description, freq (daily/weekly/monthly/yearly), every, start_date, next_date, end_date, mode (remind/auto/track), notify_days (-1 = off), match_text, url, icon, notes, active, weekend ('' / before / after / skip), nth (monthly: 1–4 = that weekday of the month, 5 = the last one), max_count (stop after N payments, 0 = no limit)`. Responses add `due_date` (after the weekend rule) and `done_count` |
| `POST /subscriptions/{id}/pay {amount?, date?, account_id?}` · `POST …/skip` · `POST …/link {transaction_id, unlink?} or {relink:true}` | |
| `GET /subscriptions/detect` | repeating payments that are not tracked yet |
| `GET /cal/subscriptions.ics?token=` | every payment for a year, for calendar apps |

## Webhooks

| | |
|---|---|
| `GET /webhooks` | your webhooks (secrets masked) and the list of `events` |
| `POST /webhooks` · `PUT/DELETE /webhooks/{id}` | `name, url, events, format (native/firefly), secret (empty = generated, returned once), active` |
| `POST /webhooks/{id}/test` · `GET /webhooks/{id}/deliveries` | send a ping now · the last 50 attempts |

Events: `transaction.created`, `transaction.updated`, `transaction.deleted` (from the app, the API, imports, rules, the Firefly mirror), `budget.alert`, `subscription.due`. Native body: `{"id", "event", "created_at", "data": {"transaction": …}}`. Headers: `X-Pocket-Event`, `X-Pocket-Signature: sha256=<hex HMAC-SHA256(secret, body)>`. The `firefly` format sends Firefly III's webhook body and `Signature: t=<unix>,v1=<hex HMAC-SHA3-256(secret, "<unix>.<body>")>`, for transaction events only. Delivery is retried after 5 s and 30 s.

## Budgets & goals

| | |
|---|---|
| `GET /budgets?date=` | with `status: from, to, spent, carry, available, left, pct, elapsed_pct, projected, daily_left, state (ok/warn/over), history` |
| `POST /budgets` · `GET/PUT/DELETE /budgets/{id}` | `name, amount (home currency), period, rollover, category_ids, tag_names, start_date, alert_pct` |
| `GET /budgets/suggest` | from the last three months |
| `GET/POST /goals` · `GET/PUT/DELETE /goals/{id}` · `POST /goals/{id}/entries {amount, date, note}` · `DELETE /goals/{id}/entries/{entry}` | `target, currency, target_date, account_id` (the account balance is what is saved) |

## Reports

`GET /reports/overview?month=YYYY-MM` (or `from`/`to`), `/reports/flow?months=`, `/reports/categories?month=&kind=`, `/reports/tags?all=1`, `/reports/networth?months=`, `/reports/calendar?year=`, `/reports/forecast`, `/reports/insights`. A month follows the “month starts on day” setting.

## AI (optional)

`POST /ai/parse {text}` → drafts (then `POST /quick {drafts}`), `POST /ai/transcribe` (multipart `file`) → `{text}`, `POST /ai/ask {question}` → `{answer}`, `GET /ai/usage`.

## Import & export

`POST /import/firefly {mode: all|transactions, since?, wait?}` · `GET /import/firefly/status` · `POST /import/csv/preview` (multipart `file`) · `POST /import/csv` (multipart `file, mapping, account_id, dry_run`) · `GET /export/transactions.csv` (same filters as the list) · `GET /export/money.json` · `GET /backup`.

## Dashboard (Homepage widget)

`GET /api/v1/dashboard` with a bearer token returns a flat object:

```json
{"currency":"BAM","accounts":4,"net_worth":79912.3,"net_worth_text":"79912.30 BAM","month_spent":1928.99,"month_spent_text":"1928.99 BAM",
 "month_income":0,"safe_per_day":789.13,"safe_per_day_text":"789.13 BAM","bills_left":25,"bills_left_text":"25.00 BAM",
 "projected_end":10569.96,"days_left":13,"next_bill_text":"Phone 25.00 BAM · 2026-10-20","budgets":4,"budgets_over":0,"budgets_warn":1}
```

```yaml
- Money:
    href: https://money.example.com
    icon: https://money.example.com/favicon.svg
    widget:
      type: customapi
      url: http://pocket-money:8080/api/v1/dashboard
      headers: { Authorization: Bearer pm_… }
      mappings:
        - { field: net_worth_text, label: Net worth }
        - { field: month_spent_text, label: This month }
        - { field: safe_per_day_text, label: Safe / day }
        - { field: next_bill_text, label: Next bill }
```

## Pocket Travel

While a trip is running in the person's Pocket Travel, a new expense gets the trip's tag (setting `travel_auto_tag`). Each person chooses how Money reaches *their* Travel, under Settings → Integrations (`PUT /settings {travel_mode}`, saved per person):

| `travel_mode` | |
|---|---|
| `core` | through Pocket Core: Money acts for the person with an app link (`uses: travel trips:read`), asking Core for a fresh five-minute `pcx_` token on every call. Nothing to paste; the link can be switched off box-wide (Core → Links) or by the person (Core → Settings → Apps acting for me) |
| `direct` | `travel_url` (empty = found through Core) + `travel_token`, a Pocket Travel API token; `travel_public_url` is what the browser opens |
| `off` | Money does not talk to Travel |

Unset, it is `direct` when a token is saved (an existing pasted token keeps working) and `core` otherwise. `GET /settings` answers `travel_mode` (the mode in effect), `travel_via` (`core` / `direct` / `""` = not connected), `travel_detail` (why not, e.g. "you switched the Money → Pocket Travel link off in Pocket Core") and `core_configured`. `POST /integrations/test {which: "travel"}` tests the mode in effect (a token typed into the form is tried as is). Disconnect = `PUT /settings {travel_url: "", travel_token: "", travel_public_url: ""}`. The trip list is kept 30 minutes per person (a failure 5 minutes) and forgotten whenever settings are saved; `GET /travel/trips` returns `{trips, public_url, running}`. `TRAVEL_URL` / `TRAVEL_TOKEN` / `TRAVEL_PUBLIC_URL` seed the owner's settings once: cleared in Settings, they do not come back at the next start.

With Pocket Core, Money reports every person's mode in effect for `travel` to Core (`PUT /api/v1/links/modes`, one batch) right after registering, after a mode change or disconnect, and every 30 minutes, so Core's Links page shows Direct connections too; a failed report is only logged.

## Firefly III-compatible API

Mounted at `/firefly/api/v1` with a Pocket Money bearer token, shaped like Firefly III 6.x so existing clients only change their base URL to `https://money.example.com/firefly`:

| Endpoint | Notes |
|---|---|
| `GET /about`, `/about/user` | |
| `GET /accounts?type=asset|liabilities|all`, `/accounts/{id}`, `/accounts/{id}/transactions` | `current_balance`, `account_role` |
| `GET /categories`, `/categories/{id}`, `/tags`, `/tags/{id or name}`, `/tags/{tag}/transactions?type=&start=&end=` | |
| `GET /transactions?type=&start=&end=&page=&limit=`, `/transactions/{id}` | one group = one split; `transaction_journal_id` = id |
| `POST /transactions` `{transactions:[{type, date, amount, description, source_id/name, destination_id/name, category_id/name, tags, notes, foreign_amount, foreign_currency_code, currency_code, bill_id}]}` | rules apply unless `apply_rules:false`; Pico's `[currency:XXX]` note markers are dropped; an optional top-level `template_id` (not Firefly's) counts one use of that template for the whole group |
| `PUT /transactions/{id}`, `DELETE /transactions/{id}` | |
| `GET /bills` (and `/subscriptions`) | `pay_dates`, `next_expected_match`, `repeat_freq`, a `notify: N` line in `notes` |
| `GET /currencies`, `/exchange-rates`, `/insight/expense/total`, `/insight/income/total` | |
| `GET /budgets`, `/piggy-banks`, `/rules`, `/recurrences` | empty lists (use the native API) |

Each split carries `external_id` (Money's own: `firefly:<journal id>` for what came from Firefly, `<app>:…` for what a linked app booked; `null` otherwise) and `original_source: "pocket-money-<version>"`; `GET /about` has `php_version: "pocket-money <version>"`, which is how a client tells Money from a real Firefly III. A Pocket Core link token with `transactions:read` works here too (see [Pocket Core app links](#pocket-core-app-links)).

Firefly Pico's template list is at `/firefly/api/transaction-templates` (Pico's shape, with `[currency:XXX]` in the notes when a template has its own currency), most-used first: pinned, then `use_count`, then your own order. Each item also has `pinned`, `use_count` and `last_used_at`, which Pico doesn't send; a client that sees `use_count` should keep this order and send `template_id` when it books from a template.

## Health & metrics

`GET /healthz`, `/readyz`, `/metrics` (Prometheus: build info, uptime, requests, rows per table).
