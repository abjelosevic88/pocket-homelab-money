# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie (the UI), a bearer token (`Authorization: Bearer pm_…`, create one under Settings → API & apps) or `X-API-Key`. Amounts are **integers in minor units** of their currency (`1250` = 12.50 EUR, `1500` = ¥1500). Dates are `YYYY-MM-DD`, times `HH:MM`.

## Auth & account

| | |
|---|---|
| `POST /auth/login` `{email, password}` · `POST /auth/logout` · `GET /auth/status` · `POST /auth/setup` | session |
| `GET/PUT /auth/me` | profile, password |
| `GET/POST /tokens` · `DELETE /tokens/{id}` | API tokens |
| `GET/POST/PUT/DELETE /users[/{id}]` | users (admin) |

## Accounts

| | |
|---|---|
| `GET /accounts[?archived=1]` | with `balance` (account currency) and `balance_home` |
| `POST /accounts` · `GET/PUT/DELETE /accounts/{id}` | `name, kind (checking/savings/cash/credit/investment/loan/other), currency, opening_balance, opening_date, institution, number, icon, color, credit_limit, include_net_worth, archived, notes` |
| `GET /accounts/{id}/history?from=&to=` | end-of-day balances |
| `POST /accounts/{id}/reconcile` `{balance, date}` | books the difference as an adjustment |
| `PUT /accounts/{id}/members` `{members:[{user_id, role: editor|viewer}]}` | share |
| `PUT /accounts/reorder` `{ids}` | |

## Transactions

| | |
|---|---|
| `GET /transactions` | filters: `account, kind, category (ids, comma separated, or none), tag, from, to, q, payee, min, max (home minor units), subscription, source, limit (≤1000), offset, oldest`. Returns `{items, totals:{count, income, expense, transfers, net}, has_more}` |
| `POST /transactions` | `kind (expense/income/transfer), date, time, account_id, amount, currency, account_amount, to_account_id, to_amount, description, payee, category_id, tags, notes, subscription_id, lat, lng, template_id, skip_rules` — see below |
| `GET/PUT/DELETE /transactions/{id}` · `POST /transactions/{id}/duplicate` | |
| `POST /transactions/bulk` `{ids, action: delete|category|add_tags|remove_tags, category_id, tags}` | |
| `POST /transactions/{id}/attachments` (multipart `file`) · `GET/DELETE /attachments/{id}` | receipts |
| `GET /suggest?q=` · `GET /payees` | autocomplete with the usual category |

**Currencies.** `amount` is in `currency` (default: the account's). When it differs, it is kept as the foreign amount and converted at that day's rate into the account's currency — or pass `account_amount` with what the bank actually charged. A transfer between currencies converts `to_amount` the same way unless given. Every transaction stores `amount_home` at the day's rate, so reports never look rates up again.

**Pipeline on create:** rules → running Pocket Travel trip tag → insert → link to the subscription it pays (name/match words, amount ±20 %, around the due date) → template use counter.

## Quick add

| | |
|---|---|
| `POST /quick/parse` `{text}` | one draft per line (`;` or newline): kind, amount, currency, date, account, category, tags, template, what was recognised, `account_amount`, `ready` |
| `POST /quick` `{text}` or `{drafts}` | books them |

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
| `POST /subscriptions` · `GET/PUT/DELETE /subscriptions/{id}` | `name, kind, amount, amount_max, currency, account_id, to_account_id, category_id, tags, payee, description, freq (daily/weekly/monthly/yearly), every, start_date, next_date, end_date, mode (remind/auto/track), notify_days (-1 = off), match_text, url, icon, notes, active` |
| `POST /subscriptions/{id}/pay {amount?, date?, account_id?}` · `POST …/skip` · `POST …/link {transaction_id, unlink?} or {relink:true}` | |
| `GET /subscriptions/detect` | repeating payments that are not tracked yet |
| `GET /cal/subscriptions.ics?token=` | every payment for a year, for calendar apps |

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

## Firefly III-compatible API

Mounted at `/firefly/api/v1` with a Pocket Money bearer token, shaped like Firefly III 6.x so existing clients only change their base URL to `https://money.example.com/firefly`:

| Endpoint | Notes |
|---|---|
| `GET /about`, `/about/user` | |
| `GET /accounts?type=asset|liabilities|all`, `/accounts/{id}`, `/accounts/{id}/transactions` | `current_balance`, `account_role` |
| `GET /categories`, `/categories/{id}`, `/tags`, `/tags/{id or name}`, `/tags/{tag}/transactions?type=&start=&end=` | |
| `GET /transactions?type=&start=&end=&page=&limit=`, `/transactions/{id}` | one group = one split; `transaction_journal_id` = id |
| `POST /transactions` `{transactions:[{type, date, amount, description, source_id/name, destination_id/name, category_id/name, tags, notes, foreign_amount, foreign_currency_code, currency_code, bill_id}]}` | rules apply unless `apply_rules:false`; Pico's `[currency:XXX]` note markers are dropped |
| `PUT /transactions/{id}`, `DELETE /transactions/{id}` | |
| `GET /bills` (and `/subscriptions`) | `pay_dates`, `next_expected_match`, `repeat_freq`, a `notify: N` line in `notes` |
| `GET /currencies`, `/exchange-rates`, `/insight/expense/total`, `/insight/income/total` | |
| `GET /budgets`, `/piggy-banks`, `/rules`, `/recurrences` | empty lists (use the native API) |

Firefly Pico's template list is at `/firefly/api/transaction-templates` (Pico's shape, with `[currency:XXX]` in the notes when a template has its own currency).

## Health & metrics

`GET /healthz`, `/readyz`, `/metrics` (Prometheus: build info, uptime, requests, rows per table).
