import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import { FREQ_LABEL, MODE_LABEL, type Detected, type Freq, type Kind, type SubMode, type Subscription, type Transaction, type Upcoming } from '../lib/types'
import { Card, Empty, Field, Loading, Modal, PageHeader, Segmented, useAsync, useToast } from '../components/ui'
import { CurrencySelect, MoneyInput } from '../components/Money'
import { AccountSelect, CategorySelect, KindTabs, TagInput } from '../components/pickers'
import { TxList } from '../components/TxList'
import { TxModal } from '../components/TxEditor'
import { Select } from '../components/Select'

type List = { items: Subscription[]; monthly: number; yearly: number; income_monthly: number; currency: string; upcoming: Upcoming[] }

export function every(s: { freq: Freq; every: number }) {
  return s.every > 1 ? `every ${s.every} ${FREQ_LABEL[s.freq]}s` : `every ${FREQ_LABEL[s.freq]}`
}
export function dueText(s: Subscription) {
  if (s.status === 'paused') return 'paused'
  if (s.status === 'ended') return 'ended'
  if (s.days_left < 0) return `${-s.days_left} d overdue`
  if (s.days_left === 0) return 'today'
  if (s.days_left === 1) return 'tomorrow'
  if (s.days_left < 30) return `in ${s.days_left} d`
  return fmtDate(s.next_date, false)
}

export default function Subscriptions() {
  const { home, currencies } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const { data, reload } = useAsync(() => api.get<List>(`${V1}/subscriptions`), [])
  const { data: detected, reload: reloadDetected } = useAsync(() => api.get<Detected[]>(`${V1}/subscriptions/detect`), [])
  const [editing, setEditing] = useState<Partial<Subscription> | null>(null)
  const [filter, setFilter] = useState<'all' | 'expense' | 'income'>('all')
  if (!data) return <Loading />
  const dec = decimalsOf(home, currencies)
  const m = (v: number) => fmtMoney(v, home, dec)
  const items = data.items.filter(s => filter === 'all' || s.kind === filter)
  const pay = async (s: Subscription) => {
    try { await api.post(`${V1}/subscriptions/${s.id}/pay`, {}); toast(`${s.name} paid`); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <>
      <PageHeader title="Subscriptions & bills" sub={<>{m(data.monthly)} a month · {m(data.yearly)} a year{data.income_monthly > 0 ? ` · ${m(data.income_monthly)} recurring income` : ''}</>} actions={<button className="btn primary" onClick={() => setEditing({})}>＋ Subscription</button>} />
      <div className="home-grid">
        <div className="grid" style={{ gap: 16 }}>
          <Card title="All" actions={<Segmented size="sm" value={filter} onChange={setFilter} options={[{ value: 'all', label: 'All' }, { value: 'expense', label: 'Bills' }, { value: 'income', label: 'Income' }]} />}>
            {items.length === 0 ? <Empty title="Nothing recurring yet">Add rent, phone, streaming… Pocket Money links the payments automatically, reminds you before they are due and can book them for you.</Empty> : items.map(s => (
              <div key={s.id} className={`sub-row ${s.active ? '' : 'paused'}`} onClick={() => navigate(`/subscriptions/${s.id}`)}>
                <span className="cat-ico">{s.icon || s.category_icon || '↻'}</span>
                <span style={{ minWidth: 0 }}>
                  <div className="t">{s.name}</div>
                  <div className="s">{every(s)} · {s.mode === 'auto' ? 'booked automatically' : s.mode === 'remind' ? 'reminds' : 'tracked'}{s.account_name ? ` · ${s.account_name}` : ''}{s.last_amount > 0 && s.amount_max === 0 && Math.abs(s.last_amount - s.amount) / s.amount > 0.02 ? ' · price changed' : ''}</div>
                </span>
                <span className={`due ${s.status}`}>{dueText(s)}</span>
                <span style={{ textAlign: 'right' }}>
                  <span className={`amt ${s.kind}`}>{fmtMoney(s.amount, s.currency, decimalsOf(s.currency, currencies))}{s.amount_max > s.amount ? '+' : ''}</span>
                  {s.active && (s.status === 'overdue' || s.status === 'today' || s.status === 'soon') && s.mode !== 'auto' && <div><button className="btn sm" onClick={e => { e.stopPropagation(); pay(s) }}>{s.kind === 'income' ? 'Received' : 'Pay'}</button></div>}
                </span>
              </div>
            ))}
          </Card>
          {detected && detected.length > 0 && (
            <Card title="Looks like a subscription">
              <p className="muted small">Payments that repeat on a steady rhythm and are not tracked yet.</p>
              {detected.map((d, i) => (
                <div key={i} className="sub-row" style={{ cursor: 'default' }}>
                  <span className="cat-ico">🔍</span>
                  <span style={{ minWidth: 0 }}><div className="t">{d.name}</div><div className="s">{d.count}× · {every(d)} · last {fmtDate(d.last_date, false)} · ≈{m(d.monthly_home)}/month</div></span>
                  <span className="amt">{fmtMoney(d.amount, d.currency, decimalsOf(d.currency, currencies))}</span>
                  <button className="btn sm" onClick={() => setEditing({ name: d.name, kind: d.kind, amount: d.amount, currency: d.currency, account_id: d.account_id, category_id: d.category_id, freq: d.freq, every: d.every, next_date: d.next_date, start_date: d.next_date, mode: 'track', match_text: d.name.toLowerCase() })}>Track</button>
                </div>
              ))}
            </Card>
          )}
        </div>
        <div className="grid" style={{ gap: 16 }}>
          <Card title="Next 60 days">
            {data.upcoming.length === 0 ? <p className="muted small">Nothing due.</p> : (
              <div className="timeline">{data.upcoming.map((u, i) => (
                <Link key={i} to={`/subscriptions/${u.id}`} className="tl" style={{ color: 'inherit' }}>
                  <span className="when">{u.date === todayISO() ? 'today' : fmtDate(u.date, false)}</span><span>{u.icon || '↻'}</span><span className="nm">{u.name}</span>
                  <span className={`amt ${u.kind}`}>{fmtMoney(u.amount, u.currency || home, decimalsOf(u.currency || home, currencies))}</span>
                </Link>
              ))}</div>
            )}
          </Card>
          <CalendarFeed />
        </div>
      </div>
      {editing && <SubForm sub={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); reload(); reloadDetected() }} />}
    </>
  )
}

function CalendarFeed() {
  const { toast } = useToast()
  const [url, setUrl] = useState('')
  const make = async () => {
    const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: 'Calendar feed' })
    setUrl(`${window.location.origin}/cal/subscriptions.ics?token=${r.token}`)
  }
  return (
    <Card title="In your calendar">
      <p className="muted small">Subscribe from Apple/Google Calendar or Nextcloud to see every payment on its day.</p>
      {url ? <><input readOnly value={url} onFocus={e => e.target.select()} /><button className="btn sm mt-sm" onClick={() => { navigator.clipboard?.writeText(url); toast('Copied') }}>Copy</button></> : <button className="btn" onClick={make}>Create feed link</button>}
    </Card>
  )
}

export function SubForm({ sub, onClose, onSaved }: { sub: Partial<Subscription>; onClose: () => void; onSaved: (s: Subscription) => void }) {
  const { settings, acctById } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({
    name: sub.name || '', kind: (sub.kind || 'expense') as Kind, amount: sub.amount ?? null as number | null, amount_max: sub.amount_max || 0, currency: sub.currency || settings?.home_currency || 'EUR',
    account_id: sub.account_id ?? null as number | null, to_account_id: sub.to_account_id ?? null as number | null, category_id: sub.category_id ?? null as number | null, tags: sub.tags || [] as string[],
    payee: sub.payee || '', description: sub.description || '', freq: (sub.freq || 'monthly') as Freq, every: sub.every || 1, start_date: sub.start_date || '', next_date: sub.next_date || todayISO(),
    end_date: sub.end_date || '', mode: (sub.mode || 'remind') as SubMode, notify_days: sub.notify_days ?? 2, match_text: sub.match_text || '', url: sub.url || '', icon: sub.icon || '', notes: sub.notes || '', active: sub.active ?? true,
  })
  const [range, setRange] = useState(sub.amount_max ? true : false)
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const body = { ...f, amount: f.amount || 0, amount_max: range ? f.amount_max : 0, account_id: f.account_id || 0, to_account_id: f.to_account_id || 0, category_id: f.category_id || 0, start_date: f.start_date || f.next_date }
    try {
      const s = sub.id ? await api.put<Subscription>(`${V1}/subscriptions/${sub.id}`, body) : await api.post<Subscription>(`${V1}/subscriptions`, body)
      toast('Saved'); onSaved(s)
    } catch (err) { toast((err as Error).message, 'err') }
  }
  const acct = acctById(f.account_id)
  return (
    <Modal title={sub.id ? `Edit ${sub.name}` : 'New subscription or bill'} onClose={onClose} size="lg">
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <KindTabs value={f.kind} onChange={k => set('kind', k)} />
        <div className="two-col tight">
          <Field label="Name"><input value={f.name} onChange={e => set('name', e.target.value)} autoFocus placeholder="Netflix, Rent, Salary…" required /></Field>
          <Field label="Icon"><input value={f.icon} onChange={e => set('icon', e.target.value)} placeholder="from the category" maxLength={4} /></Field>
        </div>
        <div className="two-col tight">
          <Field label={range ? 'From' : 'Amount'} group><div className="input-group"><MoneyInput value={f.amount} code={f.currency} onChange={v => set('amount', v)} /><CurrencySelect value={f.currency} onChange={v => set('currency', v)} /></div></Field>
          {range ? <Field label="Up to"><MoneyInput value={f.amount_max} code={f.currency} onChange={v => set('amount_max', v ?? 0)} /></Field>
            : <Field label=" " group><button type="button" className="btn ghost sm" onClick={() => setRange(true)}>The amount varies…</button></Field>}
        </div>
        <div className="row wrap">
          <span>Every</span><input type="number" min={1} value={f.every} onChange={e => set('every', Math.max(1, Number(e.target.value)))} style={{ width: 70 }} />
          <Segmented value={f.freq} onChange={v => set('freq', v)} options={[{ value: 'daily', label: 'days' }, { value: 'weekly', label: 'weeks' }, { value: 'monthly', label: 'months' }, { value: 'yearly', label: 'years' }]} />
        </div>
        <div className="three-col tight" style={{ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }}>
          <Field label="Next due"><input type="date" value={f.next_date} onChange={e => set('next_date', e.target.value)} required /></Field>
          <Field label="Started"><input type="date" value={f.start_date} onChange={e => set('start_date', e.target.value)} /></Field>
          <Field label="Ends (optional)"><input type="date" value={f.end_date} onChange={e => set('end_date', e.target.value)} /></Field>
        </div>
        <div className="two-col tight">
          <Field label={f.kind === 'income' ? 'Into account' : 'From account'} group><AccountSelect value={f.account_id} onChange={id => set('account_id', id || null)} clearLabel="Any account" /></Field>
          {f.kind === 'transfer' ? <Field label="To account" group><AccountSelect value={f.to_account_id} onChange={id => set('to_account_id', id || null)} exclude={f.account_id} /></Field>
            : <Field label="Category" group><CategorySelect value={f.category_id} kind={f.kind} onChange={v => set('category_id', v)} /></Field>}
        </div>
        <Field label="When it is due" group>
          <Select value={f.mode} onChange={v => set('mode', v as SubMode)} options={(['remind', 'auto', 'track'] as SubMode[]).map(m => ({ value: m, label: MODE_LABEL[m], hint: m === 'auto' ? 'creates the transaction on the day' : m === 'remind' ? 'push + “Pay” button' : 'links payments you enter' }))} />
        </Field>
        <div className="two-col tight">
          <Field label="Push reminder" group>
            <Select value={String(f.notify_days)} onChange={v => set('notify_days', Number(v))} options={[{ value: '-1', label: 'No reminder' }, { value: '0', label: 'On the day' }, { value: '1', label: '1 day before' }, { value: '2', label: '2 days before' }, { value: '3', label: '3 days before' }, { value: '7', label: 'A week before' }, { value: '14', label: 'Two weeks before' }, { value: '30', label: 'A month before' }]} />
          </Field>
          <Field label="Recognise payments by" help="Words in the description or payee (default: the name)"><input value={f.match_text} onChange={e => set('match_text', e.target.value)} placeholder={f.name.toLowerCase()} /></Field>
        </div>
        <Field label="Tags" group><TagInput value={f.tags} onChange={v => set('tags', v)} /></Field>
        <div className="two-col tight">
          <Field label="Description on the transaction"><input value={f.description} onChange={e => set('description', e.target.value)} placeholder={f.name} /></Field>
          <Field label="Manage / cancel link"><input value={f.url} onChange={e => set('url', e.target.value)} placeholder="https://…" /></Field>
        </div>
        <Field label="Notes"><textarea value={f.notes} onChange={e => set('notes', e.target.value)} rows={2} /></Field>
        {sub.id && <label className="row"><input type="checkbox" checked={f.active} onChange={e => set('active', e.target.checked)} style={{ width: 'auto' }} /> Active</label>}
        {f.mode === 'auto' && !acct && <div className="callout">Automatic booking needs an account.</div>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

export function SubscriptionPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { home, currencies } = useApp()
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<{ subscription: Subscription; payments: Transaction[]; next: string[] }>(`${V1}/subscriptions/${id}`), [id])
  const [editing, setEditing] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  if (!data) return <Loading />
  const s = data.subscription
  const dec = decimalsOf(s.currency, currencies)
  const act = async (what: 'pay' | 'skip') => {
    try { await api.post(`${V1}/subscriptions/${s.id}/${what}`, {}); toast(what === 'pay' ? 'Paid' : 'Skipped to the next date'); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const relink = async () => { const r = await api.post<{ linked: number }>(`${V1}/subscriptions/${s.id}/link`, { relink: true }); toast(`${r.linked} payments linked`); reload() }
  const del = async () => { if (confirm(`Delete ${s.name}? Its payments stay, unlinked.`)) { await api.del(`${V1}/subscriptions/${s.id}`); navigate('/subscriptions') } }
  return (
    <>
      <PageHeader title={<>{s.icon || s.category_icon || '↻'} {s.name}</>} sub={<>{fmtMoney(s.amount, s.currency, dec)}{s.amount_max > s.amount ? `–${fmtMoney(s.amount_max, s.currency, dec)}` : ''} {every(s)} · {MODE_LABEL[s.mode]}{s.account_name ? ` · ${s.account_name}` : ''}</>} actions={<>
        {s.active && <button className="btn primary" onClick={() => act('pay')}>Pay now</button>}
        {s.active && <button className="btn" onClick={() => act('skip')}>Skip this one</button>}
        <button className="btn" onClick={() => setEditing(true)}>Edit</button>
      </>} />
      <div className="stats">
        <div className="stat"><div className="n">{dueText(s)}</div><div className="l">next {fmtDate(s.next_date)}</div></div>
        <div className="stat"><div className="n">{fmtMoney(Math.abs(s.monthly_home), home, decimalsOf(home, currencies))}</div><div className="l">a month</div></div>
        <div className="stat"><div className="n">{fmtMoney(Math.abs(s.yearly_home), home, decimalsOf(home, currencies))}</div><div className="l">a year</div></div>
        <div className="stat"><div className="n">{s.paid_count}</div><div className="l">payments linked{s.last_paid ? ` · last ${fmtDate(s.last_paid, false)}` : ''}</div></div>
      </div>
      <div className="home-grid">
        <Card title="Payments" actions={<button className="btn ghost sm" onClick={relink} title="Find past payments that match">Find payments</button>}>
          {data.payments.length === 0 ? <p className="muted">None linked yet. New transactions that match the name and amount around the due date are linked automatically.</p> : <TxList items={data.payments} onOpen={t => setOpen(t.id)} />}
        </Card>
        <Card title="Next dates">
          <div className="timeline">{data.next.map(d => <div key={d} className="tl"><span className="when">{fmtDate(d, false)}</span><span /><span className="nm muted">{d.slice(0, 4)}</span><span className="amt">{fmtMoney(s.amount, s.currency, dec)}</span></div>)}</div>
          {s.url && <a className="btn mt" href={s.url} target="_blank" rel="noreferrer">Manage / cancel ↗</a>}
          {s.notes && <p className="mt small">{s.notes}</p>}
        </Card>
      </div>
      <div className="row mt"><span className="spacer" /><button className="btn ghost danger-text sm" onClick={del}>Delete subscription</button></div>
      {editing && <SubForm sub={s} onClose={() => setEditing(false)} onSaved={() => { setEditing(false); reload() }} />}
      {open && <TxModal id={open} onClose={() => setOpen(null)} onChanged={reload} />}
    </>
  )
}
