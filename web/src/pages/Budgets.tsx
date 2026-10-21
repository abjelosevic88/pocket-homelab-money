import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import type { Budget, DayAmount, Transaction } from '../lib/types'
import { Card, Empty, Field, Loading, Modal, PageHeader, Segmented, clickRow, useAsync, useToast } from '../components/ui'
import { MoneyInput } from '../components/Money'
import { Line } from '../components/Charts'
import { TxList } from '../components/TxList'
import { TxModal } from '../components/TxEditor'
import { TagInput } from '../components/pickers'
import { Icon } from '../components/icons'

type List = { items: Budget[]; currency: string; month_total: number; month_spent: number; month_all_spent: number; month_from: string; month_to: string; month_label: string }
type Sug = { category_id: number; name: string; icon: string; average: number; suggested: number }

function shiftMonth(d: string, n: number) {
  const [y, m, day] = d.split('-').map(Number)
  const t = new Date(y, m - 1 + n, Math.min(day, 28))
  return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')}`
}

export default function Budgets() {
  const { home, currencies } = useApp()
  const [date, setDate] = useState(todayISO())
  const { data, reload } = useAsync(() => api.get<List>(`${V1}/budgets?date=${date}`), [date])
  const [editing, setEditing] = useState<Budget | 'new' | null>(null)
  const [detail, setDetail] = useState<number | null>(null)
  const [suggest, setSuggest] = useState(false)
  if (!data) return <Loading />
  const dec = decimalsOf(home, currencies)
  const m = (v: number) => fmtMoney(v, home, dec)
  const active = data.items.filter(b => !b.archived)
  const isNow = date === todayISO()
  return (
    <>
      <PageHeader title="Budgets" sub={<>{data.month_label} · {m(data.month_spent)} of {m(data.month_total)} budgeted{data.month_all_spent > data.month_spent ? ` · ${m(data.month_all_spent - data.month_spent)} outside budgets` : ''}</>} actions={<>
        <button type="button" className="btn" onClick={() => setDate(d => shiftMonth(d, -1))}>‹</button>
        {!isNow && <button type="button" className="btn" onClick={() => setDate(todayISO())}>This month</button>}
        <button type="button" className="btn" onClick={() => setDate(d => shiftMonth(d, 1))}>›</button>
        <button type="button" className="btn" onClick={() => setSuggest(true)}><Icon name="sparkles" size={15} />Suggest</button>
        <button type="button" className="btn primary" onClick={() => setEditing('new')}><Icon name="plus" size={16} />Budget</button>
      </>} />
      {active.length === 0 ? (
        <Empty title="No budgets yet">A budget caps one or more categories (or tags) per week, month or year. “Suggest” proposes amounts from what you actually spent in the last three months.</Empty>
      ) : (
        <div className="budget-grid">
          {active.map(b => b.status && (
            <div key={b.id} className="budget-card" {...clickRow(() => setDetail(b.id))}>
              <div className="head"><span className="cat-ico">{b.icon || '◔'}</span><span className="nm">{b.name}</span><span className={`state-pill ${b.status.state}`}>{b.status.state === 'ok' ? 'on track' : b.status.state === 'warn' ? 'careful' : 'over'}</span></div>
              <div className={`pbar ${b.status.state}`} title={`${b.status.pct}% used, ${b.status.elapsed_pct}% of the period gone`}><span className="f" style={{ width: `${Math.min(100, b.status.pct)}%` }} /><span className="pace" style={{ left: `${Math.min(100, b.status.elapsed_pct)}%` }} /></div>
              <div className="nums"><span><b>{m(b.status.spent)}</b> of {m(b.status.available)}</span><span>{b.status.left >= 0 ? <><b>{m(b.status.left)}</b> left</> : <b className="bad">{m(-b.status.left)} over</b>}</span></div>
              <div className="nums">
                <span>{b.status.daily_left > 0 && isNow ? `${m(b.status.daily_left)} a day` : b.status.period_label}</span>
                <span>{isNow && b.status.projected > 0 ? `on pace for ${m(b.status.projected)}` : ''}</span>
              </div>
              {b.rollover && b.status.carry !== 0 && <div className="muted small">{b.status.carry > 0 ? `+${m(b.status.carry)} carried over` : `${m(-b.status.carry)} overspent earlier`}</div>}
              <div className="mini-hist" title="Previous periods">{b.status.history.map((h, i) => <span key={i} className={h > b.amount ? 'over' : ''} style={{ height: `${Math.min(100, (h / Math.max(1, ...b.status!.history, b.amount)) * 100)}%` }} />)}</div>
            </div>
          ))}
        </div>
      )}
      {editing && <BudgetForm budget={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} />}
      {detail && <BudgetDetail id={detail} date={date} onClose={() => setDetail(null)} onEdit={b => { setDetail(null); setEditing(b) }} onChanged={reload} />}
      {suggest && <Suggest onClose={() => setSuggest(false)} onDone={() => { setSuggest(false); reload() }} />}
    </>
  )
}

function BudgetForm({ budget, onClose, onSaved }: { budget?: Budget; onClose: () => void; onSaved: () => void }) {
  const { home, categories } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: budget?.name || '', amount: budget?.amount ?? null as number | null, period: budget?.period || 'monthly', rollover: budget?.rollover ?? false,
    category_ids: budget?.category_ids || [] as number[], tag_names: budget?.tag_names || [] as string[], alert_pct: budget?.alert_pct ?? 80, icon: budget?.icon || '', archived: budget?.archived ?? false })
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const toggleCat = (id: number) => set('category_ids', f.category_ids.includes(id) ? f.category_ids.filter(x => x !== id) : [...f.category_ids, id])
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    let name = f.name
    if (!name && f.category_ids.length === 1) name = categories.find(c => c.id === f.category_ids[0])?.name || ''
    const icon = f.icon || (f.category_ids.length ? categories.find(c => c.id === f.category_ids[0])?.icon || '' : '')
    try {
      if (budget) await api.put(`${V1}/budgets/${budget.id}`, { ...f, name, icon })
      else await api.post(`${V1}/budgets`, { ...f, name, icon })
      toast('Saved'); onSaved()
    } catch (err) { toast((err as Error).message, 'err') }
  }
  const del = async () => { if (budget && confirm(`Delete the ${budget.name} budget?`)) { await api.del(`${V1}/budgets/${budget.id}`); onSaved() } }
  return (
    <Modal title={budget ? `Edit ${budget.name}` : 'New budget'} onClose={onClose} size="lg">
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <div className="two-col tight">
          <Field label="Name"><input value={f.name} onChange={e => set('name', e.target.value)} placeholder="Eating out" autoFocus /></Field>
          <Field label={`Amount (${home})`}><MoneyInput value={f.amount} code={home} onChange={v => set('amount', v)} /></Field>
        </div>
        <div className="row wrap">
          <Segmented value={f.period} onChange={v => set('period', v)} options={[{ value: 'weekly', label: 'Weekly' }, { value: 'monthly', label: 'Monthly' }, { value: 'yearly', label: 'Yearly' }]} />
          <label className="row"><input type="checkbox" checked={f.rollover} onChange={e => set('rollover', e.target.checked)} style={{ width: 'auto' }} /> Roll over what is left (or overspent)</label>
        </div>
        <Field label="Categories" help="Children of a chosen category count too. None chosen = everything you spend." group>
          <div className="cat-grid">
            {categories.filter(c => !c.archived && c.kind !== 'income').map(c => (
              <button type="button" key={c.id} className={`cat-btn ${f.category_ids.includes(c.id) ? 'on' : ''} ${c.parent_id ? 'child' : ''}`} onClick={() => toggleCat(c.id)}><span className="e">{c.icon || '•'}</span><span className="n">{c.name}</span></button>
            ))}
          </div>
        </Field>
        <Field label="Only with tags (optional)" group><TagInput value={f.tag_names} onChange={v => set('tag_names', v)} /></Field>
        <div className="two-col tight">
          <Field label="Warn at % used"><input type="number" min={10} max={100} value={f.alert_pct} onChange={e => set('alert_pct', Number(e.target.value))} /></Field>
          <Field label="Icon"><input value={f.icon} onChange={e => set('icon', e.target.value)} placeholder="from the category" maxLength={4} /></Field>
        </div>
        {budget && <label className="row"><input type="checkbox" checked={f.archived} onChange={e => set('archived', e.target.checked)} style={{ width: 'auto' }} /> Archived</label>}
        <div className="form-actions">{budget && <button type="button" className="btn ghost danger-text" onClick={del}>Delete</button>}<span className="spacer" /><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

function BudgetDetail({ id, date, onClose, onEdit, onChanged }: { id: number; date: string; onClose: () => void; onEdit: (b: Budget) => void; onChanged: () => void }) {
  const { home, currencies } = useApp()
  const { data, reload } = useAsync(() => api.get<{ budget: Budget; transactions: Transaction[]; burn: DayAmount[] }>(`${V1}/budgets/${id}?date=${date}`), [id, date])
  const [open, setOpen] = useState<number | null>(null)
  if (!data) return null
  const b = data.budget, s = b.status!
  const dec = decimalsOf(home, currencies)
  const today = todayISO()
  const pts = data.burn.filter(d => d.date <= today).map(d => ({ x: d.date, y: d.amount }))
  return (
    <Modal title={<>{b.icon} {b.name} · {s.period_label}</>} onClose={onClose} size="lg">
      <div className="stats">
        <div className="stat"><div className="n">{fmtMoney(s.spent, home, dec)}</div><div className="l">spent of {fmtMoney(s.available, home, dec)}</div></div>
        <div className="stat"><div className={`n ${s.left < 0 ? 'bad' : ''}`}>{fmtMoney(s.left, home, dec)}</div><div className="l">{s.left < 0 ? 'over' : 'left'}</div></div>
        <div className="stat"><div className="n">{fmtMoney(s.projected, home, dec)}</div><div className="l">at this pace</div></div>
      </div>
      {pts.length > 1 && <Line points={pts} fmt={v => fmtMoney(v, home, dec)} color="expense" area height={140} />}
      <p className="muted small mt-sm">{fmtDate(s.from)} – {fmtDate(s.to)} · {s.tx_count} transactions{b.rollover ? ` · carried ${fmtMoney(s.carry, home, dec, { sign: true })}` : ''}</p>
      <div className="mt">{data.transactions.length === 0 ? <p className="muted">Nothing spent yet.</p> : <TxList items={data.transactions} onOpen={t => setOpen(t.id)} />}</div>
      <div className="form-actions"><button type="button" className="btn" onClick={() => onEdit(b)}>Edit budget</button></div>
      {open && <TxModal id={open} onClose={() => setOpen(null)} onChanged={() => { reload(); onChanged() }} />}
    </Modal>
  )
}

function Suggest({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { home, currencies } = useApp()
  const { toast } = useToast()
  const { data } = useAsync(() => api.get<Sug[]>(`${V1}/budgets/suggest`), [])
  const [pick, setPick] = useState<Record<number, number | null>>({})
  const dec = decimalsOf(home, currencies)
  const create = async () => {
    const list = (data || []).filter(s => pick[s.category_id] != null)
    for (const s of list) await api.post(`${V1}/budgets`, { name: s.name, amount: pick[s.category_id], period: 'monthly', category_ids: [s.category_id], icon: s.icon })
    toast(`${list.length} budgets created`); onDone()
  }
  return (
    <Modal title="Suggested budgets" onClose={onClose}>
      <p className="muted small">From the average of the last three months, rounded up a little. Tick the ones you want and adjust.</p>
      {!data ? <Loading /> : data.length === 0 ? <p className="muted">Nothing to suggest — every category you spend in already has a budget, or there is not enough history yet.</p> : (
        <div className="grid" style={{ gap: 8 }}>
          {data.map(s => (
            <div key={s.category_id} className="row">
              <input type="checkbox" checked={pick[s.category_id] != null} onChange={e => setPick(p => ({ ...p, [s.category_id]: e.target.checked ? s.suggested : null }))} style={{ width: 'auto' }} />
              <span className="grow">{s.icon} {s.name}<div className="muted small">average {fmtMoney(s.average, home, dec)}/month</div></span>
              <div style={{ width: 130 }}><MoneyInput value={pick[s.category_id] ?? s.suggested} code={home} onChange={v => setPick(p => ({ ...p, [s.category_id]: v }))} /></div>
            </div>
          ))}
        </div>
      )}
      <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" disabled={!Object.values(pick).some(v => v != null)} onClick={create}>Create</button></div>
    </Modal>
  )
}
