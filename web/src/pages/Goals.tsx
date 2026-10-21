import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import type { Goal } from '../lib/types'
import { Empty, Field, Loading, Modal, PageHeader, clickRow, useAsync, useToast } from '../components/ui'
import { CurrencySelect, MoneyInput } from '../components/Money'
import { AccountSelect } from '../components/pickers'
import { Ring } from '../components/Charts'
import { Icon } from '../components/icons'

export default function Goals() {
  const { currencies } = useApp()
  const { data, reload } = useAsync(() => api.get<Goal[]>(`${V1}/goals`), [])
  const [editing, setEditing] = useState<Goal | 'new' | null>(null)
  const [open, setOpen] = useState<number | null>(null)
  if (!data) return <Loading />
  const active = data.filter(g => !g.archived)
  return (
    <>
      <PageHeader title="Savings goals" sub="Put money aside for something — on its own or as the balance of a savings account." actions={<button type="button" className="btn primary" onClick={() => setEditing('new')}><Icon name="plus" size={16} />Goal</button>} />
      {active.length === 0 ? <Empty title="No goals yet">A trip, a new laptop, an emergency fund… Pocket Money tells you how much to save each month to get there on time, and the safe-to-spend figure leaves it aside.</Empty> : (
        <div className="goal-grid">
          {active.map(g => {
            const dec = decimalsOf(g.currency, currencies)
            return (
              <div key={g.id} className={`goal-card ${g.completed_at ? 'done' : ''}`} {...clickRow(() => setOpen(g.id))}>
                <Ring pct={g.pct} icon={g.completed_at ? '✅' : g.icon || '🎯'} />
                <div style={{ minWidth: 0 }}>
                  <b>{g.name}</b>
                  <div className="small">{fmtMoney(g.saved, g.currency, dec)} <span className="muted">of {fmtMoney(g.target, g.currency, dec)}</span></div>
                  {g.completed_at ? <div className="ok small">Reached {fmtDate(g.completed_at)}</div> : <>
                    {g.target_date && <div className="small muted">by {fmtDate(g.target_date)} · {fmtMoney(g.monthly_need, g.currency, dec)}/month needed</div>}
                    {g.on_track != null && <div className={`small ${g.on_track ? 'ok' : 'bad'}`}>{g.on_track ? 'On track' : 'Behind'} · saving {fmtMoney(g.avg_per_month, g.currency, dec)}/month</div>}
                    {g.on_track == null && g.eta && <div className="small muted">at this pace: {fmtDate(g.eta)}</div>}
                  </>}
                  {g.account_name && <div className="small muted">= balance of {g.account_name}</div>}
                </div>
              </div>
            )
          })}
        </div>
      )}
      {editing && <GoalForm goal={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); reload() }} />}
      {open && <GoalDetail id={open} onClose={() => setOpen(null)} onEdit={g => { setOpen(null); setEditing(g) }} onChanged={reload} />}
    </>
  )
}

function GoalForm({ goal, onClose, onSaved }: { goal?: Goal; onClose: () => void; onSaved: () => void }) {
  const { home } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: goal?.name || '', target: goal?.target ?? null as number | null, currency: goal?.currency || home, target_date: goal?.target_date || '', account_id: goal?.account_id ?? null as number | null, icon: goal?.icon || '🎯', notes: goal?.notes || '', archived: goal?.archived ?? false, completed: Boolean(goal?.completed_at) })
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const body = { ...f, account_id: f.account_id || 0 }
      if (goal) await api.put(`${V1}/goals/${goal.id}`, body); else await api.post(`${V1}/goals`, body)
      onSaved()
    } catch (err) { toast((err as Error).message, 'err') }
  }
  const del = async () => { if (goal && confirm(`Delete ${goal.name}?`)) { await api.del(`${V1}/goals/${goal.id}`); onSaved() } }
  return (
    <Modal title={goal ? `Edit ${goal.name}` : 'New goal'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <div className="row"><input value={f.icon} onChange={e => set('icon', e.target.value)} style={{ width: 64, textAlign: 'center', fontSize: 20 }} maxLength={4} aria-label="Icon" /><input value={f.name} onChange={e => set('name', e.target.value)} placeholder="Japan 2027" autoFocus required /></div>
        <Field label="Target" group><div className="input-group"><MoneyInput value={f.target} code={f.currency} onChange={v => set('target', v)} /><CurrencySelect value={f.currency} onChange={v => set('currency', v)} /></div></Field>
        <Field label="By (optional)"><input type="date" value={f.target_date} onChange={e => set('target_date', e.target.value)} /></Field>
        <Field label="Saved in an account (optional)" help="Then the account balance is what is saved; otherwise add deposits by hand." group><AccountSelect value={f.account_id} onChange={id => set('account_id', id || null)} all clearLabel="No account — track deposits" /></Field>
        <Field label="Notes"><textarea value={f.notes} onChange={e => set('notes', e.target.value)} rows={2} /></Field>
        {goal && <><label className="row"><input type="checkbox" checked={f.completed} onChange={e => set('completed', e.target.checked)} style={{ width: 'auto' }} /> Reached</label><label className="row"><input type="checkbox" checked={f.archived} onChange={e => set('archived', e.target.checked)} style={{ width: 'auto' }} /> Archived</label></>}
        <div className="form-actions">{goal && <button type="button" className="btn ghost danger-text" onClick={del}>Delete</button>}<span className="spacer" /><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

function GoalDetail({ id, onClose, onEdit, onChanged }: { id: number; onClose: () => void; onEdit: (g: Goal) => void; onChanged: () => void }) {
  const { currencies } = useApp()
  const { toast } = useToast()
  const { data: g, reload } = useAsync(() => api.get<Goal>(`${V1}/goals/${id}`), [id])
  const [amount, setAmount] = useState<number | null>(null)
  const [note, setNote] = useState('')
  if (!g) return null
  const dec = decimalsOf(g.currency, currencies)
  const add = async (sign: 1 | -1) => {
    if (!amount) return
    await api.post(`${V1}/goals/${g.id}/entries`, { amount: sign * amount, note, date: todayISO() })
    setAmount(null); setNote(''); toast(sign > 0 ? 'Added' : 'Taken out'); reload(); onChanged()
  }
  return (
    <Modal title={<>{g.icon} {g.name}</>} onClose={onClose}>
      <div className="row" style={{ gap: 18 }}>
        <Ring pct={g.pct} icon={g.icon || '🎯'} size={96} />
        <div><div style={{ fontSize: 22, fontWeight: 700 }}>{fmtMoney(g.saved, g.currency, dec)}</div><div className="muted">of {fmtMoney(g.target, g.currency, dec)} · {fmtMoney(Math.max(0, g.target - g.saved), g.currency, dec)} to go</div>
          {g.target_date && <div className="small">{fmtMoney(g.monthly_need, g.currency, dec)}/month until {fmtDate(g.target_date)}</div>}</div>
      </div>
      {!g.account_id && <>
        <div className="row mt"><div style={{ width: 150 }}><MoneyInput value={amount} code={g.currency} onChange={setAmount} /></div><input value={note} onChange={e => setNote(e.target.value)} placeholder="note" /><button type="button" className="btn primary" onClick={() => add(1)} disabled={!amount}>Add</button><button type="button" className="btn" onClick={() => add(-1)} disabled={!amount}>Take out</button></div>
        <div className="mt inline-list">{(g.entries || []).map(e => (
          <div key={e.id} className="li"><span className="muted small">{fmtDate(e.date)}</span><span className="grow">{e.note}</span><span className={`money ${e.amount < 0 ? 'bad' : 'ok'}`}>{fmtMoney(e.amount, g.currency, dec, { sign: true })}</span>
            <button type="button" className="icon-btn" onClick={async () => { await api.del(`${V1}/goals/${g.id}/entries/${e.id}`); reload(); onChanged() }} aria-label="Remove"><Icon name="x" size={14} /></button></div>
        ))}</div>
      </>}
      {g.account_id && <p className="muted mt">Follows the balance of <b>{g.account_name}</b> — transfers into it count automatically.</p>}
      <div className="form-actions"><button type="button" className="btn" onClick={() => onEdit(g)}>Edit goal</button></div>
    </Modal>
  )
}
