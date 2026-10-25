import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { addDays, decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import type { ItemStats, Merchant, Purchase } from '../lib/types'
import { Card, Empty, Field, Loading, Modal, PageHeader, clickRow, errMsg, useAsync, useDebounce, useToast } from '../components/ui'
import { CategorySelect, IconInput } from '../components/pickers'
import { Select } from '../components/Select'
import { Line } from '../components/Charts'
import { fmtQty, perUnit, qtyLabel } from '../components/ReceiptCard'
import { Icon } from '../components/icons'

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
      {open && <GroupDetail id={open} from={from} merchant={merchant} groups={real} onClose={() => set('group', '')} onChanged={reload} onEdit={g => setEditing(g)} />}
      {editing && <GroupForm g={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={msg => { setEditing(null); toast(msg); reload() }} onDeleted={() => { setEditing(null); set('group', ''); reload() }} />}
    </>
  )
}

function GroupDetail({ id, from, merchant, groups, onClose, onChanged, onEdit }: { id: string; from: string; merchant: string; groups: ItemStats[]; onClose: () => void; onChanged: () => void; onEdit: (g: ItemStats) => void }) {
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
            <thead><tr><th>Product</th><th>Bought</th><th style={{ textAlign: 'right' }}>Spent</th><th style={{ textAlign: 'right' }}>Last price</th><th>Group</th></tr></thead>
            <tbody>
              {products.map(p => (
                <tr key={p.key}>
                  <td><Link to={`/transactions?product=${p.key}`}>{p.name}</Link></td>
                  <td className="muted nowrap">{p.purchases}× · {qtyLabel(p.quantity, p.unit)}</td>
                  <td style={{ textAlign: 'right' }}>{fmtMoney(p.spent, home, dec)}</td>
                  <td style={{ textAlign: 'right' }} className="muted">{p.last_currency ? fmtMoney(p.last_price, p.last_currency, decimalsOf(p.last_currency, currencies)) : ''}</td>
                  <td style={{ width: 190 }}>
                    {p.key > 0 && <Select value={p.group_id ? String(p.group_id) : ''} onChange={v => move(p, v)} ariaLabel="Group"
                      options={[...(p.locked ? [{ value: 'auto', label: '↺ By keywords' }] : []), ...groups.map(x => ({ value: String(x.key), label: `${x.icon || '📦'} ${x.name}` }))]}
                      placeholder="No group" clearLabel="No group" />}
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
                  <td>{p.name}<div className="muted small">{p.merchant}</div></td>
                  <td className="muted nowrap">{fmtQty(p.quantity, p.unit)} × {fmtMoney(p.unit_price, p.currency, decimalsOf(p.currency, currencies), { code: false })}</td>
                  <td style={{ textAlign: 'right' }}>{fmtMoney(p.total, p.currency, decimalsOf(p.currency, currencies))}</td>
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
