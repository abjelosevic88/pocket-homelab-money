import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { addDays, decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import type { CheckSuggestion, ItemStats, Merchant, ProductCheckRun, Purchase } from '../lib/types'
import { Card, Empty, Field, Loading, Modal, PageHeader, Spinner, clickRow, errMsg, useAsync, useDebounce, useToast } from '../components/ui'
import { CategorySelect, IconInput } from '../components/pickers'
import { Select } from '../components/Select'
import { Line } from '../components/Charts'
import { fmtQty, perUnit, qtyLabel } from '../components/ReceiptCard'
import { Icon } from '../components/icons'
import { HowPill, SourceMark } from '../components/SourceMark'

const RANGES = [
  { id: '', label: 'All time' },
  { id: 'month', label: 'This month' },
  { id: '90', label: '3 months' },
  { id: 'year', label: 'This year' },
]
function rangeFrom(id: string): string {
  const t = todayISO()
  if (id === 'month') return t.slice(0, 8) + '01'
  if (id === '90') return addDays(t, -89)
  if (id === 'year') return t.slice(0, 4) + '-01-01'
  return ''
}

/** What you buy, from the items of scanned receipts, in groups ("Milk"):
 *  how often, how much, what it cost and where it is cheapest. */
export default function Products() {
  const { home, currencies } = useApp()
  const { toast } = useToast()
  const [sp, setSp] = useSearchParams()
  const range = sp.get('range') || ''
  const merchant = sp.get('merchant') || ''
  const from = rangeFrom(range)
  const qs = `from=${from}${merchant ? `&merchant=${merchant}` : ''}`
  const { data: groups, reload } = useAsync(() => api.get<ItemStats[]>(`${V1}/products/groups?${qs}`), [qs])
  const { data: merchants } = useAsync(() => api.get<Merchant[]>(`${V1}/merchants`), [])
  const [editing, setEditing] = useState<ItemStats | 'new' | null>(null)
  const open = sp.get('group')
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  const dec = decimalsOf(home, currencies)
  const starter = async () => {
    const r = await api.post<{ added: number; moved: number }>(`${V1}/products/groups/starter`)
    toast(`${r.added} groups added · ${r.moved} products sorted into them`); reload()
  }
  const real = (groups || []).filter(g => g.key > 0)
  return (
    <>
      <PageHeader title="Products" sub="Everything on your scanned receipts, grouped: how much milk, coffee or fuel you buy, and what it costs."
        actions={<>
          {groups && real.length === 0 && <button type="button" className="btn" onClick={starter}><Icon name="sparkles" size={15} />Add suggested groups</button>}
          <button type="button" className="btn" onClick={() => set('report', '1')}><Icon name="history" size={15} />Check</button>
          <button type="button" className="btn primary" onClick={() => setEditing('new')}><Icon name="plus" size={16} />Group</button>
        </>} />
      <div className="filters">
        {RANGES.map(r => <button key={r.id} type="button" className={`chip ${range === r.id ? 'active' : ''}`} onClick={() => set('range', r.id)}>{r.label}</button>)}
        <div style={{ width: 200 }}><Select value={merchant} onChange={v => set('merchant', v)} options={(merchants || []).map(m => ({ value: String(m.id), label: m.display_name }))} placeholder="All shops" clearLabel="All shops" /></div>
        {groups && real.length > 0 && <button type="button" className="btn ghost sm" onClick={starter} title="Adds the suggested groups you do not have yet">+ suggested groups</button>}
      </div>
      {!groups ? <Loading /> : groups.length === 0 ? (
        <Empty title="No products yet">Scan receipts and their items show up here. Groups such as “Milk” collect every product whose name has one of its keywords (mlijeko, mleko…), so you can see how many you bought and what you paid.</Empty>
      ) : (
        <div className="prod-grid">
          {groups.map(g => (
            <div key={g.key} className={`prod-card ${g.purchases === 0 ? 'unused' : ''}`} {...clickRow(() => set('group', g.key ? String(g.key) : 'none'))}>
              <div className="prod-top"><span className="prod-ico">{g.icon || '📦'}</span><b>{g.name}</b>
                {g.key > 0 && <button type="button" className="btn ghost icon-only xs" aria-label="Edit group" onClick={e => { e.stopPropagation(); setEditing(g) }}><Icon name="edit" size={13} /></button>}</div>
              {g.purchases > 0 ? <>
                <div className="prod-big">{fmtMoney(g.spent, home, dec)}</div>
                <div className="muted small">{g.purchases}× bought · {qtyLabel(g.quantity, g.unit)}{g.quantity > 0 && <> · avg {fmtMoney(g.avg_price, home, dec)}/{perUnit(g.unit)}</>}</div>
                <div className="muted small">{g.products} product{g.products === 1 ? '' : 's'} · last {fmtDate(g.last_date, false)}</div>
              </> : <div className="muted small">{g.keywords ? `Nothing bought yet · ${g.keywords}` : 'Nothing bought yet'}</div>}
            </div>
          ))}
        </div>
      )}
      {open && <GroupDetail id={open} from={from} merchant={merchant} groups={real} onClose={() => set('group', '')} onChanged={reload} onEdit={g => setEditing(g)} onProduct={id => set('product', String(id))} />}
      {sp.get('report') && <CheckReport onClose={() => set('report', '')} onChanged={reload} onProduct={id => set('product', String(id))} />}
      {sp.get('product') && <ProductDetail id={Number(sp.get('product'))} from={from} merchant={merchant} groups={real} onClose={() => set('product', '')} onChanged={reload} />}
      {editing && <GroupForm g={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={msg => { setEditing(null); toast(msg); reload() }} onDeleted={() => { setEditing(null); set('group', ''); reload() }} />}
    </>
  )
}

function GroupDetail({ id, from, merchant, groups, onClose, onChanged, onEdit, onProduct }: { id: string; from: string; merchant: string; groups: ItemStats[]; onClose: () => void; onChanged: () => void; onEdit: (g: ItemStats) => void; onProduct: (id: number) => void }) {
  const { home, currencies } = useApp()
  const { toast } = useToast()
  const [sp] = useSearchParams()
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q, 250)
  const g = groups.find(x => String(x.key) === id)
  const qs = `group=${id}&from=${from}${merchant ? `&merchant=${merchant}` : ''}`
  const { data: products, reload } = useAsync(() => api.get<ItemStats[]>(`${V1}/products?${qs}&q=${encodeURIComponent(dq)}`), [qs, dq])
  const { data: purchases } = useAsync(() => api.get<Purchase[]>(`${V1}/products/purchases?${qs}&limit=500`), [qs])
  const dec = decimalsOf(home, currencies)
  const move = async (p: ItemStats, v: string) => {
    try {
      await api.put(`${V1}/products/${p.key}`, v === 'auto' ? { auto: true } : { group_id: Number(v) || 0 })
      toast(v === 'auto' ? 'Back to keyword matching' : 'Moved'); reload(); onChanged()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const setCat = async (p: ItemStats, id: number | null) => {
    try { await api.put(`${V1}/products/${p.key}`, { category_id: id || 0 }); toast('Remembered for every receipt'); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  // price per unit over time, and where it is cheapest
  const points = useMemo(() => (purchases || []).filter(p => p.quantity > 0).map(p => ({ x: p.date, y: p.unit_price, label: `${p.name} · ${p.merchant}` })).reverse(), [purchases])
  const cur = purchases?.[0]?.currency || home
  const shops = useMemo(() => {
    const m = new Map<string, { name: string; n: number; qty: number; total: number; cur: string }>()
    for (const p of purchases || []) {
      const k = `${p.merchant}|${p.currency}`
      const x = m.get(k) || { name: p.merchant || 'Unknown shop', n: 0, qty: 0, total: 0, cur: p.currency }
      x.n++; x.qty += p.quantity; x.total += p.total
      m.set(k, x)
    }
    return [...m.values()].sort((a, b) => a.total / (a.qty || 1) - b.total / (b.qty || 1))
  }, [purchases])
  const title = id === 'none' ? 'Products without a group' : g ? `${g.icon || '📦'} ${g.name}` : 'Group'
  const txLink = id === 'none' ? '' : `/transactions?product_group=${id}${from ? `&from=${from}` : ''}`
  return (
    <Modal title={title} onClose={onClose} size="lg" footer={<>
      {g && <button type="button" className="btn ghost" onClick={() => onEdit(g)}><Icon name="edit" size={14} />Edit group</button>}
      <span className="spacer" />
      {txLink && <Link className="btn primary" to={txLink}>Show the transactions</Link>}
    </>}>
      {g && g.purchases > 0 && (
        <div className="stats">
          <div className="stat"><div className="n">{g.purchases}</div><div className="l">times bought</div></div>
          <div className="stat"><div className="n">{qtyLabel(g.quantity, g.unit)}</div><div className="l">bought in all</div></div>
          <div className="stat"><div className="n">{fmtMoney(g.spent, home, dec)}</div><div className="l">spent</div></div>
          <div className="stat"><div className="n">{fmtMoney(g.avg_price, home, dec)}</div><div className="l">average per {perUnit(g.unit)}</div></div>
        </div>
      )}
      {points.length > 1 && (
        <Card title="Price per unit" sub="Every purchase, oldest to newest">
          <Line points={points} fmt={y => fmtMoney(y, cur, decimalsOf(cur, currencies))} height={150} />
        </Card>
      )}
      {shops.length > 1 && (
        <Card title="By shop" sub="Cheapest per unit first" flush>
          <table className="table compact">
            <tbody>{shops.map(s => <tr key={s.name + s.cur}><td>{s.name}</td><td className="muted">{s.n}×</td><td style={{ textAlign: 'right' }}>{fmtMoney(Math.round(s.total / (s.qty || 1)), s.cur, decimalsOf(s.cur, currencies))} <span className="muted small">avg</span></td></tr>)}</tbody>
          </table>
        </Card>
      )}
      <Card title="Products" sub={id === 'none' ? 'Put each into a group, or create a group whose keywords catch them.' : 'Matched by the keywords, or moved here by hand.'} flush
        actions={<div className="search" style={{ width: 200 }}><input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter…" /></div>}>
        {!products ? <Loading /> : products.length === 0 ? <div className="muted small" style={{ padding: 14 }}>None.</div> : (
          <table className="table compact">
            <thead><tr><th>Product</th><th>Bought</th><th style={{ textAlign: 'right' }}>Spent</th><th>Category</th><th>Group</th></tr></thead>
            <tbody>
              {products.map(p => (
                <tr key={p.key}>
                  <td style={{ minWidth: 140 }}>{p.key > 0 ? <button type="button" className="link-btn" onClick={() => onProduct(p.key)}>{p.name}</button> : p.name}</td>
                  <td className="muted nowrap">{p.purchases}× · {qtyLabel(p.quantity, p.unit)}</td>
                  <td style={{ textAlign: 'right' }}>{fmtMoney(p.spent, home, dec)}</td>
                  <td className="pick-td">
                    {p.key > 0 && <div className="pick-cell"><div className="pick-field"><CategorySelect value={p.category_id ?? null} onChange={id => setCat(p, id)} kind="expense" placeholder={g?.category_id ? 'Group’s' : 'None'} clearLabel={g?.category_id ? 'Group’s' : 'None'} /></div><SourceMark source={p.category_source} what="category" slot /></div>}
                  </td>
                  <td className="pick-td">
                    {p.key > 0 && <div className="pick-cell"><div className="pick-field"><Select value={p.group_id ? String(p.group_id) : ''} onChange={v => move(p, v)} ariaLabel="Group"
                      options={[...(p.locked ? [{ value: 'auto', label: p.group_source === 'you' ? '↺ By keywords' : '↺ Undo, back to keywords' }] : []), ...groups.map(x => ({ value: String(x.key), label: `${x.icon || '📦'} ${x.name}` }))]}
                      placeholder="No group" clearLabel="No group" /></div><SourceMark source={p.group_source} what="group" slot /></div>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
      {purchases && purchases.length > 0 && (
        <Card title="Purchases" flush>
          <table className="table compact">
            <tbody>
              {purchases.slice(0, 100).map((p, i) => (
                <tr key={i}>
                  <td className="nowrap muted">{fmtDate(p.date, false)}</td>
                  <td>{p.name}<div className="muted small">{p.merchant}<span className="show-sm"> · {fmtQty(p.quantity, p.unit)} × {fmtMoney(p.unit_price, p.currency, decimalsOf(p.currency, currencies), { code: false })}</span></div></td>
                  <td className="muted nowrap hide-sm">{fmtQty(p.quantity, p.unit)} × {fmtMoney(p.unit_price, p.currency, decimalsOf(p.currency, currencies), { code: false })}</td>
                  <td style={{ textAlign: 'right' }} className="nowrap">{fmtMoney(p.total, p.currency, decimalsOf(p.currency, currencies))}</td>
                  <td>{p.transaction_id && <Link className="btn ghost xs" to={`/transactions/${p.transaction_id}`} aria-label="Open the transaction"><Icon name="arrow-right" size={13} /></Link>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </Modal>
  )
}

function GroupForm({ g, onClose, onSaved, onDeleted }: { g?: ItemStats; onClose: () => void; onSaved: (msg: string) => void; onDeleted: () => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(g?.name || '')
  const [icon, setIcon] = useState(g?.icon || '')
  const [keywords, setKeywords] = useState(g?.keywords || '')
  const [cat, setCat] = useState<number | null>(g?.category_id ?? null)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const body = { name, icon, keywords, category_id: cat || 0 }
      const r = g ? await api.put<{ moved: number }>(`${V1}/products/groups/${g.key}`, body) : await api.post<{ moved: number }>(`${V1}/products/groups`, body)
      onSaved(r.moved ? `Saved · ${r.moved} products regrouped` : 'Saved')
    } catch (err) { toast(errMsg(err), 'err') }
  }
  const del = async () => {
    if (!g || !confirm(`Delete the group “${g.name}”? Its products stay, without a group.`)) return
    await api.del(`${V1}/products/groups/${g.key}`)
    onDeleted()
  }
  return (
    <Modal title={g ? `Edit ${g.name}` : 'New product group'} onClose={onClose} size="sm">
      <form onSubmit={save} className="form">
        <div className="two-col tight">
          <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder="Milk" autoFocus /></Field>
          <Field label="Icon"><IconInput value={icon} onChange={setIcon} placeholder="🥛" /></Field>
        </div>
        <Field label="Keywords" help="Separated by commas. A product joins when a word in its name starts with one (case, Cyrillic and č/ć/š don’t matter): “mlijeko, mleko”. The longest matching keyword wins.">
          <textarea rows={2} value={keywords} onChange={e => setKeywords(e.target.value)} placeholder="mlijeko, mleko, milk" />
        </Field>
        <Field label="Category" help="Used to split a receipt by item category." group><CategorySelect value={cat} onChange={setCat} placeholder="None" clearLabel="None" /></Field>
        <div className="form-actions">
          {g && <button type="button" className="btn ghost danger-text" onClick={del}>Delete</button>}
          <span className="spacer" /><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!name.trim()}>Save</button>
        </div>
      </form>
    </Modal>
  )
}

const dayMs = 86400000
const daysBetween = (a: string, b: string) => Math.round((Date.parse(b) - Date.parse(a)) / dayMs)

/** One product: how often and how much you buy it, what it costs and where
 *  it is cheapest, month by month, every purchase. */
function ProductDetail({ id, from, merchant, groups, onClose, onChanged }: { id: number; from: string; merchant: string; groups: ItemStats[]; onClose: () => void; onChanged: () => void }) {
  const { home, currencies } = useApp()
  const { toast } = useToast()
  const qs = `product=${id}&from=${from}${merchant ? `&merchant=${merchant}` : ''}`
  const { data: sum, reload } = useAsync(() => api.get<ItemStats>(`${V1}/products/summary?${qs}`), [qs])
  const { data: buys } = useAsync(() => api.get<Purchase[]>(`${V1}/products/purchases?${qs}&limit=2000`), [qs])
  const dec = decimalsOf(home, currencies)
  const st = useMemo(() => {
    const list = (buys || []).filter(b => b.quantity > 0)
    if (!list.length) return null
    const cur = list[0].currency
    const same = list.filter(b => b.currency === cur) // prices compared in one currency
    const asc = [...same].reverse()
    const min = same.reduce((a, b) => (b.unit_price < a.unit_price ? b : a))
    const max = same.reduce((a, b) => (b.unit_price > a.unit_price ? b : a))
    const first = asc[0], last = asc[asc.length - 1]
    const change = first.unit_price ? Math.round(((last.unit_price - first.unit_price) / first.unit_price) * 100) : 0
    const days = [...new Set(list.map(b => b.date))].sort()
    const every = days.length > 1 ? Math.round(daysBetween(days[0], days[days.length - 1]) / (days.length - 1)) : 0
    const since = daysBetween(days[days.length - 1], todayISO())
    const next = every > 0 ? addDays(days[days.length - 1], every) : ''
    const shops = new Map<string, { name: string; n: number; qty: number; total: number; last: number; lastDate: string }>()
    for (const b of same) {
      const x = shops.get(b.merchant) || { name: b.merchant || 'Unknown shop', n: 0, qty: 0, total: 0, last: b.unit_price, lastDate: b.date }
      x.n++; x.qty += b.quantity; x.total += b.total
      shops.set(b.merchant, x)
    }
    const months = new Map<string, { qty: number; spent: number; n: number }>()
    for (const b of list) {
      const m = b.date.slice(0, 7)
      const x = months.get(m) || { qty: 0, spent: 0, n: 0 }
      x.qty += b.quantity; x.spent += b.home; x.n++
      months.set(m, x)
    }
    return {
      cur, min, max, first, last, change, every, since, next,
      shops: [...shops.values()].sort((a, b) => a.total / a.qty - b.total / b.qty),
      months: [...months.entries()].sort((a, b) => b[0].localeCompare(a[0])),
      points: asc.map(b => ({ x: b.date, y: b.unit_price, label: b.merchant })),
      unit: list[0].unit,
    }
  }, [buys])
  const save = async (body: Record<string, unknown>, msg: string) => {
    try { await api.put(`${V1}/products/${id}`, body); toast(msg); reload(); onChanged() } catch (e) { toast(errMsg(e), 'err') }
  }
  const money = (v: number, c = st?.cur || home) => fmtMoney(v, c, decimalsOf(c, currencies))
  const per = st ? perUnit(st.unit) : 'piece'
  const group = groups.find(g => g.key === sum?.group_id)
  return (
    <Modal title={sum ? `${group?.icon || '🛒'} ${sum.name}` : 'Product'} onClose={onClose} size="lg" footer={<>
      <span className="spacer" />
      <Link className="btn primary" to={`/transactions?product=${id}${from ? `&from=${from}` : ''}`}>Show the transactions</Link>
    </>}>
      {!sum || !buys ? <Loading /> : <>
        <div className="pick-row">
          <div><div className="pick-label">Category</div><div className="pick-cell"><div className="pick-field"><CategorySelect value={sum.category_id ?? null} onChange={c => save({ category_id: c || 0 }, 'Category remembered')} kind="expense" placeholder={group?.category_id ? 'Group’s category' : 'No category'} clearLabel={group?.category_id ? 'Group’s category' : 'No category'} /></div><SourceMark source={sum.category_source} what="category" slot /></div></div>
          <div><div className="pick-label">Group</div><div className="pick-cell"><div className="pick-field"><Select value={sum.group_id ? String(sum.group_id) : ''} onChange={v => save(v === 'auto' ? { auto: true } : { group_id: Number(v) || 0 }, 'Moved')} ariaLabel="Group"
            options={[...(sum.locked ? [{ value: 'auto', label: sum.group_source === 'you' ? '↺ By keywords' : '↺ Undo, back to keywords' }] : []), ...groups.map(g => ({ value: String(g.key), label: `${g.icon || '📦'} ${g.name}` }))]} placeholder="No group" clearLabel="No group" /></div><SourceMark source={sum.group_source} what="group" slot /></div></div>
        </div>
        {sum.purchases === 0 || !st ? <Empty title="Not bought in this period">Pick “All time” on the Products page.</Empty> : <>
          <div className="stats">
            <div className="stat"><div className="n">{sum.purchases}×</div><div className="l">bought{st.every > 0 ? ` · about every ${st.every} day${st.every === 1 ? '' : 's'}` : ''}</div></div>
            <div className="stat"><div className="n">{qtyLabel(sum.quantity, sum.unit)}</div><div className="l">in all</div></div>
            <div className="stat"><div className="n">{fmtMoney(sum.spent, home, dec)}</div><div className="l">spent</div></div>
            <div className="stat"><div className="n">{money(st.last.unit_price)}</div><div className="l">now per {per}{st.change !== 0 && st.first !== st.last && <span className={st.change > 0 ? 'bad' : 'ok'}> · {st.change > 0 ? '+' : ''}{st.change} % since {fmtDate(st.first.date, false)}</span>}</div></div>
          </div>
          <div className="prod-facts small">
            {st.min.unit_price !== st.max.unit_price && <>
              <span>Cheapest <b>{money(st.min.unit_price)}</b> at {st.min.merchant || 'a shop'} on {fmtDate(st.min.date, false)}</span>
              <span>Dearest <b>{money(st.max.unit_price)}</b> at {st.max.merchant || 'a shop'} on {fmtDate(st.max.date, false)}</span>
            </>}
            <span>Last bought {st.since === 0 ? 'today' : st.since === 1 ? 'yesterday' : `${st.since} days ago`}{st.next && st.next >= todayISO() ? ` · next probably around ${fmtDate(st.next, false)}` : ''}</span>
          </div>
          {st.points.length > 1 && (
            <Card title={`Price per ${per}`} sub="Every purchase, oldest to newest">
              <Line points={st.points} fmt={y => money(y)} height={150} />
            </Card>
          )}
          {st.shops.length > 0 && (
            <Card title="By shop" sub="Cheapest per unit first" flush>
              <table className="table compact">
                <thead><tr><th>Shop</th><th>Bought</th><th style={{ textAlign: 'right' }}>Average</th><th style={{ textAlign: 'right' }}>Spent</th></tr></thead>
                <tbody>{st.shops.map(x => <tr key={x.name}><td>{x.name}</td><td className="muted">{x.n}× · {qtyLabel(x.qty, st.unit)}</td><td style={{ textAlign: 'right' }}>{money(Math.round(x.total / (x.qty || 1)))}/{per}</td><td style={{ textAlign: 'right' }}>{money(x.total)}</td></tr>)}</tbody>
              </table>
            </Card>
          )}
          {st.months.length > 1 && (
            <Card title="By month" flush>
              <table className="table compact">
                <tbody>{st.months.map(([m, x]) => <tr key={m}><td>{new Date(m + '-15').toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}</td><td className="muted">{x.n}× · {qtyLabel(x.qty, st.unit)}</td><td style={{ textAlign: 'right' }}>{fmtMoney(x.spent, home, dec)}</td></tr>)}</tbody>
              </table>
            </Card>
          )}
          <Card title="Purchases" flush>
            <table className="table compact">
              <tbody>
                {buys.slice(0, 200).map((b, i) => (
                  <tr key={i}>
                    <td className="nowrap muted">{fmtDate(b.date, false)}</td>
                    <td>{b.merchant}<div className="muted small show-sm">{fmtQty(b.quantity, b.unit)} × {fmtMoney(b.unit_price, b.currency, decimalsOf(b.currency, currencies), { code: false })}</div></td>
                    <td className="muted nowrap hide-sm">{fmtQty(b.quantity, b.unit)} × {fmtMoney(b.unit_price, b.currency, decimalsOf(b.currency, currencies), { code: false })}</td>
                    <td style={{ textAlign: 'right' }} className="nowrap">{fmtMoney(b.total, b.currency, decimalsOf(b.currency, currencies))}</td>
                    <td>{b.transaction_id && <Link className="btn ghost xs" to={`/transactions/${b.transaction_id}`} aria-label="Open the transaction"><Icon name="arrow-right" size={13} /></Link>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </>}
      </>}
    </Modal>
  )
}

/** The daily product check's reports: what each run placed and how, the
 *  groups the AI suggests (one click creates them), what is left. */
function CheckReport({ onClose, onChanged, onProduct }: { onClose: () => void; onChanged: () => void; onProduct: (id: number) => void }) {
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<{ runs: ProductCheckRun[]; ungrouped: number }>(`${V1}/products/check`), [])
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  const [done, setDone] = useState<Set<string>>(new Set())
  const runNow = async () => {
    setBusy(true)
    try {
      const r = await api.post<ProductCheckRun>(`${V1}/products/check`)
      toast(`${r.by_keywords + r.by_similar + r.by_ai} grouped · ${r.still_ungrouped} left`)
      setOpen(r.id); reload(); onChanged()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const create = async (sg: CheckSuggestion) => {
    try {
      const r = await api.post<{ placed: number; moved: number }>(`${V1}/products/check/suggestion`, sg)
      toast(`${sg.name} created · ${r.placed + r.moved} products in it`)
      setDone(d => new Set(d).add(sg.name)); onChanged()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const runs = data?.runs || []
  const shown = runs.find(r => r.id === open) || runs[0]
  return (
    <Modal title="Product check" onClose={onClose} size="lg" footer={<>
      <span className="muted small">{data ? `${data.ungrouped} products without a group now` : ''}</span>
      <span className="spacer" />
      <Link className="btn ghost" to="/receipts?tab=setup">Settings</Link>
      <button type="button" className="btn primary" disabled={busy} onClick={runNow}>{busy ? <><Spinner /> Checking…</> : <><Icon name="refresh" size={15} />Run it now</>}</button>
    </>}>
      {!data ? <Loading /> : runs.length === 0 ? <Empty title="No checks yet">The check runs every day at the hour set under Receipts → Settings, or now with Run it now.</Empty> : <>
        <div className="filters">
          {runs.slice(0, 10).map(r => (
            <button key={r.id} type="button" className={`chip ${shown?.id === r.id ? 'active' : ''}`} onClick={() => setOpen(r.id)}>
              {fmtDate(r.started_at.slice(0, 10), false)} {r.started_at.slice(11, 16)}{r.trigger === 'manual' ? ' · by hand' : r.trigger === 'scan' ? ' · on scan' : ''}
            </button>
          ))}
        </div>
        {shown && <>
          <div className="stats">
            <div className="stat"><div className="n">{shown.checked}</div><div className="l">without a group</div></div>
            <div className="stat"><div className="n">{shown.by_keywords + shown.by_similar + shown.by_ai}</div><div className="l">placed · {shown.by_keywords} keywords, {shown.by_similar} similar, {shown.by_ai} AI</div></div>
            <div className="stat"><div className="n">{shown.report.suggestions.length}</div><div className="l">new groups suggested</div></div>
            <div className="stat"><div className="n">{shown.still_ungrouped}</div><div className="l">still without a group</div></div>
          </div>
          {shown.report.ai_note && <div className="rc-note small" style={{ marginBottom: 12 }}>{shown.report.ai_note}</div>}
          {shown.categorized > 0 && <div className="muted small" style={{ marginBottom: 12 }}>The AI also gave {shown.categorized} products a category.</div>}
          {shown.report.suggestions.length > 0 && (
            <Card title="Suggested groups" sub="Create one and its products go in; its keywords catch the next ones.">
              <div style={{ display: 'grid', gap: 10 }}>
                {shown.report.suggestions.map(sg => (
                  <div key={sg.name} className="check-sugg">
                    <div><b>{sg.icon} {sg.name}</b> <span className="muted small">· {sg.keywords.split(',').map(k => k.trim()).filter(Boolean).join(', ')}</span><div className="small">{sg.products.join(', ')}</div></div>
                    {done.has(sg.name) ? <span className="badge ok">created</span> : <button type="button" className="btn sm" onClick={() => create(sg)}><Icon name="plus" size={14} />Create</button>}
                  </div>
                ))}
              </div>
            </Card>
          )}
          {shown.report.moves.length > 0 && (
            <Card title="Placed" flush>
              <table className="table compact">
                <tbody>{shown.report.moves.map(m => (
                  <tr key={m.product_id}>
                    <td><button type="button" className="link-btn" onClick={() => onProduct(m.product_id)}>{m.product}</button></td>
                    <td>{m.icon} {m.group}</td>
                    <td style={{ textAlign: 'right' }}><HowPill how={m.how} /></td>
                  </tr>
                ))}</tbody>
              </table>
            </Card>
          )}
          {shown.report.left.length > 0 && (
            <Card title="Still without a group" sub="Put them in a group by hand on the Products page, or give a group a keyword that catches them.">
              <div className="small">{shown.report.left.join(', ')}{shown.still_ungrouped > shown.report.left.length ? ` and ${shown.still_ungrouped - shown.report.left.length} more` : ''}</div>
              <Link className="btn ghost sm" style={{ marginTop: 8 }} to="/products?group=none" onClick={onClose}>Open them</Link>
            </Card>
          )}
        </>}
      </>}
    </Modal>
  )
}
