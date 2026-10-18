import { useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney } from '../lib/format'
import { KIND_ICON, type Category, type Kind, type Transaction } from '../lib/types'
import { Select } from './Select'

/** Accounts as a searchable select; writable ones only unless told otherwise. */
export function AccountSelect({ value, onChange, placeholder = 'Account…', all = false, exclude, clearLabel }: { value: number | null; onChange: (id: number) => void; placeholder?: string; all?: boolean; exclude?: number | null; clearLabel?: string }) {
  const { accounts } = useApp()
  const opts = accounts.filter(a => (all || a.role !== 'viewer') && (!a.archived || a.id === value) && a.id !== exclude)
    .map(a => ({ value: String(a.id), label: `${a.icon || KIND_ICON[a.kind]} ${a.name}`, hint: a.currency }))
  return <Select value={value ? String(value) : ''} onChange={v => onChange(Number(v) || 0)} options={opts} placeholder={placeholder} ariaLabel="Account" clearLabel={clearLabel} />
}

function catLabel(c: Category, all: Category[]) {
  const parent = c.parent_id ? all.find(p => p.id === c.parent_id) : undefined
  return `${c.icon || '•'} ${parent ? parent.name + ' › ' : ''}${c.name}`
}

/** Category select with "create" — new categories get an emoji guessed from the name. */
export function CategorySelect({ value, onChange, kind = 'expense', placeholder = 'Category…', clearLabel = 'No category' }: { value: number | null; onChange: (id: number | null) => void; kind?: Kind | 'any'; placeholder?: string; clearLabel?: string }) {
  const { categories, reloadCatalog } = useApp()
  const opts = categories.filter(c => !c.archived || c.id === value).filter(c => kind === 'any' || kind === 'transfer' || c.kind === 'both' || c.kind === kind || c.id === value)
    .map(c => ({ value: String(c.id), label: catLabel(c, categories) }))
  const create = async (name: string) => {
    const c = await api.post<Category>(`${V1}/categories`, { name, kind: kind === 'income' ? 'income' : 'expense' })
    await reloadCatalog()
    return String(c.id)
  }
  return <Select value={value ? String(value) : ''} onChange={v => onChange(v ? Number(v) : null)} options={opts} placeholder={placeholder} onCreate={create} createLabel="New category" clearLabel={clearLabel} ariaLabel="Category" />
}

/** Big tappable category buttons (fast on a phone), most used first. */
export function CategoryGrid({ value, onChange, kind = 'expense' }: { value: number | null; onChange: (id: number | null) => void; kind?: Kind }) {
  const { categories } = useApp()
  const [q, setQ] = useState('')
  const list = useMemo(() => {
    const s = q.trim().toLowerCase()
    return categories.filter(c => !c.archived && (kind === 'transfer' || c.kind === 'both' || c.kind === kind))
      .filter(c => !s || c.name.toLowerCase().includes(s))
      .sort((a, b) => (b.tx_count - a.tx_count) || a.name.localeCompare(b.name))
  }, [categories, kind, q])
  return (
    <div>
      {categories.length > 12 && <input className="mb-sm" placeholder="Filter categories…" value={q} onChange={e => setQ(e.target.value)} />}
      <div className="cat-grid">
        {list.map(c => (
          <button type="button" key={c.id} className={`cat-btn ${value === c.id ? 'on' : ''} ${c.parent_id ? 'child' : ''}`} onClick={() => onChange(value === c.id ? null : c.id)}>
            <span className="e">{c.icon || '•'}</span><span className="n">{c.name}</span>
          </button>
        ))}
        {list.length === 0 && <span className="muted small">No categories yet — add them under Settings → Categories.</span>}
      </div>
    </div>
  )
}

/** Tag chips with autocomplete from existing tags; Enter or comma adds. */
export function TagInput({ value, onChange, placeholder = 'Add tag…' }: { value: string[]; onChange: (tags: string[]) => void; placeholder?: string }) {
  const { tags } = useApp()
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const add = (t: string) => {
    t = t.trim().replace(/^#/, '')
    if (!t || value.some(v => v.toLowerCase() === t.toLowerCase())) { setText(''); return }
    onChange([...value, t]); setText('')
  }
  const sugg = tags.filter(t => !t.archived && !value.some(v => v.toLowerCase() === t.name.toLowerCase()) && (!text || t.name.toLowerCase().includes(text.toLowerCase())))
    .sort((a, b) => (b.last_used || '').localeCompare(a.last_used || '')).slice(0, 8)
  return (
    <div className="tag-input" onBlur={() => setTimeout(() => setOpen(false), 150)}>
      {value.map(t => <span key={t} className="tg">#{t}<button type="button" onClick={() => onChange(value.filter(x => x !== t))} aria-label={`Remove ${t}`}>×</button></span>)}
      <input value={text} placeholder={value.length ? '' : placeholder} onFocus={() => setOpen(true)} onChange={e => { setText(e.target.value); setOpen(true) }}
        onKeyDown={e => {
          if ((e.key === 'Enter' || e.key === ',') && text.trim()) { e.preventDefault(); add(text) }
          if (e.key === 'Backspace' && !text && value.length) onChange(value.slice(0, -1))
        }} />
      {open && sugg.length > 0 && (
        <div className="suggest">{sugg.map(t => <div key={t.id} className="r" onMouseDown={e => { e.preventDefault(); add(t.name) }}><span>#{t.name}</span><span className="muted small">{t.tx_count}</span></div>)}</div>
      )}
    </div>
  )
}

export function KindTabs({ value, onChange, allowTransfer = true }: { value: Kind; onChange: (k: Kind) => void; allowTransfer?: boolean }) {
  const kinds: { k: Kind; l: string }[] = [{ k: 'expense', l: '− Expense' }, { k: 'income', l: '+ Income' }, ...(allowTransfer ? [{ k: 'transfer' as Kind, l: '⇄ Transfer' }] : [])]
  return <div className="kind-tabs" style={{ gridTemplateColumns: `repeat(${kinds.length}, 1fr)` }}>{kinds.map(x => <button type="button" key={x.k} className={`${x.k} ${value === x.k ? 'on' : ''}`} onClick={() => onChange(x.k)}>{x.l}</button>)}</div>
}

/** A transaction's amount: red-free for expenses, green for income, grey for transfers; foreign amount underneath. */
export function TxAmount({ t, home = false, perspective }: { t: Transaction; home?: boolean; perspective?: number }) {
  const { currencies, home: hc } = useApp()
  let sign = t.kind === 'expense' ? -1 : t.kind === 'income' ? 1 : 0
  let amt = home ? t.amount_home : t.amount
  let cur = home ? hc : t.currency
  // on an account's own page a transfer is money out of (or into) that account
  if (t.kind === 'transfer' && perspective) {
    if (perspective === t.account_id) sign = -1
    else if (perspective === t.to_account_id) { sign = 1; if (!home && t.to_currency) { amt = t.to_amount; cur = t.to_currency } }
  }
  const s = fmtMoney(sign === 0 ? amt : sign * amt, cur, decimalsOf(cur, currencies), { sign: sign > 0 })
  return (
    <span className={`amt ${t.kind}`}>
      {s}
      {t.foreign_amount > 0 && <span className="fx">{fmtMoney(t.foreign_amount, t.foreign_currency, decimalsOf(t.foreign_currency, currencies))}</span>}
      {t.kind === 'transfer' && t.to_currency && t.to_currency !== t.currency && <span className="fx">→ {fmtMoney(t.to_amount, t.to_currency, decimalsOf(t.to_currency, currencies))}</span>}
    </span>
  )
}

export function CatIcon({ t, size = '' }: { t: Pick<Transaction, 'kind' | 'category_icon'>; size?: '' | 'sm' | 'lg' }) {
  const ico = t.kind === 'transfer' ? '⇄' : t.category_icon || (t.kind === 'income' ? '💰' : '•')
  return <span className={`cat-ico ${size} ${t.kind}`}>{ico}</span>
}
