import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO, weekday } from '../lib/format'
import type { DayAmount, Forecast, GroupSum } from '../lib/types'
import { Card, Empty, Loading, PageHeader, Segmented, Tabs, useAsync, useToast } from '../components/ui'
import { DayBars, FlowBars, HBars, Line } from '../components/Charts'

type Tab = 'overview' | 'flow' | 'categories' | 'tags' | 'networth' | 'calendar' | 'ask'
type Overview = { from: string; to: string; label: string; currency: string; income: number; expense: number; net: number; count: number; savings_rate: number; avg_daily: number; prev: { income: number; expense: number; net: number; from: string; to: string }; daily: DayAmount[]; categories: GroupSum[]; income_categories: GroupSum[]; top: GroupSum[] }

function monthOf(d: string) { return d.slice(0, 7) }
function shift(m: string, n: number) { const [y, mo] = m.split('-').map(Number); const t = new Date(y, mo - 1 + n, 1); return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}` }

export default function Reports() {
  const { settings } = useApp()
  const [tab, setTab] = useState<Tab>(() => (new URLSearchParams(window.location.search).get('tab') as Tab) || 'overview')
  return (
    <>
      <PageHeader title="Reports" />
      <Tabs tabs={[{ id: 'overview', label: 'Month' }, { id: 'flow', label: 'Cash flow' }, { id: 'categories', label: 'Categories' }, { id: 'tags', label: 'Tags & trips' }, { id: 'networth', label: 'Net worth' }, { id: 'calendar', label: 'Calendar' }, ...(settings?.ai_configured ? [{ id: 'ask' as Tab, label: '✨ Ask' }] : [])]} value={tab} onChange={setTab} />
      <div className="mt">
        {tab === 'overview' && <MonthReport />}
        {tab === 'flow' && <Flow />}
        {tab === 'categories' && <Categories />}
        {tab === 'tags' && <Tags />}
        {tab === 'networth' && <NetWorth />}
        {tab === 'calendar' && <Calendar />}
        {tab === 'ask' && <Ask />}
      </div>
    </>
  )
}

function Delta({ cur, prev, inverse = false }: { cur: number; prev: number; inverse?: boolean }) {
  if (!prev) return null
  const d = Math.round((cur - prev) / Math.abs(prev) * 100)
  if (!d) return <span className="cmp muted">same as before</span>
  const bad = inverse ? d < 0 : d > 0
  return <span className={`cmp ${bad ? 'up' : 'down'}`}>{d > 0 ? '▲' : '▼'} {Math.abs(d)}% vs previous</span>
}

function MonthReport() {
  const { home, currencies } = useApp()
  const navigate = useNavigate()
  const [month, setMonth] = useState(monthOf(todayISO()))
  const { data } = useAsync(() => api.get<Overview>(`${V1}/reports/overview?month=${month}`), [month])
  const { data: fc } = useAsync(() => api.get<Forecast>(`${V1}/reports/forecast`), [])
  const dec = decimalsOf(home, currencies)
  const m = (v: number, o = {}) => fmtMoney(v, home, dec, o)
  if (!data) return <Loading />
  const isNow = month === monthOf(todayISO())
  return (
    <div className="grid" style={{ gap: 16 }}>
      <div className="row wrap"><button className="btn" onClick={() => setMonth(x => shift(x, -1))}>‹</button><b>{data.label}</b><button className="btn" onClick={() => setMonth(x => shift(x, 1))}>›</button>{!isNow && <button className="btn ghost sm" onClick={() => setMonth(monthOf(todayISO()))}>This month</button>}<span className="spacer" /><span className="muted small">{fmtDate(data.from)} – {fmtDate(data.to)}</span></div>
      <div className="stats">
        <div className="stat"><div className="n ok">{m(data.income)}</div><div className="l">Income <Delta cur={data.income} prev={data.prev.income} inverse /></div></div>
        <div className="stat"><div className="n">{m(data.expense)}</div><div className="l">Expenses <Delta cur={data.expense} prev={data.prev.expense} /></div></div>
        <div className="stat"><div className={`n ${data.net >= 0 ? 'ok' : 'bad'}`}>{m(data.net, { sign: true })}</div><div className="l">Net · kept {data.savings_rate}%</div></div>
        <div className="stat"><div className="n">{m(data.avg_daily)}</div><div className="l">a day on average · {data.count} transactions</div></div>
      </div>
      {isNow && fc && (
        <Card title="Rest of the month">
          <div className="stats" style={{ marginBottom: 8 }}>
            <div className="stat"><div className="n">{m(fc.safe_per_day)}</div><div className="l">safe to spend a day ({fc.days_left + 1} days left)</div></div>
            <div className="stat"><div className="n">{m(fc.bills_left)}</div><div className="l">bills still due</div></div>
            <div className="stat"><div className="n">{m(fc.variable_daily)}</div><div className="l">your typical day (no bills)</div></div>
            <div className="stat"><div className="n">{m(fc.projected_end)}</div><div className="l">everyday money at month end</div></div>
          </div>
          <Line points={fc.curve.map(d => ({ x: d.date, y: d.amount }))} fmt={v => m(v)} area height={140} />
          <p className="muted small">Projection: today’s everyday balance, minus your typical day and the bills on their due dates, plus recurring income.</p>
        </Card>
      )}
      <Card title="Day by day"><DayBars days={data.daily} currency={home} decimals={dec} height={110} /></Card>
      <div className="two-col">
        <Card title="Where it went">
          <HBars rows={data.categories.map(c => ({ key: c.id, label: c.name || 'Uncategorised', icon: c.icon, value: c.sum, compare: c.prev }))} currency={home} decimals={dec} max={14} onPick={k => navigate(`/transactions?category=${k || 'none'}&from=${data.from}&to=${data.to}`)} />
          <p className="muted small mt-sm">Thin grey bar = the period before.</p>
        </Card>
        <div className="grid" style={{ gap: 16 }}>
          <Card title="Top payees">
            <div className="inline-list">{data.top.map(t => <div key={t.name} className="li"><span className="grow">{t.name}</span><span className="muted small">{t.count}×</span><span className="money">{m(t.sum)}</span></div>)}</div>
          </Card>
          {data.income_categories.length > 0 && <Card title="Money in"><div className="inline-list">{data.income_categories.map(c => <div key={c.id} className="li"><span className="grow">{c.icon} {c.name || 'Uncategorised'}</span><span className="money ok">{m(c.sum)}</span></div>)}</div></Card>}
        </div>
      </div>
    </div>
  )
}

function Flow() {
  const { home, currencies } = useApp()
  const [n, setN] = useState(12)
  const { data } = useAsync(() => api.get<{ months: { label: string; from: string; to: string; income: number; expense: number; net: number }[] }>(`${V1}/reports/flow?months=${n}`), [n])
  const navigate = useNavigate()
  const dec = decimalsOf(home, currencies)
  if (!data) return <Loading />
  // start at the first month that has anything in it
  const first = data.months.findIndex(x => x.income || x.expense)
  const months = first > 0 ? data.months.slice(first) : data.months
  const totIn = months.reduce((s, x) => s + x.income, 0), totOut = months.reduce((s, x) => s + x.expense, 0)
  return (
    <div className="grid" style={{ gap: 16 }}>
      <Card title="Income and expenses" actions={<Segmented size="sm" value={n} onChange={setN} options={[{ value: 6, label: '6 mo' }, { value: 12, label: '12 mo' }, { value: 24, label: '24 mo' }]} />}>
        <FlowBars data={months} currency={home} decimals={dec} height={220} onPick={i => navigate(`/transactions?from=${months[i].from}&to=${months[i].to}`)} />
      </Card>
      <div className="stats">
        <div className="stat"><div className="n ok">{fmtMoney(totIn, home, dec, { compact: true })}</div><div className="l">in over {months.length} months</div></div>
        <div className="stat"><div className="n">{fmtMoney(totOut, home, dec, { compact: true })}</div><div className="l">out</div></div>
        <div className="stat"><div className="n">{fmtMoney(Math.round(totOut / Math.max(1, months.length)), home, dec)}</div><div className="l">average month out</div></div>
        <div className="stat"><div className={`n ${totIn - totOut >= 0 ? 'ok' : 'bad'}`}>{totIn > 0 ? Math.round((totIn - totOut) / totIn * 100) : 0}%</div><div className="l">kept</div></div>
      </div>
      <Card title="Month by month" flush>
        <table className="table"><thead><tr><th>Month</th><th className="num">In</th><th className="num">Out</th><th className="num">Net</th></tr></thead>
          <tbody>{[...months].reverse().map(x => <tr key={x.from} className="clickable" onClick={() => navigate(`/transactions?from=${x.from}&to=${x.to}`)}><td>{x.label}</td><td className="num ok">{fmtMoney(x.income, home, dec)}</td><td className="num">{fmtMoney(x.expense, home, dec)}</td><td className={`num ${x.net >= 0 ? 'ok' : 'bad'}`}>{fmtMoney(x.net, home, dec, { sign: true })}</td></tr>)}</tbody></table>
      </Card>
    </div>
  )
}

function Categories() {
  const { home, currencies } = useApp()
  const navigate = useNavigate()
  const [month, setMonth] = useState(monthOf(todayISO()))
  const [kind, setKind] = useState<'expense' | 'income'>('expense')
  const [split, setSplit] = useState(false)
  const { data } = useAsync(() => api.get<{ from: string; to: string; total: number; categories: GroupSum[]; parents: GroupSum[] }>(`${V1}/reports/categories?month=${month}&kind=${kind}`), [month, kind])
  const dec = decimalsOf(home, currencies)
  if (!data) return <Loading />
  const rows = split ? data.categories : data.parents
  return (
    <Card title={<div className="row wrap"><button className="btn sm" onClick={() => setMonth(x => shift(x, -1))}>‹</button><span>{fmtDate(data.from, false)} – {fmtDate(data.to)}</span><button className="btn sm" onClick={() => setMonth(x => shift(x, 1))}>›</button></div>}
      actions={<><Segmented size="sm" value={kind} onChange={setKind} options={[{ value: 'expense', label: 'Expenses' }, { value: 'income', label: 'Income' }]} /><Segmented size="sm" value={split ? 1 : 0} onChange={v => setSplit(v === 1)} options={[{ value: 0, label: 'Groups' }, { value: 1, label: 'All' }]} /></>} flush>
      {rows.length === 0 ? <Empty title="Nothing in this period" /> : (
        <table className="table cat-table">
          <thead><tr><th>Category</th><th className="num">Amount</th><th className="num">Share</th><th className="num">Before</th><th className="num">3-period avg</th></tr></thead>
          <tbody>{rows.map(c => {
            const vs = c.average > 0 ? Math.round((c.sum - c.average) / c.average * 100) : null
            return (
              <tr key={c.id} className="clickable" onClick={() => navigate(`/transactions?category=${c.id || 'none'}&from=${data.from}&to=${data.to}`)}>
                <td>{c.icon} {c.name || 'Uncategorised'} <span className="muted small">{c.count}×</span></td>
                <td className="num">{fmtMoney(c.sum, home, dec)}</td>
                <td className="num">{data.total ? Math.round(c.sum / data.total * 100) : 0}%</td>
                <td className="num muted">{c.prev ? fmtMoney(c.prev, home, dec) : '—'}</td>
                <td className="num">{c.average ? <>{fmtMoney(c.average, home, dec)} {vs != null && Math.abs(vs) >= 10 && <span className={`cmp ${kind === 'expense' ? (vs > 0 ? 'up' : 'down') : vs > 0 ? 'down' : 'up'}`}>{vs > 0 ? '+' : ''}{vs}%</span>}</> : '—'}</td>
              </tr>
            )
          })}</tbody>
        </table>
      )}
    </Card>
  )
}

function Tags() {
  const { home, currencies } = useApp()
  const navigate = useNavigate()
  const { data } = useAsync(() => api.get<{ tags: GroupSum[] }>(`${V1}/reports/tags?all=1`), [])
  const dec = decimalsOf(home, currencies)
  if (!data) return <Loading />
  return (
    <Card title="Spending per tag (all time)" flush>
      {data.tags.length === 0 ? <Empty title="No tags used yet">Tag a trip, a project or a vehicle (#lisbon, #car) and its total shows up here — Pocket Travel and Pocket Garage read the same tags.</Empty> : (
        <table className="table"><thead><tr><th>Tag</th><th className="num">Transactions</th><th className="num">Spent</th></tr></thead>
          <tbody>{data.tags.map(t => <tr key={t.id} className="clickable" onClick={() => navigate(`/transactions?tag=${encodeURIComponent(t.name)}`)}><td>#{t.name}</td><td className="num">{t.count}</td><td className="num">{fmtMoney(t.sum, home, dec)}</td></tr>)}</tbody></table>
      )}
    </Card>
  )
}

function NetWorth() {
  const { home, currencies } = useApp()
  const [n, setN] = useState(12)
  const { data } = useAsync(() => api.get<{ points: { date: string; label: string; assets: number; liabilities: number; net: number; by_account: Record<string, number> }[]; accounts: Record<string, string> }>(`${V1}/reports/networth?months=${n}`), [n])
  const dec = decimalsOf(home, currencies)
  if (!data) return <Loading />
  const last = data.points[data.points.length - 1], first = data.points[0]
  return (
    <div className="grid" style={{ gap: 16 }}>
      <Card title="Net worth" actions={<Segmented size="sm" value={n} onChange={setN} options={[{ value: 6, label: '6 mo' }, { value: 12, label: '12 mo' }, { value: 36, label: '3 y' }]} />}>
        <Line points={data.points.map(p => ({ x: p.date, y: p.net }))} fmt={v => fmtMoney(v, home, dec)} area height={220} color="income" />
        {last && first && <p className="small mt-sm">{fmtMoney(last.net, home, dec)} now · <span className={last.net - first.net >= 0 ? 'ok' : 'bad'}>{fmtMoney(last.net - first.net, home, dec, { sign: true })}</span> since {fmtDate(first.date)}. Every account is valued at that day’s exchange rate.</p>}
      </Card>
      {last && (
        <Card title="By account" flush>
          <table className="table"><thead><tr><th>Account</th>{data.points.slice(-4).map(p => <th key={p.date} className="num">{p.label}</th>)}</tr></thead>
            <tbody>{Object.entries(data.accounts).filter(([id]) => last.by_account[id] != null).map(([id, name]) => <tr key={id}><td>{name}</td>{data.points.slice(-4).map(p => <td key={p.date} className="num">{p.by_account[id] != null ? fmtMoney(p.by_account[id], home, dec) : '—'}</td>)}</tr>)}</tbody></table>
        </Card>
      )}
    </div>
  )
}

function Calendar() {
  const { home, currencies } = useApp()
  const navigate = useNavigate()
  const [year, setYear] = useState(todayISO().slice(0, 4))
  const { data } = useAsync(() => api.get<{ days: Record<string, { expense?: number; income?: number; count?: number }> }>(`${V1}/reports/calendar?year=${year}`), [year])
  const dec = decimalsOf(home, currencies)
  if (!data) return <Loading />
  const vals = Object.values(data.days).map(d => d.expense || 0).filter(v => v > 0).sort((a, b) => a - b)
  const q = (p: number) => vals[Math.floor(p * (vals.length - 1))] || 0
  const cuts = [q(0.25), q(0.5), q(0.75), q(0.93)]
  const start = new Date(Number(year), 0, 1)
  const pad = (start.getDay() + 6) % 7
  const cells: { date: string; v: number }[] = []
  for (let i = -pad; i < 372 - pad; i++) {
    const d = new Date(Number(year), 0, 1 + i)
    const iso = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    cells.push({ date: d.getFullYear() === Number(year) ? iso : '', v: data.days[iso]?.expense || 0 })
  }
  const level = (v: number) => (v <= 0 ? '' : v <= cuts[0] ? 'l1' : v <= cuts[1] ? 'l2' : v <= cuts[2] ? 'l3' : 'l4')
  const total = vals.reduce((s, v) => s + v, 0)
  const top = Object.entries(data.days).sort((a, b) => (b[1].expense || 0) - (a[1].expense || 0)).slice(0, 5)
  return (
    <div className="grid" style={{ gap: 16 }}>
      <Card title={<div className="row"><button className="btn sm" onClick={() => setYear(y => String(Number(y) - 1))}>‹</button>{year}<button className="btn sm" onClick={() => setYear(y => String(Number(y) + 1))}>›</button></div>} actions={<span className="muted small">{fmtMoney(total, home, dec)} spent · {vals.length} days with spending</span>}>
        <div className="heatmap">{cells.map((c, i) => <span key={i} className={`hc ${c.date ? level(c.v) : 'out'}`} title={c.date ? `${weekday(c.date)} ${fmtDate(c.date)} · ${fmtMoney(c.v, home, dec)}` : ''} onClick={() => c.date && navigate(`/transactions?from=${c.date}&to=${c.date}`)} style={{ cursor: c.date ? 'pointer' : undefined }} />)}</div>
        <div className="legend"><span>less</span>{['', 'l1', 'l2', 'l3', 'l4'].map(l => <span key={l} className={`hc ${l}`} style={{ width: 12, height: 12, borderRadius: 3, display: 'inline-block', background: l ? undefined : 'var(--surface-2)' }} />)}<span>more</span></div>
      </Card>
      <Card title="Biggest days">
        <div className="inline-list">{top.map(([d, v]) => <div key={d} className="li" style={{ cursor: 'pointer' }} onClick={() => navigate(`/transactions?from=${d}&to=${d}`)}><span className="grow">{weekday(d, true)}, {fmtDate(d)}</span><span className="muted small">{v.count} tx</span><span className="money">{fmtMoney(v.expense || 0, home, dec)}</span></div>)}</div>
      </Card>
    </div>
  )
}

function Ask() {
  const { toast } = useToast()
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState(false)
  const [answers, setAnswers] = useState<{ q: string; a: string }[]>([])
  const ask = async (question = q) => {
    if (!question.trim()) return
    setBusy(true)
    try {
      const r = await api.post<{ answer: string }>(`${V1}/ai/ask`, { question })
      setAnswers(a => [{ q: question, a: r.answer }, ...a]); setQ('')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const examples = ['How much did I spend on restaurants this year?', 'Koliko sam potrošio na gorivo u septembru?', 'Which subscriptions could I cancel?', 'What did the Luštica trip cost in total?']
  return (
    <div className="grid" style={{ gap: 12 }}>
      <form className="row" onSubmit={e => { e.preventDefault(); ask() }}><input value={q} onChange={e => setQ(e.target.value)} placeholder="Ask anything about your money…" autoFocus /><button className="btn primary" disabled={busy}>{busy ? 'Thinking…' : 'Ask'}</button></form>
      {answers.length === 0 && <div className="chips">{examples.map(e => <button key={e} className="chip" onClick={() => ask(e)}>{e}</button>)}</div>}
      {answers.map((x, i) => <Card key={i} title={x.q}><div className="answer">{x.a}</div></Card>)}
      <p className="muted small">The model sees monthly totals per category, balances, subscriptions, top payees and the transactions that match words in your question — not your notes or receipts.</p>
    </div>
  )
}
