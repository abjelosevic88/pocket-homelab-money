import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney } from '../lib/format'
import type { Receipt, Transaction } from '../lib/types'
import { errMsg, useToast } from './ui'
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

/** A scanned fiscal receipt: shop, every item (with its product group),
 *  payment and tax, and the printed original. */
export function ReceiptCard({ receipt, tx, onChanged }: { receipt: Receipt; tx?: Transaction; onChanged?: (t: Transaction) => void }) {
  const { currencies } = useApp()
  const { toast } = useToast()
  const [journal, setJournal] = useState(false)
  const [busy, setBusy] = useState(false)
  const r = receipt
  const dec = decimalsOf(r.currency, currencies)
  const m = r.merchant
  const split = async (undo: boolean) => {
    setBusy(true)
    try {
      const t = await api.post<Transaction>(`${V1}/receipts/${r.id}/split${undo ? '?undo=1' : ''}`)
      toast(undo ? 'Joined back into one' : `Split into ${t.splits?.length || 0} categories`)
      onChanged?.(t)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <div className="receipt-card">
      <div className="rc-head">
        <span className="rc-icon">🧾</span>
        <div className="rc-shop">
          <b>{m?.display_name || 'Unknown shop'}</b>
          <div className="muted small">{[r.location, m?.address, m?.city].filter(Boolean).join(' · ')}</div>
        </div>
        <div className="rc-total">
          <b>{fmtMoney(r.total, r.currency, dec)}</b>
          <div className="muted small">{r.date} {r.time}</div>
        </div>
      </div>
      {!r.complete && <div className="rc-note">⏳ Booked from the QR code alone: the shop and items are filled in once the tax office answers.</div>}
      {r.kind === 'refund' && <div className="rc-note">↩ Refund{r.referent ? ` of receipt ${r.referent}` : ''}</div>}
      {r.items.length > 0 && (
        <table className="rc-items">
          <tbody>
            {r.items.map(it => (
              <tr key={it.id}>
                <td className="rc-name">
                  <span>{it.product_name || it.name}</span>
                  {it.group_id
                    ? <Link to={`/products?group=${it.group_id}`} className="badge rc-group">{it.group_icon} {it.group_name}</Link>
                    : it.product_id ? <Link to={`/products?group=none&q=${encodeURIComponent(it.product_name || it.name)}`} className="badge rc-group muted">+ group</Link> : null}
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
          : <button type="button" className="btn ghost sm" disabled={busy} onClick={() => split(false)} title="One part per category of the items' product groups">✂ Split by item category</button>)}
      </div>
      {journal && <pre className="rc-journal">{r.journal}</pre>}
    </div>
  )
}
