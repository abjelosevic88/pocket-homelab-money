import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { addDays, decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import { KIND_ICON, KIND_LABEL, LIABILITY, type Account, type AccountKind, type AccountMember, type DayAmount, type Payoff, type Transaction, type TxTotals } from '../lib/types'
import { Card, Empty, Field, Loading, Modal, PageHeader, Segmented, useAsync, useToast } from '../components/ui'
import { CurrencySelect, MoneyInput } from '../components/Money'
import { Line } from '../components/Charts'
import { TxList } from '../components/TxList'
import { TxModal } from '../components/TxEditor'
import { Select } from '../components/Select'

const COLORS = ['#047857', '#2563eb', '#7c3aed', '#db2777', '#d97706', '#0d9488', '#475569', '#dc2626']

export function AccountForm({ account, onClose, onSaved }: { account?: Account; onClose: () => void; onSaved: (a: Account) => void }) {
  const { settings, meta, currencies } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({
    name: account?.name || '', kind: (account?.kind || 'checking') as AccountKind, currency: account?.currency || settings?.home_currency || 'EUR',
    opening_balance: account?.opening_balance ?? 0, opening_date: account?.opening_date || '', institution: account?.institution || '', number: account?.number || '',
    color: account?.color || '', icon: account?.icon || '', credit_limit: account?.credit_limit ?? 0, include_net_worth: account?.include_net_worth ?? true, everyday: account?.everyday ?? true,
    archived: account?.archived ?? false, notes: account?.notes || '',
    direction: account?.direction || 'owe', interest_rate: account?.interest_rate ?? 0, interest_auto: account?.interest_auto ?? false,
    principal: account?.principal ?? 0, payment: account?.payment ?? 0, payment_day: account?.payment_day ?? 0,
  })
  const liability = LIABILITY.includes(f.kind)
  const [balance, setBalance] = useState<number | null>(null)
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      // a new liability is typed as what is still owed; I owe it = a negative balance
      const start = liability && f.direction === 'owe' ? -Math.abs(balance ?? 0) : balance ?? 0
      const body = { ...f, opening_balance: account ? f.opening_balance : start, opening_date: account ? f.opening_date : f.opening_date || todayISO() }
      const a = account ? await api.put<Account>(`${V1}/accounts/${account.id}`, body) : await api.post<Account>(`${V1}/accounts`, body)
      toast(account ? 'Saved' : 'Account created')
      onSaved(a)
    } catch (err) { toast((err as Error).message, 'err') }
  }
  const kinds = meta?.account_kinds?.length ? meta.account_kinds : (Object.keys(KIND_LABEL) as AccountKind[])
  return (
    <Modal title={account ? `Edit ${account.name}` : 'New account'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <Field label="Name"><input value={f.name} onChange={e => set('name', e.target.value)} autoFocus required placeholder="UniCredit, Cash, Revolut…" /></Field>
        <div className="two-col tight">
          <Field label="Type" group><Select value={f.kind} onChange={v => { const k = v as AccountKind; set('kind', k); if (!account && LIABILITY.includes(k)) set('everyday', false) }} options={kinds.map(k => ({ value: k, label: `${KIND_ICON[k]} ${KIND_LABEL[k]}` }))} /></Field>
          <Field label="Currency" group><CurrencySelect value={f.currency} onChange={v => set('currency', v)} disabled={Boolean(account && account.tx_count > 0)} /></Field>
        </div>
        {!account ? (
          <Field label={liability ? 'Still owed today' : 'Balance today'} help={liability ? 'The amount left to pay off, as a positive number.' : 'What the bank app shows right now. You can correct it later with “Balance check”.'}>
            <MoneyInput value={balance} code={f.currency} onChange={setBalance} />
          </Field>
        ) : (
          <div className="two-col tight">
            <Field label="Opening balance" help="Before the first transaction"><MoneyInput value={f.opening_balance} code={f.currency} onChange={v => set('opening_balance', v ?? 0)} /></Field>
            <Field label="Opening date"><input type="date" value={f.opening_date} onChange={e => set('opening_date', e.target.value)} /></Field>
          </div>
        )}
        <div className="two-col tight">
          <Field label="Bank"><input value={f.institution} onChange={e => set('institution', e.target.value)} /></Field>
          <Field label="IBAN / number"><input value={f.number} onChange={e => set('number', e.target.value)} /></Field>
        </div>
        {liability && <>
          <Field label="Direction" group>
            <Segmented value={f.direction} onChange={v => set('direction', v)} options={[{ value: 'owe', label: 'I owe it' }, { value: 'owed', label: 'Someone owes me' }]} />
          </Field>
          <div className="two-col tight">
            <Field label="Original amount" help="Borrowed or lent at the start — for the progress bar"><MoneyInput value={f.principal} code={f.currency} onChange={v => set('principal', v ?? 0)} /></Field>
            <Field label="Interest rate (% a year)"><input type="number" min={0} max={100} step="0.01" value={f.interest_rate || ''} onChange={e => set('interest_rate', Number(e.target.value) || 0)} placeholder="0" /></Field>
          </div>
          <div className="two-col tight">
            <Field label="Monthly payment" help="Leave empty to use the average of the last 3 months"><MoneyInput value={f.payment} code={f.currency} onChange={v => set('payment', v ?? 0)} /></Field>
            <Field label="Payment day" help="Day of the month, 1–28"><input type="number" min={0} max={28} value={f.payment_day || ''} onChange={e => set('payment_day', Math.min(28, Math.max(0, Number(e.target.value) || 0)))} placeholder="—" /></Field>
          </div>
          <label className="row"><input type="checkbox" checked={f.interest_auto} onChange={e => set('interest_auto', e.target.checked)} style={{ width: 'auto' }} disabled={!f.interest_rate} /> Book the interest automatically each month on the payment day</label>
        </>}
        {f.kind === 'credit' && <Field label="Credit limit"><MoneyInput value={f.credit_limit} code={f.currency} onChange={v => set('credit_limit', v ?? 0)} /></Field>}
        <div className="two-col tight">
          <Field label="Icon (emoji)"><input value={f.icon} onChange={e => set('icon', e.target.value)} placeholder={KIND_ICON[f.kind]} maxLength={4} /></Field>
          <Field label="Colour" group><div className="row wrap">{COLORS.map(c => <button type="button" key={c} className={`color-dot ${f.color === c ? 'active' : ''}`} style={{ background: c }} onClick={() => set('color', c)} aria-label={c} />)}</div></Field>
        </div>
        <label className="row"><input type="checkbox" checked={f.include_net_worth} onChange={e => set('include_net_worth', e.target.checked)} style={{ width: 'auto' }} /> Count in net worth</label>
        <label className="row"><input type="checkbox" checked={f.everyday} onChange={e => set('everyday', e.target.checked)} style={{ width: 'auto' }} /> Everyday money — counts in “safe to spend” and the forecast (untick savings, business or foreign-currency accounts)</label>
        {account && <label className="row"><input type="checkbox" checked={f.archived} onChange={e => set('archived', e.target.checked)} style={{ width: 'auto' }} /> Archived (closed account — hidden from pickers)</label>}
        <Field label="Notes"><textarea value={f.notes} onChange={e => set('notes', e.target.value)} rows={2} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">{account ? 'Save' : 'Create'}</button></div>
        {!account && currencies.length === 0 && <span />}
      </form>
    </Modal>
  )
}

export default function Accounts() {
  const { accounts, reloadAccounts, home, currencies } = useApp()
  const [creating, setCreating] = useState(false)
  const [showArchived, setShowArchived] = useState(false)
  useEffect(() => { reloadAccounts() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const active = accounts.filter(a => !a.archived)
  const net = active.filter(a => a.include_net_worth).reduce((s, a) => s + a.balance_home, 0)
  const dec = decimalsOf(home, currencies)
  const byKind = (Object.keys(KIND_LABEL) as AccountKind[]).map(k => ({ k, list: active.filter(a => a.kind === k) })).filter(g => g.list.length)
  return (
    <>
      <PageHeader title="Accounts" sub={<>Net worth <b>{fmtMoney(net, home, dec)}</b></>} actions={<button className="btn primary" onClick={() => setCreating(true)}>＋ Account</button>} />
      {accounts.length === 0 ? <Empty title="No accounts yet">Create one, or import from Firefly III under Settings → Import.</Empty> : (
        <div className="grid" style={{ gap: 18 }}>
          {byKind.map(g => (
            <div key={g.k}>
              <h3 className="muted small mb-sm" style={{ textTransform: 'uppercase', letterSpacing: '.04em' }}>{KIND_LABEL[g.k]} · {fmtMoney(g.list.reduce((s, a) => s + a.balance_home, 0), home, dec)}</h3>
              <div className="acct-grid">{g.list.map(a => <AccountCard key={a.id} a={a} />)}</div>
            </div>
          ))}
          {accounts.some(a => a.archived) && <>
            <button className="btn ghost sm" style={{ justifySelf: 'start' }} onClick={() => setShowArchived(s => !s)}>{showArchived ? 'Hide' : 'Show'} archived ({accounts.filter(a => a.archived).length})</button>
            {showArchived && <div className="acct-grid">{accounts.filter(a => a.archived).map(a => <AccountCard key={a.id} a={a} />)}</div>}
          </>}
        </div>
      )}
      {creating && <AccountForm onClose={() => setCreating(false)} onSaved={() => { setCreating(false); reloadAccounts() }} />}
    </>
  )
}

function AccountCard({ a }: { a: Account }) {
  const { currencies, home } = useApp()
  return (
    <Link to={`/accounts/${a.id}`} className={`acct-card ${a.archived ? 'archived' : ''}`} style={{ ['--c' as string]: a.color || undefined }}>
      <div className="top"><span className="cat-ico">{a.icon || KIND_ICON[a.kind]}</span><span className="grow"><div className="nm">{a.name}</div><div className="muted small">{a.institution || KIND_LABEL[a.kind]}{a.role !== 'owner' ? ` · shared (${a.role})` : a.members?.length ? ' · shared' : ''}</div></span></div>
      <div className={`bal ${a.balance < 0 ? 'neg' : ''}`}>{fmtMoney(a.balance, a.currency, decimalsOf(a.currency, currencies))}</div>
      <div className="muted small">{a.currency !== home ? `≈ ${fmtMoney(a.balance_home, home, decimalsOf(home, currencies))} · ` : ''}{a.tx_count} transactions{a.last_tx_date ? ` · last ${fmtDate(a.last_tx_date, false)}` : ''}</div>
    </Link>
  )
}

export function AccountPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { currencies, home, users, user, reloadAccounts } = useApp()
  const { toast } = useToast()
  const aid = Number(id)
  const { data: a, reload } = useAsync(() => api.get<Account>(`${V1}/accounts/${aid}`), [aid])
  const [range, setRange] = useState<'90' | '365' | 'all'>('365')
  const { data: hist } = useAsync(() => api.get<{ days: DayAmount[] }>(`${V1}/accounts/${aid}/history?from=${range === 'all' ? '2000-01-01' : addDays(todayISO(), -Number(range))}`), [aid, range])
  const [txs, setTxs] = useState<{ items: Transaction[]; totals: TxTotals; has_more: boolean } | null>(null)
  const loadTx = () => api.get<{ items: Transaction[]; totals: TxTotals; has_more: boolean }>(`${V1}/transactions?account=${aid}&limit=60`).then(setTxs)
  useEffect(() => { loadTx() }, [aid]) // eslint-disable-line react-hooks/exhaustive-deps
  const [editing, setEditing] = useState(false)
  const [reconcile, setReconcile] = useState(false)
  const [sharing, setSharing] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  const [adding, setAdding] = useState(false)
  if (!a) return <Loading />
  const dec = decimalsOf(a.currency, currencies)
  const points = (hist?.days || []).filter((_, i, arr) => arr.length < 400 || i % Math.ceil(arr.length / 400) === 0 || i === arr.length - 1).map(d => ({ x: d.date, y: d.amount }))
  const refresh = () => { reload(); loadTx(); reloadAccounts() }
  return (
    <>
      <PageHeader title={<>{a.icon || KIND_ICON[a.kind]} {a.name}</>} sub={<>{KIND_LABEL[a.kind]}{a.institution ? ` · ${a.institution}` : ''}{a.number ? ` · ${a.number}` : ''}</>} actions={<>
        <button className="btn primary" onClick={() => setAdding(true)} disabled={a.role === 'viewer'}>＋ Transaction</button>
        <button className="btn" onClick={() => setReconcile(true)} disabled={a.role === 'viewer'} title="Tell it what the bank says; the difference is booked">Balance check</button>
        {a.role === 'owner' && <button className="btn" onClick={() => setSharing(true)}>Share</button>}
        {a.role === 'owner' && <button className="btn" onClick={() => setEditing(true)}>Edit</button>}
      </>} />
      <div className="stats">
        <div className="stat"><div className="n" style={{ color: a.balance < 0 ? 'var(--red-ink)' : undefined }}>{fmtMoney(a.balance, a.currency, dec)}</div><div className="l">Balance{a.currency !== home ? ` · ≈ ${fmtMoney(a.balance_home, home, decimalsOf(home, currencies))}` : ''}</div></div>
        {txs && <div className="stat"><div className="n ok">{fmtMoney(txs.totals.income, home, decimalsOf(home, currencies), { compact: true })}</div><div className="l">In (all time, {home})</div></div>}
        {txs && <div className="stat"><div className="n">{fmtMoney(txs.totals.expense, home, decimalsOf(home, currencies), { compact: true })}</div><div className="l">Out (all time, {home})</div></div>}
        <div className="stat"><div className="n">{a.tx_count}</div><div className="l">Transactions</div></div>
        {a.kind === 'credit' && a.credit_limit > 0 && <div className="stat"><div className="n">{fmtMoney(a.credit_limit + a.balance, a.currency, dec)}</div><div className="l">Available credit</div></div>}
      </div>
      {LIABILITY.includes(a.kind) && <PayoffCard a={a} onBooked={refresh} />}
      <Card title="Balance" actions={<Segmented size="sm" value={range} onChange={setRange} options={[{ value: '90', label: '3 months' }, { value: '365', label: 'Year' }, { value: 'all', label: 'All' }]} />}>
        <Line points={points} fmt={v => fmtMoney(v, a.currency, dec)} area zero height={180} />
      </Card>
      <div className="mt">
        <Card title="Transactions" actions={<Link className="btn ghost sm" to={`/transactions?account=${a.id}`}>Filter & search →</Link>}>
          {!txs ? <Loading /> : txs.items.length === 0 ? <Empty title="No transactions yet" /> : <TxList items={txs.items} onOpen={t => setOpen(t.id)} showAccount={false} perspective={a.id} />}
        </Card>
      </div>
      {a.notes && <div className="mt callout">{a.notes}</div>}
      {editing && <AccountForm account={a} onClose={() => setEditing(false)} onSaved={() => { setEditing(false); refresh() }} />}
      {reconcile && <Reconcile a={a} onClose={() => setReconcile(false)} onDone={() => { setReconcile(false); refresh() }} />}
      {sharing && <Share a={a} users={users.filter(u => u.id !== user?.id)} onClose={() => setSharing(false)} onDone={() => { setSharing(false); refresh() }} />}
      {(open || adding) && <TxModal id={open || undefined} seed={{ account_id: a.id }} onClose={() => { setOpen(null); setAdding(false) }} onChanged={refresh} />}
      {editing && a.role === 'owner' && <span />}
      <div className="mt row">
        <span className="spacer" />
        {a.role === 'owner' && <button className="btn ghost danger-text sm" onClick={async () => {
          if (!confirm(`Delete ${a.name} and all ${a.tx_count} of its transactions? This cannot be undone — archive it instead to keep the history.`)) return
          await api.del(`${V1}/accounts/${a.id}`); toast('Deleted'); reloadAccounts(); navigate('/accounts')
        }}>Delete account</button>}
      </div>
    </>
  )
}

function Reconcile({ a, onClose, onDone }: { a: Account; onClose: () => void; onDone: () => void }) {
  const { currencies } = useApp()
  const { toast } = useToast()
  const [bal, setBal] = useState<number | null>(a.balance)
  const [date, setDate] = useState(todayISO())
  const dec = decimalsOf(a.currency, currencies)
  const diff = (bal ?? 0) - a.balance
  const go = async () => {
    const r = await api.post<{ difference: number }>(`${V1}/accounts/${a.id}/reconcile`, { balance: bal ?? 0, date })
    toast(r.difference === 0 ? 'Already matches' : `Booked ${fmtMoney(r.difference, a.currency, dec, { sign: true })}`)
    onDone()
  }
  return (
    <Modal title="Balance check" onClose={onClose}>
      <p className="muted">Type what your bank shows. If it differs, an adjustment is booked so the balance matches from then on.</p>
      <div className="two-col tight">
        <Field label="Bank says"><MoneyInput value={bal} code={a.currency} onChange={setBal} autoFocus /></Field>
        <Field label="On"><input type="date" value={date} onChange={e => setDate(e.target.value)} /></Field>
      </div>
      <p className="mt">Pocket Money has <b>{fmtMoney(a.balance, a.currency, dec)}</b>{diff !== 0 && <> — difference <b className={diff > 0 ? 'ok' : 'bad'}>{fmtMoney(diff, a.currency, dec, { sign: true })}</b></>}.</p>
      <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={go}>{diff === 0 ? 'Matches' : 'Book adjustment'}</button></div>
    </Modal>
  )
}

function Share({ a, users, onClose, onDone }: { a: Account; users: { id: number; name: string; email: string }[]; onClose: () => void; onDone: () => void }) {
  const [members, setMembers] = useState<AccountMember[]>(a.members || [])
  const role = (uid: number) => members.find(m => m.user_id === uid)?.role || ''
  const setRole = (uid: number, r: string) => setMembers(ms => r ? [...ms.filter(m => m.user_id !== uid), { user_id: uid, name: '', email: '', role: r as 'editor' | 'viewer' }] : ms.filter(m => m.user_id !== uid))
  const save = async () => { await api.put(`${V1}/accounts/${a.id}/members`, { members }); onDone() }
  return (
    <Modal title={`Share ${a.name}`} onClose={onClose}>
      {users.length === 0 ? <p className="muted">No other users yet — add them under Settings → Users.</p> : (
        <div className="grid" style={{ gap: 8 }}>
          {users.map(u => (
            <div key={u.id} className="row"><span className="grow">{u.name || u.email}<div className="muted small">{u.email}</div></span>
              <Segmented size="sm" value={role(u.id)} onChange={v => setRole(u.id, v)} options={[{ value: '', label: 'No access' }, { value: 'viewer', label: 'Can see' }, { value: 'editor', label: 'Can book' }]} />
            </div>
          ))}
        </div>
      )}
      <div className="form-actions"><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save}>Save</button></div>
    </Modal>
  )
}

function PayoffCard({ a, onBooked }: { a: Account; onBooked: () => void }) {
  const { currencies } = useApp()
  const { toast } = useToast()
  const { data: p, reload } = useAsync(() => api.get<Payoff>(`${V1}/accounts/${a.id}/payoff`), [a.id, a.balance])
  const [all, setAll] = useState(false)
  if (!p) return null
  const dec = decimalsOf(a.currency, currencies)
  const m = (v: number) => fmtMoney(v, a.currency, dec)
  const owe = p.direction === 'owe'
  const book = async () => {
    if (!confirm(`Book ${m(p.month_interest)} interest on ${a.name} today?`)) return
    try { await api.post(`${V1}/accounts/${a.id}/interest`); toast('Interest booked'); reload(); onBooked() } catch (e) { toast((e as Error).message, 'err') }
  }
  const rows = all ? p.schedule : p.schedule.slice(0, 6)
  return (
    <Card className="mb" title={owe ? 'Paying it off' : 'Getting it back'} actions={p.rate > 0 && p.outstanding > 0 && a.role !== 'viewer' && <button className="btn sm" onClick={book}>Book this month's interest</button>}>
      <div className="stats">
        <div className="stat"><div className="n">{m(p.outstanding)}</div><div className="l">{owe ? 'Still owed' : 'Still to get back'}</div></div>
        {p.principal > 0 && <div className="stat"><div className="n ok">{p.pct}%</div><div className="l">{owe ? 'Repaid' : 'Returned'} · {m(p.repaid)} of {m(p.principal)}</div></div>}
        <div className="stat"><div className="n">{p.payment > 0 ? m(p.payment) : '—'}</div><div className="l">A month{p.payment_estimated ? ' (3-month average)' : ''}</div></div>
        {p.rate > 0 && <div className="stat"><div className="n">{m(p.month_interest)}</div><div className="l">Interest this month · {p.rate}% a year</div></div>}
        <div className="stat"><div className="n">{p.outstanding === 0 ? 'Done 🎉' : p.never ? 'Never' : fmtDate(p.payoff_date)}</div><div className="l">{p.never ? 'the payment does not cover the interest' : p.outstanding === 0 ? 'Nothing left' : `${p.months_left} payments left${p.interest_left > 0 ? ` · ${m(p.interest_left)} interest` : ''}`}</div></div>
      </div>
      {p.principal > 0 && <div className="bar lg mt-sm"><span className="fill" style={{ width: `${Math.min(100, p.pct)}%` }} /></div>}
      {p.schedule.length > 0 && (
        <div className="mt">
          <table className="table compact">
            <thead><tr><th>Date</th><th className="num">Payment</th><th className="num">Interest</th><th className="num">Principal</th><th className="num">Left after</th></tr></thead>
            <tbody>{rows.map(r => <tr key={r.date}><td>{fmtDate(r.date)}</td><td className="num">{m(r.payment)}</td><td className="num">{m(r.interest)}</td><td className="num">{m(r.principal)}</td><td className="num">{m(r.remaining)}</td></tr>)}</tbody>
          </table>
          {p.schedule.length > 6 && <button className="btn ghost sm mt-sm" onClick={() => setAll(x => !x)}>{all ? 'Show less' : `Show ${p.schedule.length} months`}</button>}
        </div>
      )}
    </Card>
  )
}
