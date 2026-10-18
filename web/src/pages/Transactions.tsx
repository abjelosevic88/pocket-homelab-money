import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { addDays, decimalsOf, fmtMoney, todayISO } from '../lib/format'
import type { Transaction, TxTotals } from '../lib/types'
import { Empty, Loading, PageHeader, useDebounce, useToast } from '../components/ui'
import { Select } from '../components/Select'
import { TxList } from '../components/TxList'
import { TxModal } from '../components/TxEditor'
import { CategorySelect } from '../components/pickers'

type Page = { items: Transaction[]; totals: TxTotals; has_more: boolean }

function monthStart(d: string) { return d.slice(0, 8) + '01' }
function prevMonth(d: string): [string, string] {
  const [y, m] = d.split('-').map(Number)
  const s = new Date(y, m - 2, 1), e = new Date(y, m - 1, 0)
  const f = (x: Date) => `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, '0')}-${String(x.getDate()).padStart(2, '0')}`
  return [f(s), f(e)]
}

const PRESETS = [
  { id: 'all', label: 'All time' },
  { id: 'month', label: 'This month' },
  { id: 'last', label: 'Last month' },
  { id: '30', label: '30 days' },
  { id: 'year', label: 'This year' },
]

export default function Transactions() {
  const { accounts, categories, tags, home, currencies, reloadAccounts } = useApp()
  const { toast } = useToast()
  const [sp, setSp] = useSearchParams()
  const { id: openId } = useParams()
  const navigate = useNavigate()
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q, 250)
  const [page, setPage] = useState<Page | null>(null)
  const [busy, setBusy] = useState(false)
  const [sel, setSel] = useState<Set<number>>(new Set())
  const [open, setOpen] = useState<number | null>(openId ? Number(openId) : null)
  const [creating, setCreating] = useState(false)
  const [bulkCat, setBulkCat] = useState<number | null>(null)
  const [bulkTag, setBulkTag] = useState('')

  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  useEffect(() => { set('q', dq) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const query = useMemo(() => {
    const n = new URLSearchParams(sp)
    n.delete('preset')
    return n.toString()
  }, [sp])
  const load = async (append = false) => {
    setBusy(true)
    try {
      const off = append && page ? page.items.length : 0
      const r = await api.get<Page>(`${V1}/transactions?${query}&limit=100&offset=${off}`)
      setPage(p => append && p ? { ...r, items: [...p.items, ...r.items] } : r)
    } finally { setBusy(false) }
  }
  useEffect(() => { load(false); setSel(new Set()) }, [query]) // eslint-disable-line react-hooks/exhaustive-deps

  const preset = (id: string) => {
    const n = new URLSearchParams(sp)
    const t = todayISO()
    n.delete('from'); n.delete('to')
    if (id === 'month') n.set('from', monthStart(t))
    if (id === 'last') { const [a, b] = prevMonth(t); n.set('from', a); n.set('to', b) }
    if (id === '30') n.set('from', addDays(t, -29))
    if (id === 'year') n.set('from', t.slice(0, 4) + '-01-01')
    setSp(n, { replace: true })
  }
  const activePreset = (() => {
    const f = sp.get('from') || '', to = sp.get('to') || '', t = todayISO()
    if (!f && !to) return 'all'
    if (f === monthStart(t) && !to) return 'month'
    const [a, b] = prevMonth(t)
    if (f === a && to === b) return 'last'
    if (f === addDays(t, -29) && !to) return '30'
    if (f === t.slice(0, 4) + '-01-01' && !to) return 'year'
    return ''
  })()
  const dec = decimalsOf(home, currencies)
  const toggle = (id: number) => setSel(s => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const bulk = async (action: string, extra: Record<string, unknown> = {}) => {
    if (action === 'delete' && !confirm(`Delete ${sel.size} transactions?`)) return
    const r = await api.post<{ count: number }>(`${V1}/transactions/bulk`, { ids: [...sel], action, ...extra })
    toast(`${r.count} updated`)
    setSel(new Set()); setBulkTag(''); setBulkCat(null)
    load(false); reloadAccounts()
  }
  const closeModal = () => { setOpen(null); if (openId) navigate('/transactions', { replace: true }) }
  const hasFilters = ['account', 'category', 'tag', 'kind', 'q', 'from', 'to', 'payee', 'subscription', 'source'].some(k => sp.get(k))

  return (
    <>
      <PageHeader title="Transactions" sub={page ? `${page.totals.count.toLocaleString()} found` : ''} actions={<>
        <a className="btn" href={`${V1}/export/transactions.csv?${query}`}>Export CSV</a>
        <button className="btn primary" onClick={() => setCreating(true)}>＋ Add</button>
      </>} />
      <div className="toolbar">
        <div className="search"><span className="ico">⌕</span><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search description, payee, notes, category, tag…" /></div>
        <Select value={sp.get('account') || ''} onChange={v => set('account', v)} options={accounts.map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} placeholder="All accounts" clearLabel="All accounts" />
        <Select value={sp.get('category') || ''} onChange={v => set('category', v)} options={[{ value: 'none', label: '∅ Uncategorised' }, ...categories.map(c => ({ value: String(c.id), label: `${c.icon || '•'} ${c.full_name}` }))]} placeholder="All categories" clearLabel="All categories" />
        <Select value={sp.get('tag') || ''} onChange={v => set('tag', v)} options={tags.map(t => ({ value: t.name, label: `#${t.name}`, hint: String(t.tx_count) }))} placeholder="Any tag" clearLabel="Any tag" />
        <Select value={sp.get('kind') || ''} onChange={v => set('kind', v)} options={[{ value: 'expense', label: 'Expenses' }, { value: 'income', label: 'Income' }, { value: 'transfer', label: 'Transfers' }]} placeholder="All kinds" clearLabel="All kinds" />
      </div>
      <div className="filters">
        {PRESETS.map(p => <button key={p.id} type="button" className={`chip ${activePreset === p.id ? 'active' : ''}`} onClick={() => preset(p.id)}>{p.label}</button>)}
        <input type="date" value={sp.get('from') || ''} onChange={e => set('from', e.target.value)} style={{ width: 'auto' }} aria-label="From" />
        <span className="muted">–</span>
        <input type="date" value={sp.get('to') || ''} onChange={e => set('to', e.target.value)} style={{ width: 'auto' }} aria-label="To" />
        {hasFilters && <button className="btn ghost sm" onClick={() => { setQ(''); setSp(new URLSearchParams(), { replace: true }) }}>Clear filters</button>}
      </div>
      {page && page.totals.count > 0 && (
        <div className="totals-strip">
          <span>In <b className="ok">{fmtMoney(page.totals.income, home, dec)}</b></span>
          <span>Out <b>{fmtMoney(page.totals.expense, home, dec)}</b></span>
          <span>Net <b className={page.totals.net >= 0 ? 'ok' : 'bad'}>{fmtMoney(page.totals.net, home, dec, { sign: true })}</b></span>
          {page.totals.transfers > 0 && <span className="muted">Transfers {fmtMoney(page.totals.transfers, home, dec)}</span>}
          <span className="spacer" />
          <button className="btn ghost sm" onClick={() => setSel(sel.size ? new Set() : new Set(page.items.map(t => t.id)))}>{sel.size ? 'Clear selection' : 'Select all shown'}</button>
        </div>
      )}
      {!page ? <Loading /> : page.items.length === 0 ? <Empty title="Nothing here">{hasFilters ? 'No transaction matches these filters.' : 'Add your first one with ＋ Add.'}</Empty> : (
        <div className="list-card">
          <TxList items={page.items} onOpen={t => setOpen(t.id)} selected={sel} onToggle={toggle} />
          {page.has_more && <div className="row" style={{ justifyContent: 'center', padding: 10 }}><button className="btn" disabled={busy} onClick={() => load(true)}>{busy ? 'Loading…' : 'Load more'}</button></div>}
        </div>
      )}
      {sel.size > 0 && (
        <div className="bulk-bar">
          <b>{sel.size} selected</b>
          <div style={{ width: 220 }}><CategorySelect value={bulkCat} onChange={setBulkCat} kind="any" placeholder="Set category…" /></div>
          <button className="btn sm" disabled={!bulkCat} onClick={() => bulk('category', { category_id: bulkCat })}>Apply</button>
          <input value={bulkTag} onChange={e => setBulkTag(e.target.value)} placeholder="tag" style={{ width: 120 }} />
          <button className="btn sm" disabled={!bulkTag.trim()} onClick={() => bulk('add_tags', { tags: [bulkTag] })}>+ Tag</button>
          <button className="btn sm" disabled={!bulkTag.trim()} onClick={() => bulk('remove_tags', { tags: [bulkTag] })}>− Tag</button>
          <span className="spacer" />
          <button className="btn sm" onClick={() => bulk('delete')}>Delete</button>
        </div>
      )}
      {(open || creating) && <TxModal id={open || undefined} onClose={() => { creating ? setCreating(false) : closeModal() }} onChanged={() => load(false)} />}
    </>
  )
}
