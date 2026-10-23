export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface Currency { code: string; name: string; decimals: number }
export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

/** How Money reaches another Pocket app for a person: through Pocket Core, with their own URL + token, or not at all. */
export type ConnMode = 'core' | 'direct' | 'off'

export interface Settings {
  ai_provider: string; ai_pocket_model: string; ai_status: { provider: string; selected: string; ready: boolean; detail: string }; ai_transcribe_configured: boolean

  title: string; home_currency: string; date_format: string; time_format: string; week_start: number; month_start_day: number
  fx_auto: boolean; fx_provider: string; default_account_id: number; quick_lang: string
  firefly_url: string; firefly_token: string; firefly_configured: boolean; firefly_mirror: boolean; pico_url: string
  notify_url: string; notify_format: string; notify_hour: number; notify_digest: boolean; notify_configured: boolean
  ai_url: string; ai_key: string; ai_model: string; ai_model_effective: string; ai_transcribe_model: string; ai_language: string; ai_context: string; ai_configured: boolean
  travel_url: string; travel_public_url: string; travel_token: string; travel_configured: boolean; travel_auto_tag: boolean
  travel_mode: ConnMode; travel_via: '' | 'core' | 'direct'; travel_detail: string; core_configured: boolean; core_registered?: boolean
  max_upload_bytes: number; setup_complete: boolean; base_url: string
}

export type AccountKind = 'checking' | 'savings' | 'cash' | 'credit' | 'investment' | 'loan' | 'mortgage' | 'debt' | 'other'
export const LIABILITY: AccountKind[] = ['loan', 'mortgage', 'debt']
export interface AccountMember { user_id: number; name: string; email: string; role: 'editor' | 'viewer' }
export interface Account {
  id: number; owner_id: number; name: string; kind: AccountKind; currency: string; opening_balance: number; opening_date: string
  institution: string; number: string; color: string; icon: string; credit_limit: number; include_net_worth: boolean; everyday: boolean; archived: boolean
  sort_order: number; notes: string; external_id: string; created_at: string; updated_at: string
  direction: 'owe' | 'owed'; interest_rate: number; interest_auto: boolean; principal: number; payment: number; payment_day: number; interest_booked: string
  role?: 'owner' | 'editor' | 'viewer'; members?: AccountMember[]; balance: number; balance_home: number; tx_count: number; last_tx_date: string
}

export interface Category { id: number; parent_id: number | null; name: string; kind: 'expense' | 'income' | 'both'; icon: string; color: string; archived: boolean; sort_order: number; external_id: string; tx_count: number; full_name: string; share_hidden: boolean }
export interface Tag { id: number; name: string; color: string; archived: boolean; tx_count: number; last_used: string; created_at: string }

export type Kind = 'expense' | 'income' | 'transfer'
export interface Attachment { id: number; transaction_id: number; name: string; mime: string; size: number; created_at: string }
export interface Transaction {
  id: number; kind: Kind; date: string; time: string; account_id: number; amount: number; to_account_id: number | null; to_amount: number
  foreign_amount: number; foreign_currency: string; amount_home: number; fx_rate: number; description: string; payee: string
  category_id: number | null; notes: string; subscription_id: number | null; source: string; external_id: string; lat: number | null; lng: number | null
  created_by: number | null; created_at: string; updated_at: string
  tags: string[]; account_name: string; currency: string; to_account_name?: string; to_currency?: string
  category_name: string; category_icon: string; category_color: string; subscription_name?: string; created_by_name?: string
  attachments?: Attachment[]; attachment_count: number; splits: Split[] | null; balance_after?: number
}
export interface Split { id: number; category_id: number | null; amount: number; foreign_amount: number; amount_home: number; note: string; category_name: string; category_icon: string }
export interface TxTotals { count: number; income: number; expense: number; transfers: number; net: number }

export interface Template {
  id: number; owner_id: number; name: string; aliases: string; kind: Kind; amount: number | null; currency: string
  account_id: number | null; to_account_id: number | null; category_id: number | null; tags: string[]; description: string; payee: string; notes: string
  pinned: boolean; use_count: number; last_used_at: string; sort_order: number; category_name?: string; category_icon?: string; account_name?: string
  /** owner switched sharing on; owner_name is set only on someone else's shared template */
  shared: boolean; owner_name?: string
}

export type Freq = 'daily' | 'weekly' | 'monthly' | 'yearly'
export type SubMode = 'remind' | 'auto' | 'track'
export interface Subscription {
  id: number; owner_id: number; name: string; kind: Kind; amount: number; amount_max: number; currency: string
  account_id: number | null; to_account_id: number | null; category_id: number | null; tags: string[]; payee: string; description: string
  freq: Freq; every: number; start_date: string; next_date: string; end_date: string; mode: SubMode; notify_days: number; match_text: string
  url: string; icon: string; notes: string; active: boolean; notified_for: string; external_id: string
  weekend: '' | 'before' | 'after' | 'skip'; nth: number; month_day: number; weekday: number; max_count: number; done_count: number; due_date: string
  category_name?: string; category_icon?: string; account_name?: string
  days_left: number; status: 'overdue' | 'today' | 'soon' | 'upcoming' | 'ended' | 'paused'; monthly_home: number; yearly_home: number
  last_paid: string; last_amount: number; paid_count: number
}
export interface AmortRow { date: string; payment: number; interest: number; principal: number; remaining: number }
export interface Payoff {
  direction: 'owe' | 'owed'; outstanding: number; principal: number; repaid: number; pct: number; rate: number; payment: number; payment_estimated: boolean
  months_left: number; payoff_date: string; interest_left: number; total_left: number; month_interest: number; never: boolean; schedule: AmortRow[]
}
export interface Webhook { id: number; name: string; url: string; secret?: string; events: string[]; format: 'native' | 'firefly'; active: boolean; last_status: number; last_error: string; last_at: string; created_at: string }
export interface Delivery { id: number; event: string; subject: string; status: number; error: string; attempts: number; duration_ms: number; created_at: string }
export interface Upcoming { id: number; name: string; date: string; amount: number; currency?: string; kind: Kind; icon: string }
export interface Detected { name: string; kind: Kind; amount: number; currency: string; account_id: number; category_id: number | null; freq: Freq; every: number; count: number; last_date: string; next_date: string; confidence: number; monthly_home: number }

export interface BudgetStatus { from: string; to: string; spent: number; carry: number; available: number; left: number; pct: number; elapsed_pct: number; projected: number; daily_left: number; state: 'ok' | 'warn' | 'over'; history: number[]; tx_count: number; period_label: string }
export interface Budget {
  id: number; owner_id: number; name: string; amount: number; period: 'weekly' | 'monthly' | 'yearly'; rollover: boolean; category_ids: number[]; tag_names: string[]
  start_date: string; alert_pct: number; color: string; icon: string; archived: boolean; sort_order: number; created_at: string; status?: BudgetStatus
}

export interface GoalEntry { id: number; goal_id: number; date: string; amount: number; note: string }
export interface Goal {
  id: number; owner_id: number; name: string; target: number; currency: string; target_date: string; account_id: number | null; icon: string; color: string; notes: string
  archived: boolean; completed_at: string; saved: number; pct: number; monthly_need: number; account_name?: string; entries?: GoalEntry[]; months_left: number
  on_track?: boolean; avg_per_month: number; eta: string
}

export interface Condition { field: string; op: string; value: string; value2?: string }
export interface Rule { id: number; name: string; match: 'all' | 'any'; conditions: Condition[]; actions: { category_id?: number | null; add_tags?: string[]; payee?: string; description?: string; append_note?: string }; active: boolean; stop: boolean; sort_order: number; hits: number; last_hit_at: string }

export interface DayAmount { date: string; amount: number }
export interface GroupSum { id: number; parent_id?: number | null; name: string; icon?: string; color?: string; sum: number; count: number; prev: number; average: number }

export interface Forecast {
  from: string; to: string; days_left: number; currency: string; balance: number; income_so_far: number; spent_so_far: number; bills_left: number
  income_expected: number; goals_need: number; variable_daily: number; projected_spend: number; projected_end: number; safe_to_spend: number; safe_per_day: number
  upcoming: Upcoming[]; curve: DayAmount[]; last_month_by_now: number
}
export interface Insight { kind: 'up' | 'down' | 'info' | 'warn' | 'sub'; icon: string; title: string; text: string; link?: string }
export interface Trip { id: number; name: string; destination: string; start_date: string; end_date: string; status: string; firefly_tag: string }

export interface Home {
  currency: string; net_worth: number; liquid: number; accounts: Account[]
  month: { from: string; to: string; label: string; income: number; expense: number; net: number; daily: DayAmount[]; categories: GroupSum[] }
  forecast: Forecast; recent: Transaction[]; templates: Template[]; budgets: Budget[]; goals: Goal[]; insights: Insight[]; trip: Trip | null; needs_onboarding: boolean
}

export interface Draft {
  kind: Kind; amount: number | null; currency: string; date: string; time: string; account_id: number; to_account_id: number; category_id: number
  tags: string[]; description: string; payee: string; template_id: number; recognized: string[]; input: string; calc?: string
  account_name: string; account_currency: string; to_account_name?: string; category_name: string; category_icon: string; account_amount: number | null; rate: number; ready: boolean; problem?: string
}

/** A template that fits what is being typed; `line` books it, `preview` is what that line would book. */
export interface QuickSuggestion { template_id: number; name: string; alias?: string; line: string; preview: Draft }

export interface Meta { account_kinds: AccountKind[]; transaction_kinds: Kind[]; frequencies: Freq[]; subscription_modes: SubMode[]; budget_periods: string[] }

export const KIND_ICON: Record<AccountKind, string> = { checking: '🏦', savings: '🐷', cash: '💵', credit: '💳', investment: '📈', loan: '🧾', mortgage: '🏠', debt: '🤝', other: '👛' }
export const KIND_LABEL: Record<AccountKind, string> = { checking: 'Current account', savings: 'Savings', cash: 'Cash', credit: 'Credit card', investment: 'Investments', loan: 'Loan', mortgage: 'Mortgage', debt: 'Debt / IOU', other: 'Other' }
export const FREQ_LABEL: Record<Freq, string> = { daily: 'day', weekly: 'week', monthly: 'month', yearly: 'year' }
export const MODE_LABEL: Record<SubMode, string> = { remind: 'Remind me', auto: 'Book automatically', track: 'Only track payments' }

/** Categorical chart colours (validated palette, light + dark steps). */
export const SERIES: Record<string, { light: string; dark: string }> = {
  income: { light: '#1baf7a', dark: '#199e70' }, expense: { light: '#eb6834', dark: '#d95926' }, net: { light: '#2a78d6', dark: '#3987e5' },
  a: { light: '#2a78d6', dark: '#3987e5' }, b: { light: '#eb6834', dark: '#d95926' }, c: { light: '#1baf7a', dark: '#199e70' }, d: { light: '#eda100', dark: '#c98500' }, e: { light: '#e87ba4', dark: '#d55181' },
}
export const PALETTE = ['a', 'b', 'c', 'd', 'e']

export type AuditChange = { from?: string; to?: string }
export interface AuditEntry {
  id: number; account_id: number; account_name?: string; actor_user_id: number | null; actor_name: string; actor_label: string
  source: string; action: string; transaction_id: number | null; summary: string; changes: Record<string, AuditChange>; created_at: string
}
export interface AuditActor { key: string; user_id: number | null; name: string }
export interface AuditPage { items: AuditEntry[]; next_before: number | null; actors: AuditActor[] }
