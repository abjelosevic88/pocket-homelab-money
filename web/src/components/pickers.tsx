import { useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney } from '../lib/format'
import { KIND_ICON, type Category, type Kind, type Transaction } from '../lib/types'
import { Select } from './Select'
import { Icon } from './icons'

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
/** `list` replaces the person's own categories (a shared account books in its owner's); no "create" then. */
export function CategorySelect({ value, onChange, kind = 'expense', placeholder = 'Category…', clearLabel = 'No category', list }: { value: number | null; onChange: (id: number | null) => void; kind?: Kind | 'any'; placeholder?: string; clearLabel?: string; list?: Category[] }) {
  const { categories: own, reloadCatalog } = useApp()
  const categories = list || own
  const opts = categories.filter(c => !c.archived || c.id === value).filter(c => kind === 'any' || kind === 'transfer' || c.kind === 'both' || c.kind === kind || c.id === value)
    .map(c => ({ value: String(c.id), label: catLabel(c, categories) }))
  const create = async (name: string) => {
    const c = await api.post<Category>(`${V1}/categories`, { name, kind: kind === 'income' ? 'income' : 'expense' })
    await reloadCatalog()
    return String(c.id)
  }
  return <Select value={value ? String(value) : ''} onChange={v => onChange(v ? Number(v) : null)} options={opts} placeholder={placeholder} onCreate={list ? undefined : create} createLabel="New category" clearLabel={clearLabel} ariaLabel="Category" />
}

/** Big tappable category buttons (fast on a phone), most used first. */
export function CategoryGrid({ value, onChange, kind = 'expense', list: only }: { value: number | null; onChange: (id: number | null) => void; kind?: Kind; list?: Category[] }) {
  const { categories: own } = useApp()
  const categories = only || own
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
      {value.map(t => <span key={t} className="tg">#{t}<button type="button" onClick={() => onChange(value.filter(x => x !== t))} aria-label={`Remove ${t}`}><Icon name="x" size={12} /></button></span>)}
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

/** Emoji with words to find them by (English and Bosnian/Croatian/Serbian, no diacritics). */
const EMOJI_WORDS: [string, string][] = [
  ['🛒', 'groceries shop market supermarket hrana namirnice nabavka'], ['🥐', 'bakery bread pekara kifla hljeb'], ['🍽️', 'restaurant dining food restoran rucak vecera'],
  ['☕', 'coffee cafe kafa kafic'], ['🍺', 'beer pivo bar'], ['🍷', 'wine vino alcohol alkohol'], ['🍕', 'pizza fast food brza hrana'], ['🛵', 'delivery dostava wolt glovo'],
  ['🚗', 'car auto vozilo automobil'], ['⛽', 'fuel gas petrol gorivo benzin dizel pumpa'], ['🅿️', 'parking'], ['🚇', 'transport metro bus tram prevoz'], ['🚕', 'taxi uber cab'],
  ['🛣️', 'toll road putarina autoput'], ['🔧', 'repair service servis majstor alat tool'], ['🛞', 'tyres tires gume'], ['🧼', 'car wash pranje'],
  ['✈️', 'flight plane travel avion let putovanje'], ['🏨', 'hotel smjestaj accommodation airbnb'], ['🧳', 'trip luggage putovanje odmor holiday vacation'], ['🏖️', 'beach more plaza ljeto'],
  ['🏠', 'home house rent kuca stan kirija'], ['🧾', 'bill invoice racun naknada fee'], ['⚡', 'electricity power struja elektro'], ['💧', 'water voda vodovod'],
  ['🔥', 'heating gas grijanje toplana plin'], ['🌐', 'internet wifi net'], ['📱', 'phone mobile telefon mobitel app aplikacija'], ['📺', 'tv television kablovska streaming'],
  ['🎬', 'netflix movies cinema film bioskop kino'], ['🎧', 'music spotify muzika'], ['▶️', 'youtube video'], ['☁️', 'cloud icloud storage google drive dropbox oblak'],
  ['💻', 'computer laptop software tech kompjuter racunar'], ['🖥️', 'server hosting domain domena vps'], ['🤖', 'ai openai chatgpt claude'], ['🎮', 'games gaming igre playstation steam'],
  ['🏦', 'bank banka racun account fee naknada odrzavanje'], ['💳', 'card kartica credit kredit debit'], ['💶', 'euro eur devizni cash gotovina'], ['💵', 'cash dollar money novac pare'],
  ['💰', 'salary plata income prihod wage'], ['💼', 'work business posao firma sp'], ['🏢', 'company office tax porez knjigovodja'], ['📈', 'investment invest stocks dionice etf'],
  ['🐷', 'savings stednja ustedjevina'], ['🪙', 'crypto bitcoin kripto coin'], ['🤝', 'loan debt dug pozajmica'], ['🧮', 'accounting knjigovodstvo'],
  ['💊', 'pharmacy medicine apoteka lijek ljekovi'], ['🩺', 'doctor health ljekar doktor zdravlje'], ['🦷', 'dentist zubar'], ['🧘', 'therapy terapija yoga psiholog'],
  ['🏋️', 'gym fitness sport teretana'], ['⚽', 'football fudbal sport'], ['🚴', 'bike bicikl cycling'], ['🏊', 'swimming bazen plivanje'],
  ['👕', 'clothes clothing odjeca fashion'], ['👟', 'shoes obuca patike'], ['💄', 'beauty cosmetics kozmetika'], ['💇', 'hair haircut frizer sisanje'],
  ['🧒', 'kids children djeca dijete'], ['🎓', 'school education skola fakultet kurs'], ['📚', 'books knjige reading'], ['✏️', 'office supplies pribor'],
  ['🐾', 'pets ljubimci vet veterinar'], ['🐶', 'dog pas'], ['🐱', 'cat macka'], ['🌾', 'garden basta selo vikendica'], ['🪴', 'plants biljke cvijece'],
  ['🎁', 'gift poklon donation donacija'], ['❤️', 'charity love'], ['🎉', 'party zabava proslava rodjendan birthday'], ['💍', 'wedding vjencanje svadba'],
  ['🛡️', 'insurance osiguranje'], ['🏛️', 'government drzava takse administration'], ['⚖️', 'legal lawyer advokat notar'], ['📦', 'other ostalo misc razno package paket'],
  ['🔁', 'transfer prenos'], ['🛠️', 'maintenance odrzavanje'], ['🧹', 'cleaning ciscenje'], ['🛋️', 'furniture namjestaj'], ['🔌', 'electronics uredjaji device'],
  ['🎸', 'guitar gitara instrument'], ['📰', 'news novine magazine subscription pretplata'], ['🗓️', 'subscription pretplata monthly mjesecno'], ['🚬', 'cigarettes cigare duhan'],
]

function foldText(s: string) { return s.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/đ/g, 'dj') }
function hasEmoji(s: string) { return /\p{Extended_Pictographic}|\p{Regional_Indicator}/u.test(s) }

/** Icon field with autocomplete: type a word ("bank", "struja") or a category
 *  name and pick from the icons your categories use and a built-in list.
 *  An emoji typed or pasted is kept as it is; other text is only a search. */
export function IconInput({ value, onChange, placeholder, fallback }: { value: string; onChange: (icon: string) => void; placeholder?: string; fallback?: string }) {
  const { categories } = useApp()
  const [q, setQ] = useState<string | null>(null) // null = not editing
  const [hl, setHl] = useState(0)
  const options = useMemo(() => {
    const byIcon = new Map<string, { emoji: string; label: string; words: string }>()
    if (fallback) byIcon.set(fallback, { emoji: fallback, label: 'from the category', words: '' })
    for (const c of categories) {
      if (!c.icon || c.archived) continue
      const o = byIcon.get(c.icon)
      if (o) { if (!o.label.includes(c.name)) { o.label = o.label === 'from the category' ? `${o.label} · ${c.name}` : `${o.label}, ${c.name}`; o.words += ' ' + foldText(c.name) } }
      else byIcon.set(c.icon, { emoji: c.icon, label: c.name, words: foldText(c.name) })
    }
    const all = [...byIcon.values()]
    for (const [emoji, words] of EMOJI_WORDS) {
      const o = byIcon.get(emoji)
      if (o) o.words += ' ' + words // a category's icon is found by its built-in words too
      else all.push({ emoji, label: words.split(' ').slice(0, 3).join(', '), words })
    }
    return all
  }, [categories, fallback])
  const sugg = useMemo(() => {
    const s = foldText((q || '').trim())
    if (!s || hasEmoji(s)) return options.slice(0, 40)
    return options.filter(o => o.words.split(' ').some(w => w.startsWith(s)) || o.words.includes(s)).slice(0, 40)
  }, [q, options])
  const pick = (emoji: string) => { onChange(emoji); setQ(null) }
  return (
    <div className="icon-input" onBlur={() => setTimeout(() => setQ(null), 150)}>
      <input value={q ?? value} placeholder={placeholder} aria-label="Icon" autoComplete="off"
        onFocus={() => { setQ(''); setHl(0) }}
        onChange={e => { const v = e.target.value; setQ(v); setHl(0); if (!v || hasEmoji(v)) onChange(v.trim()) }}
        onKeyDown={e => {
          if (q === null) return
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); setHl(h => Math.max(0, Math.min(sugg.length - 1, h + (e.key === 'ArrowDown' ? 1 : -1)))) }
          if (e.key === 'Enter' && sugg[hl] && q && !hasEmoji(q)) { e.preventDefault(); pick(sugg[hl].emoji) }
          if (e.key === 'Escape' && q !== '') { e.stopPropagation(); setQ('') }
        }} />
      {q !== null && sugg.length > 0 && (
        <div className="suggest icon-suggest" role="listbox">
          {value && !q && <div className="r" onMouseDown={e => { e.preventDefault(); pick('') }}><span className="muted">✕ No own icon</span></div>}
          {sugg.map((o, i) => (
            <div key={o.emoji} role="option" aria-selected={i === hl} className={`r ${i === hl ? 'hl' : ''}`} onMouseEnter={() => setHl(i)} onMouseDown={e => { e.preventDefault(); pick(o.emoji) }}>
              <span><span className="em">{o.emoji}</span> {o.label}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
