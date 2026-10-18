import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { daysBetween, decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import { KIND_ICON, type Home as HomeT, type Template, type Transaction } from '../lib/types'
import { Card, Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'
import { QuickAdd } from '../components/QuickAdd'
import { TxList } from '../components/TxList'
import { TxModal, type TxSeed } from '../components/TxEditor'
import { DayBars, HBars } from '../components/Charts'
import { AccountForm } from './Accounts'

export function Templates({ items, onBooked, onOpen }: { items: Template[]; onBooked: () => void; onOpen: (seed: TxSeed) => void }) {
  const { currencies, acctById } = useApp()
  const { toast } = useToast()
  const tap = async (t: Template) => {
    if (t.amount == null) {
      onOpen({ kind: t.kind, account_id: t.account_id || undefined, to_account_id: t.to_account_id, category_id: t.category_id, description: t.description || t.name, payee: t.payee, tags: t.tags, notes: t.notes, currency: t.currency || undefined, template_id: t.id })
      return
    }
    try {
      const tx = await api.post<Transaction>(`${V1}/templates/${t.id}/use`, {})
      toast(`Added ${tx.description} · ${fmtMoney(tx.amount, tx.currency, decimalsOf(tx.currency, currencies))}`)
      onBooked()
    } catch (e) { toast((e as Error).message, 'err') }
  }
  if (items.length === 0) return null
  return (
    <div className="tpl-grid">
      {items.map(t => {
        const cur = t.currency || acctById(t.account_id)?.currency || ''
        return (
          <button key={t.id} type="button" className={`tpl ${t.pinned ? 'pinned' : ''}`} onClick={() => tap(t)} title={t.amount == null ? 'Opens the form for the amount' : 'Books it right away'}>
            <span className="cat-ico sm">{t.category_icon || (t.kind === 'income' ? '💰' : t.kind === 'transfer' ? '⇄' : '•')}</span>
            <span className="tx"><span className="t">{t.name}</span><span className="s">{t.amount != null ? fmtMoney(t.amount, cur, decimalsOf(cur, currencies)) : 'amount…'}{t.currency ? ` · ${t.currency}` : ''}</span></span>
          </button>
        )
      })}
    </div>
  )
}

export default function Home() {
  const { home, currencies, settings } = useApp()
  const navigate = useNavigate()
  const { data, loading, reload } = useAsync(() => api.get<HomeT>(`${V1}/home`), [])
  const [open, setOpen] = useState<{ id?: number; seed?: TxSeed } | null>(null)
  if (loading && !data) return <Loading />
  if (!data) return null
  const dec = decimalsOf(home, currencies)
  const m = (v: number, o: { sign?: boolean; compact?: boolean } = {}) => fmtMoney(v, home, dec, o)
  if (data.needs_onboarding) return <Onboarding onDone={reload} />
  const fc = data.forecast
  const pace = fc.last_month_by_now > 0 ? Math.round((fc.spent_so_far - fc.last_month_by_now) / fc.last_month_by_now * 100) : null
  const upcoming = fc.upcoming.filter(u => daysBetween(todayISO(), u.date) <= 14)
  return (
    <>
      {data.trip && <div className="trip-banner">✈️ <span><b>{data.trip.name}</b> is on — new expenses get the <b>#{data.trip.firefly_tag}</b> tag{settings?.travel_auto_tag ? '' : ' (auto-tag is off)'}.</span><span className="spacer" /><Link to={`/transactions?tag=${encodeURIComponent(data.trip.firefly_tag)}`}>Trip spending →</Link></div>}
      <div className="money-hero">
        <div className="first">
          <div className="l">Net worth</div>
          <div className="n">{m(data.net_worth)}</div>
          <div className="s">{data.accounts.length} accounts · everyday money {m(data.liquid)}</div>
        </div>
        <div className="divider">
          <div className="l">Spent in {data.month.label}</div>
          <div className="n md">{m(data.month.expense)}</div>
          <div className="s">{data.month.income > 0 ? `in ${m(data.month.income)}` : 'no income yet'}{pace != null ? ` · ${pace > 0 ? '+' : ''}${pace}% vs last month` : ''}</div>
        </div>
        <div className="divider">
          <div className="l">Safe to spend</div>
          <div className="n md">{m(fc.safe_per_day)}<span style={{ fontSize: 13, fontWeight: 500 }}> /day</span></div>
          <div className="s">{m(fc.safe_to_spend)} for {fc.days_left + 1} days</div>
        </div>
        <div className="divider">
          <div className="l">Still due this month</div>
          <div className="n md">{m(fc.bills_left)}</div>
          <div className="s">month ends ≈ {m(fc.projected_end, { compact: true })}</div>
        </div>
      </div>
      <div className="mb"><QuickAdd onAdded={() => reload()} /></div>
      {data.templates.length > 0 && <div className="mb"><Templates items={data.templates} onBooked={reload} onOpen={seed => setOpen({ seed })} /></div>}
      <div className="home-grid">
        <div className="grid" style={{ gap: 16 }}>
          <Card title="Recent" actions={<Link to="/transactions" className="btn ghost sm">All →</Link>}>
            {data.recent.length === 0 ? <Empty title="No transactions yet">Type one above.</Empty> : <TxList items={data.recent} onOpen={t => setOpen({ id: t.id })} />}
          </Card>
          <Card title={`${data.month.label} day by day`} actions={<Link to="/reports" className="btn ghost sm">Reports →</Link>}>
            <DayBars days={data.month.daily} currency={home} decimals={dec} />
            <div className="mt"><HBars rows={data.month.categories.map(c => ({ key: c.id, label: c.name || 'Uncategorised', icon: c.icon, value: c.sum }))} currency={home} decimals={dec} onPick={k => navigate(`/transactions?category=${k || 'none'}&from=${data.month.from}&to=${data.month.to}`)} /></div>
          </Card>
        </div>
        <div className="grid" style={{ gap: 16 }}>
          {data.insights.length > 0 && (
            <Card title="Worth a look">
              <div className="insights">{data.insights.slice(0, 4).map((i, n) => (
                <Link key={n} to={i.link || '#'} className={`insight ${i.kind}`}><span className="ie">{i.icon}</span><span><div className="it">{i.title}</div><div className="muted">{i.text}</div></span></Link>
              ))}</div>
            </Card>
          )}
          <Card title="Coming up" actions={<Link to="/subscriptions" className="btn ghost sm">Subscriptions →</Link>}>
            {upcoming.length === 0 ? <p className="muted small">Nothing due in the next two weeks.</p> : (
              <div className="timeline">{upcoming.map((u, i) => (
                <Link key={i} to={`/subscriptions/${u.id}`} className="tl" style={{ color: 'inherit' }}>
                  <span className="when">{u.date === todayISO() ? 'today' : fmtDate(u.date, false)}</span><span>{u.icon || '↻'}</span><span className="nm">{u.name}</span>
                  <span className={`amt ${u.kind}`}>{u.kind === 'income' ? '+' : '−'}{m(u.amount)}</span>
                </Link>
              ))}</div>
            )}
          </Card>
          {data.budgets.length > 0 && (
            <Card title="Budgets" actions={<Link to="/budgets" className="btn ghost sm">All →</Link>}>
              <div className="grid" style={{ gap: 10 }}>{data.budgets.slice(0, 5).map(b => b.status && (
                <Link key={b.id} to="/budgets" style={{ color: 'inherit' }}>
                  <div className="row small"><span>{b.icon} {b.name}</span><span className="spacer" /><span className="muted">{m(b.status.spent)} / {m(b.status.available)}</span></div>
                  <div className={`pbar sm ${b.status.state}`}><span className="f" style={{ width: `${Math.min(100, b.status.pct)}%` }} /><span className="pace" style={{ left: `${Math.min(100, b.status.elapsed_pct)}%` }} /></div>
                </Link>
              ))}</div>
            </Card>
          )}
          <Card title="Accounts" actions={<Link to="/accounts" className="btn ghost sm">Manage →</Link>}>
            <div className="grid" style={{ gap: 6 }}>{data.accounts.map(a => (
              <Link key={a.id} to={`/accounts/${a.id}`} className="row" style={{ color: 'inherit' }}>
                <span>{a.icon || KIND_ICON[a.kind]}</span><span className="grow" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.name}</span>
                <span className={`amt ${a.balance < 0 ? 'bad' : ''}`}>{fmtMoney(a.balance, a.currency, decimalsOf(a.currency, currencies))}</span>
              </Link>
            ))}</div>
          </Card>
          {data.goals.length > 0 && (
            <Card title="Goals" actions={<Link to="/goals" className="btn ghost sm">All →</Link>}>
              <div className="grid" style={{ gap: 10 }}>{data.goals.slice(0, 3).map(g => (
                <Link key={g.id} to="/goals" style={{ color: 'inherit' }}>
                  <div className="row small"><span>{g.icon} {g.name}</span><span className="spacer" /><span className="muted">{fmtMoney(g.saved, g.currency, decimalsOf(g.currency, currencies), { compact: true })} / {fmtMoney(g.target, g.currency, decimalsOf(g.currency, currencies), { compact: true })}</span></div>
                  <div className="pbar sm"><span className="f" style={{ width: `${g.pct}%` }} /></div>
                </Link>
              ))}</div>
            </Card>
          )}
        </div>
      </div>
      {open && <TxModal id={open.id} seed={open.seed} onClose={() => setOpen(null)} onChanged={reload} />}
    </>
  )
}

function Onboarding({ onDone }: { onDone: () => void }) {
  const { settings, reloadAccounts } = useApp()
  const [creating, setCreating] = useState(false)
  const navigate = useNavigate()
  return (
    <>
      <PageHeader title="Welcome to Pocket Money" sub="Accounts, quick entry, subscriptions, budgets and goals — in any currency." />
      <div className="onboard">
        <div className="opt"><span className="e">🔥</span><span className="grow"><b>Bring everything over from Firefly III</b><div className="muted small">Accounts, categories, tags, all transactions, bills and recurring transactions, and Firefly Pico templates. Re-runnable, nothing is duplicated.</div></span><button className="btn primary" onClick={() => navigate('/settings?tab=import')}>{settings?.firefly_configured ? 'Import' : 'Set up'}</button></div>
        <div className="opt"><span className="e">🏦</span><span className="grow"><b>Start fresh</b><div className="muted small">Create your first account with its current balance.</div></span><button className="btn" onClick={() => setCreating(true)}>New account</button></div>
        <div className="opt"><span className="e">📄</span><span className="grow"><b>Import a bank statement (CSV)</b><div className="muted small">Map the columns once; duplicates are skipped.</div></span><button className="btn" onClick={() => navigate('/settings?tab=import')}>CSV import</button></div>
      </div>
      {creating && <AccountForm onClose={() => setCreating(false)} onSaved={async () => { setCreating(false); await reloadAccounts(); onDone() }} />}
    </>
  )
}
