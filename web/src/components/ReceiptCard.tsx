import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney, osmLink } from '../lib/format'
import type { Receipt, ReceiptItem, Transaction } from '../lib/types'
import { type Anchor, Popover, errMsg, rectOf, useToast } from './ui'
import { CategorySelect } from './pickers'
import { Icon } from './icons'

const METHOD: Record<string, string> = { cash: '💵 Cash', card: '💳 Card', transfer: '🏦 Transfer', voucher: '🎟 Voucher', check: '🧾 Cheque', other: 'Other' }

const PIECE = ['', 'ko', 'kom', 'kos', 'kd', 'pcs', 'pc']

/** "2.884 kg", "3 l" or "2 pcs": amounts bought with their unit (pieces when the receipt says KO/KOM). */
export function qtyLabel(q: number, unit: string): string {
  const r = Math.round(q * 1000) / 1000
  return PIECE.includes(unit) ? `${r.toLocaleString(undefined, { maximumFractionDigits: 3 })} pcs` : fmtQty(r, unit)
}

/** Per unit: "kg", "l", or "piece". */
export function perUnit(unit: string): string { return PIECE.includes(unit) ? 'piece' : unit }

export function fmtQty(q: number, unit: string): string {
  const n = Number.isInteger(q) ? String(q) : q.toLocaleString(undefined, { maximumFractionDigits: 3 })
  const u = unit && !['ko', 'kom', 'kos', 'kd'].includes(unit) ? ` ${unit}` : ''
  return n + u
}

const SOURCE_HINT: Record<string, string> = { you: 'your choice', ai: 'sorted by AI', group: 'from its group' }

/** A scanned fiscal receipt: shop, every item (with its product group and
 *  category, changeable in place), why the transaction got its category,
 *  payment and tax, and the printed original. */
export function ReceiptCard({ receipt, tx, onChanged }: { receipt: Receipt; tx?: Transaction; onChanged?: (t: Transaction) => void }) {
  const { currencies, categories } = useApp()
  const { toast } = useToast()
  const [r, setR] = useState(receipt)
  useEffect(() => setR(receipt), [receipt])
  const [journal, setJournal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [picking, setPicking] = useState<{ item: ReceiptItem; at: Anchor } | null>(null)
  const dec = decimalsOf(r.currency, currencies)
  const m = r.merchant
  const reload = () => api.get<Receipt>(`${V1}/receipts/${r.id}`).then(setR).catch(() => {})
  const split = async (undo: boolean) => {
    setBusy(true)
    try {
      const t = await api.post<Transaction>(`${V1}/receipts/${r.id}/split${undo ? '?undo=1' : ''}`)
      toast(undo ? 'Joined back into one' : `Split into ${t.splits?.length || 0} categories`)
      onChanged?.(t)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const setItemCategory = async (it: ReceiptItem, cat: number | null) => {
    setPicking(null)
    try {
      await api.put(`${V1}/products/${it.product_id}`, { category_id: cat || 0 })
      toast(cat ? `${it.product_name || it.name}: remembered for next time` : 'Category removed')
      reload()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const applyGuess = async () => {
    setBusy(true)
    try {
      const out = await api.post<{ transaction: Transaction; reason: string }>(`${V1}/receipts/${r.id}/apply`)
      toast('Updated from the receipt')
      onChanged?.(out.transaction)
      reload()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const g = r.guess
  const guessCat = g?.category_id ? categories.find(c => c.id === g.category_id) : undefined
  const differs = !!tx && !!g && !tx.splits?.length && ((g.category_id != null && g.category_id !== tx.category_id) || (tx.source === 'receipt' && g.description && g.description !== tx.description.replace(/^Refund · /, '')))
  return (
    <div className="receipt-card">
      <div className="rc-head">
        <span className="rc-icon">🧾</span>
        <div className="rc-shop">
          <b>{m?.display_name || 'Unknown shop'}</b>
          <div className="muted small">{[r.location, m?.address, m?.city].filter(Boolean).join(' · ')}{m?.lat != null && m?.lng != null && <> · <a href={osmLink(m.lat, m.lng)} target="_blank" rel="noreferrer">📍 map</a></>}</div>
        </div>
        <div className="rc-total">
          <b>{fmtMoney(r.total, r.currency, dec)}</b>
          <div className="muted small">{r.date} {r.time}</div>
        </div>
      </div>
      {!r.complete && <div className="rc-note">⏳ Booked from the QR code alone: the shop and items are filled in once the tax office answers.</div>}
      {r.kind === 'refund' && <div className="rc-note">↩ Refund{r.referent ? ` of receipt ${r.referent}` : ''}</div>}
      {tx && tx.category_name && r.category_reason && !differs && <div className="rc-why small">{tx.category_icon || '🏷'} <b>{tx.category_name}</b> <span className="muted">· {r.category_reason}</span></div>}
      {differs && g && (
        <div className="rc-why rc-suggest small">
          <span>💡 The receipt now says <b>{guessCat ? `${guessCat.icon || ''} ${guessCat.name}` : tx?.category_name || 'no category'}</b>{tx?.source === 'receipt' && g.description ? <> · “{g.description}”</> : null}{g.reason && <span className="muted"> · {g.reason}</span>}</span>
          <button type="button" className="btn primary xs" disabled={busy} onClick={applyGuess}>Use it</button>
        </div>
      )}
      {r.items.length > 0 && (
        <table className="rc-items">
          <tbody>
            {r.items.map(it => (
              <tr key={it.id}>
                <td className="rc-name">
                  {it.product_id ? <Link to={`/products?product=${it.product_id}`} className="rc-item-link" title="Statistics for this product">{it.product_name || it.name}</Link> : <span>{it.product_name || it.name}</span>}
                  {it.group_id
                    ? <Link to={`/products?group=${it.group_id}`} className="badge rc-group">{it.group_icon} {it.group_name}</Link>
                    : it.product_id ? <Link to={`/products?group=none&q=${encodeURIComponent(it.product_name || it.name)}`} className="badge rc-group muted">+ group</Link> : null}
                  {it.product_id && (
                    <button type="button" className={`badge rc-cat ${it.category_id ? '' : 'none'}`} title={it.category_id ? `${it.category_name} · ${SOURCE_HINT[it.category_source] || ''} · change` : 'Give this product a category'}
                      onClick={e => setPicking({ item: it, at: rectOf(e.currentTarget) })}>
                      {it.category_id ? <>{it.category_icon} {it.category_name}{it.category_source === 'ai' && <span className="rc-ai">AI</span>}</> : '+ category'}
                    </button>
                  )}
                </td>
                <td className="muted rc-qty">{it.quantity !== 1 || it.unit === 'kg' ? `${fmtQty(it.quantity, it.unit)} × ${fmtMoney(it.unit_price, r.currency, dec, { code: false })}` : ''}</td>
                <td className="rc-amt">{fmtMoney(it.total, r.currency, dec, { code: false })}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="rc-foot muted small">
        {r.payments.map((p, i) => <span key={i}>{METHOD[p.method] || p.method} {fmtMoney(p.amount, r.currency, dec, { code: false })}</span>)}
        {r.tax_total > 0 && <span>VAT {fmtMoney(r.tax_total, r.currency, dec, { code: false })}</span>}
        <span title="Receipt number">№ {r.invoice_number}</span>
      </div>
      <div className="row wrap" style={{ gap: 6 }}>
        {r.url && <a className="btn ghost sm" href={r.url} target="_blank" rel="noreferrer"><Icon name="external" size={14} />Tax office</a>}
        {r.journal && <button type="button" className="btn ghost sm" onClick={() => setJournal(j => !j)}><Icon name="receipt" size={14} />{journal ? 'Hide' : 'Printed'} receipt</button>}
        {tx && r.items.length > 1 && (tx.splits?.length
          ? <button type="button" className="btn ghost sm" disabled={busy} onClick={() => split(true)}>Join parts</button>
          : <button type="button" className="btn ghost sm" disabled={busy} onClick={() => split(false)} title="One part per category of the items">✂ Split by item category</button>)}
      </div>
      {journal && <pre className="rc-journal">{r.journal}</pre>}
      {picking && (
        <Popover anchor={picking.at} onClose={() => setPicking(null)} width={300} prefer="below" label="Item category">
          <div style={{ padding: 10, display: 'grid', gap: 8 }}>
            <b className="small">{picking.item.product_name || picking.item.name}</b>
            <CategorySelect value={picking.item.category_source === 'group' ? null : picking.item.category_id} onChange={id => setItemCategory(picking.item, id)} kind="expense" placeholder="Category…" clearLabel={picking.item.group_id ? 'Use the group’s' : 'No category'} />
            <span className="muted small">Remembered for this product on every receipt from now on.</span>
          </div>
        </Popover>
      )}
    </div>
  )
}
