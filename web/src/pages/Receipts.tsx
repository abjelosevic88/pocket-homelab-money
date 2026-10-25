import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, decimalsOf, fmtDate, fmtMoney } from '../lib/format'
import type { Merchant, Receipt, ReceiptProvider, ReceiptScan, ReceiptSettings, ScanStatus } from '../lib/types'
import { Card, CopyField, Empty, Field, Loading, Modal, PageHeader, SettingRow, Switch, Tabs, clickRow, errMsg, useAsync, useDebounce, useToast } from '../components/ui'
import { AccountSelect, CategorySelect } from '../components/pickers'
import { ReceiptCard } from '../components/ReceiptCard'
import { ReceiptScanner } from '../components/ReceiptScanner'
import { Icon } from '../components/icons'

type Tab = 'receipts' | 'log' | 'shops' | 'setup'

export default function Receipts() {
  const [sp, setSp] = useSearchParams()
  const tab = (sp.get('tab') as Tab) || 'receipts'
  const [scanning, setScanning] = useState(false)
  const [rev, setRev] = useState(0)
  return (
    <>
      <PageHeader title="Receipts" sub="Fiscal receipts read from their QR code: the shop, every item, booked as one transaction."
        actions={<button type="button" className="btn primary" onClick={() => setScanning(true)}><Icon name="camera" size={16} />Scan a receipt</button>} />
      <Tabs<Tab> value={tab} onChange={t => setSp(t === 'receipts' ? {} : { tab: t }, { replace: true })}
        tabs={[{ id: 'receipts', label: 'Receipts', icon: 'receipt' }, { id: 'log', label: 'Scan log', icon: 'history' }, { id: 'shops', label: 'Shops', icon: 'bank' }, { id: 'setup', label: 'Settings & iPhone', icon: 'settings' }]} />
      <div style={{ marginTop: 14 }} key={rev}>
        {tab === 'receipts' && <ReceiptList />}
        {tab === 'log' && <ScanLog />}
        {tab === 'shops' && <Shops />}
        {tab === 'setup' && <Setup />}
      </div>
      {scanning && <ReceiptScanner onClose={() => { setScanning(false); setRev(r => r + 1) }} />}
    </>
  )
}

function ReceiptList() {
  const { currencies } = useApp()
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 250)
  const [open, setOpen] = useState<number | null>(null)
  const { data, reload } = useAsync(() => api.get<{ items: Receipt[]; total: number }>(`${V1}/receipts?limit=200&q=${encodeURIComponent(dq)}`), [dq])
  return (
    <>
      <div className="toolbar">
        <div className="search"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search shop, item (mlijeko, kafa…) or receipt number" /></div>
      </div>
      {!data ? <Loading /> : data.items.length === 0 ? <Empty title={q ? 'No receipt matches' : 'No receipts yet'}>{q ? 'Try another word.' : 'Scan the QR code at the bottom of a fiscal receipt: the shop and every item come with it.'}</Empty> : (
        <div className="list-card">
          {data.items.map(r => {
            const dec = decimalsOf(r.currency, currencies)
            return (
              <div key={r.id} className="tx-row" {...clickRow(() => setOpen(r.id))}>
                <span className="pick"><span className="cat-ico">🧾</span></span>
                <span className="tx-main">
                  <span className="t">{r.merchant?.display_name || 'Unknown shop'}{!r.complete && <span title="Shop and items still to come">⏳</span>}{r.kind === 'refund' && <span className="badge warn">refund</span>}</span>
                  <span className="s">{[fmtDate(r.date), r.time, r.item_count ? `${r.item_count} items` : '', r.method, r.transaction_id ? '' : 'not booked'].filter(Boolean).join(' · ')}</span>
                </span>
                <b className="amt">{r.kind === 'refund' ? '+' : '−'}{fmtMoney(r.total, r.currency, dec)}</b>
              </div>
            )
          })}
          {data.total > data.items.length && <div className="muted small" style={{ padding: 10 }}>Showing {data.items.length} of {data.total}. Search to narrow down.</div>}
        </div>
      )}
      {open && <ReceiptModal id={open} onClose={() => setOpen(null)} onChanged={reload} />}
    </>
  )
}

function ReceiptModal({ id, onClose, onChanged }: { id: number; onClose: () => void; onChanged: () => void }) {
  const { toast } = useToast()
  const navigate = useNavigate()
  const { data: r, reload } = useAsync(() => api.get<Receipt>(`${V1}/receipts/${id}`), [id])
  if (!r) return null
  const del = async (withTx: boolean) => {
    if (!confirm(withTx ? 'Delete the receipt and the transaction booked from it?' : 'Delete the receipt? The transaction stays.')) return
    await api.del(`${V1}/receipts/${r.id}${withTx ? '?transaction=1' : ''}`)
    toast('Receipt deleted'); onChanged(); onClose()
  }
  const book = async () => {
    try {
      const out = await api.post<{ message: string }>(`${V1}/receipts/${r.id}/book`, {})
      toast(out.message); reload(); onChanged()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <Modal title="Receipt" onClose={onClose} footer={<>
      <button type="button" className="btn ghost danger-text" onClick={() => del(false)}>Delete receipt</button>
      {r.transaction_id && <button type="button" className="btn ghost danger-text" onClick={() => del(true)}>…and its transaction</button>}
      <span className="spacer" />
      {r.transaction_id ? <button type="button" className="btn primary" onClick={() => navigate(`/transactions/${r.transaction_id}`)}>Open the transaction</button>
        : <button type="button" className="btn primary" onClick={book}>Book it</button>}
    </>}>
      <ReceiptCard receipt={r} />
    </Modal>
  )
}

const SCAN_BADGE: Record<ScanStatus, [string, string]> = {
  done: ['ok', 'booked'], partial: ['warn', 'waiting'], pending: ['warn', 'pending'], duplicate: ['', 'duplicate'], invalid: ['danger', 'invalid'],
  skipped: ['', 'skipped'], unsupported: ['danger', 'not a receipt'], failed: ['danger', 'failed'],
}

function ScanLog() {
  const { currencies } = useApp()
  const { toast } = useToast()
  const [status, setStatus] = useState('')
  const { data, reload } = useAsync(() => api.get<{ items: ReceiptScan[]; total: number }>(`${V1}/receipts/scans?limit=300&status=${status}`), [status])
  const retry = async (s: ReceiptScan) => {
    try {
      const r = await api.post<{ status: string; message: string }>(`${V1}/receipts/scans/${s.id}/retry`)
      toast(r.message || r.status); reload()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <>
      <div className="filters">
        {[['', 'All'], ['done', 'Booked'], ['partial', 'Waiting'], ['duplicate', 'Duplicates'], ['failed', 'Failed'], ['unsupported', 'Not receipts']].map(([v, l]) => (
          <button key={v} type="button" className={`chip ${status === v ? 'active' : ''}`} onClick={() => setStatus(v)}>{l}</button>
        ))}
        {data && <span className="muted small">{data.total.toLocaleString()} scans</span>}
      </div>
      {!data ? <Loading /> : data.items.length === 0 ? <Empty title="Nothing scanned yet">Every scan lands here first — with the tax office's raw answer — whatever became of it.</Empty> : (
        <Card flush>
          <table className="table compact">
            <thead><tr><th>When</th><th>Status</th><th>Receipt</th><th>Via</th><th>Details</th><th /></tr></thead>
            <tbody>
              {data.items.map(s => {
                const [cls, label] = SCAN_BADGE[s.status] || ['', s.status]
                return (
                  <tr key={s.id}>
                    <td className="nowrap" title={s.created_at}>{ago(s.created_at)}</td>
                    <td><span className={`badge ${cls}`}>{label}</span>{s.attempts > 1 && <span className="muted small"> ×{s.attempts}</span>}</td>
                    <td>{s.receipt_id ? <>{s.merchant_name || s.receipt_key}{s.total > 0 && <span className="muted"> · {fmtMoney(s.total, s.currency, decimalsOf(s.currency, currencies))}</span>}</> : <span className="muted ellipsis" style={{ maxWidth: 260, display: 'inline-block' }}>{s.receipt_key || s.url}</span>}<div className="muted small">{s.country}</div></td>
                    <td className="muted small">{s.source}</td>
                    <td className="small" style={{ maxWidth: 340 }}>{s.message}{s.next_try_at && <div className="muted">next try {ago(s.next_try_at)}</div>}</td>
                    <td className="nowrap" style={{ textAlign: 'right' }}>
                      {s.transaction_id && <Link className="btn ghost xs" to={`/transactions/${s.transaction_id}`} title="Open the transaction"><Icon name="arrow-right" size={13} /></Link>}
                      {s.has_raw && <a className="btn ghost xs" href={`${V1}/receipts/scans/${s.id}/raw`} target="_blank" rel="noreferrer" title="The tax office's raw answer"><Icon name="code" size={13} /></a>}
                      {s.provider && ['partial', 'pending', 'failed', 'invalid'].includes(s.status) && <button type="button" className="btn ghost xs" onClick={() => retry(s)} title="Ask the tax office again"><Icon name="refresh" size={13} /></button>}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </Card>
      )}
    </>
  )
}

function Shops() {
  const { currencies } = useApp()
  const { data, reload } = useAsync(() => api.get<Merchant[]>(`${V1}/merchants`), [])
  const [edit, setEdit] = useState<Merchant | null>(null)
  if (!data) return <Loading />
  if (data.length === 0) return <Empty title="No shops yet">Shops appear with their first scanned receipt. Give each one a short name, a category and an account here.</Empty>
  return (
    <>
      <Card flush>
        <table className="table compact">
          <thead><tr><th>Shop</th><th>Category</th><th>Receipts</th><th style={{ textAlign: 'right' }}>Spent</th><th>Last</th></tr></thead>
          <tbody>
            {data.map(m => (
              <tr key={m.id} {...clickRow(() => setEdit(m))} style={{ cursor: 'pointer' }}>
                <td><b>{m.display_name}</b><div className="muted small">{m.name} · {m.tax_id}{m.city ? ` · ${m.city}` : ''}</div></td>
                <td>{m.category_name ? `${m.category_icon || '•'} ${m.category_name}` : <span className="muted">learned</span>}</td>
                <td><Link to={`/products?merchant=${m.id}`} onClick={e => e.stopPropagation()}>{m.receipts}</Link></td>
                <td style={{ textAlign: 'right' }}>{fmtMoney(m.spent, m.currency, decimalsOf(m.currency, currencies))}</td>
                <td className="muted">{m.last_date ? fmtDate(m.last_date) : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {edit && <ShopForm m={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload() }} />}
    </>
  )
}

function ShopForm({ m, onClose, onSaved }: { m: Merchant; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [name, setName] = useState(m.display_name)
  const [cat, setCat] = useState<number | null>(m.category_id)
  const [acct, setAcct] = useState<number | null>(m.account_id)
  const [apply, setApply] = useState(true)
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const r = await api.put<{ updated: number }>(`${V1}/merchants/${m.id}`, { display_name: name, category_id: cat || 0, account_id: acct || 0, apply })
      toast(r.updated ? `Saved · ${r.updated} transactions updated` : 'Saved')
      onSaved()
    } catch (err) { toast(errMsg(err), 'err') }
  }
  return (
    <Modal title={m.display_name} onClose={onClose} size="sm">
      <form onSubmit={save} className="form">
        <Field label="Name on transactions" help={`The tax office calls it “${m.name}”.`}><input value={name} onChange={e => setName(e.target.value)} autoFocus /></Field>
        <Field label="Category" help="New receipts from this shop get it. Empty: learned from how you booked it before." group><CategorySelect value={cat} onChange={setCat} placeholder="Learned" clearLabel="Learned" /></Field>
        <Field label="Account" help="Always book this shop here (else by payment method)." group><AccountSelect value={acct} onChange={id => setAcct(id || null)} placeholder="By payment method" clearLabel="By payment method" /></Field>
        <SettingRow title="Also update what is already booked" help="Renames and re-categorises the transactions booked from this shop's receipts (ones you renamed by hand stay)."><Switch checked={apply} onChange={setApply} /></SettingRow>
        <div className="form-actions"><span className="spacer" /><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

function Setup() {
  const { settings } = useApp()
  const { toast } = useToast()
  const [st, setSt] = useState<ReceiptSettings | null>(null)
  const { data: providers } = useAsync(() => api.get<ReceiptProvider[]>(`${V1}/receipts/providers`), [])
  useEffect(() => { api.get<ReceiptSettings>(`${V1}/receipts/settings`).then(setSt) }, [])
  const save = async (patch: Partial<ReceiptSettings>) => {
    const next = { ...st!, ...patch }
    setSt(next)
    try { await api.put(`${V1}/receipts/settings`, { ...next, card_account_id: next.card_account_id || 0, cash_account_id: next.cash_account_id || 0 }); toast('Saved') } catch (e) { toast(errMsg(e), 'err') }
  }
  const base = settings?.base_url || window.location.origin
  if (!st) return <Loading />
  return (
    <div className="stack" style={{ display: 'grid', gap: 16 }}>
      <Card title="Booking">
        <SettingRow title="Card payments go to" help="Receipts paid by card (or anything but cash)."><div style={{ width: 240 }}><AccountSelect value={st.card_account_id} onChange={id => save({ card_account_id: id || null })} placeholder="Default account" clearLabel="Default account" /></div></SettingRow>
        <SettingRow title="Cash payments go to" help="Empty: your first cash account."><div style={{ width: 240 }}><AccountSelect value={st.cash_account_id} onChange={id => save({ cash_account_id: id || null })} placeholder="First cash account" clearLabel="First cash account" /></div></SettingRow>
        <SettingRow title="Link to what I typed" help="A receipt with the same amount within a day of a transaction you entered by hand is linked to it instead of booked twice."><Switch checked={st.attach_manual} onChange={v => save({ attach_manual: v })} /></SettingRow>
        <SettingRow title="Split by item category" help="Book a receipt in parts, one per category of its items' product groups (milk → Groceries, shampoo → Cosmetics). Off: one category, split later with one click."><Switch checked={st.split_items} onChange={v => save({ split_items: v })} /></SettingRow>
      </Card>
      <Card title="iPhone: scan from the Shortcuts app" sub="Faster than opening Pocket Money: scan, and a notification says what was booked.">
        <ol className="steps small">
          <li>Create a token in <Link to="/settings/tokens">Settings → API &amp; apps</Link> (name it “iPhone receipts”).</li>
          <li>In <b>Shortcuts</b>, new shortcut: <b>Scan QR or Barcode</b>.</li>
          <li>Add <b>Get Contents of URL</b> with this address, Method <b>POST</b>, header <code>Authorization</code> = <code>Bearer &lt;your token&gt;</code>, Request Body <b>Form</b> with <code>url</code> = <i>QR/Barcode</i> and <code>source</code> = <code>shortcut</code>:
            <CopyField value={`${base}/api/v1/receipts/scan?format=text`} /></li>
          <li>Add <b>Show Notification</b> with <i>Contents of URL</i>. Name it “Scan receipt” and add it to the Home Screen, or say “Hey Siri, scan receipt”.</li>
          <li>Optional: turn on <b>Show in Share Sheet</b> for URLs, so a receipt opened in Safari can be shared to it.</li>
        </ol>
        <p className="muted small">Pocket Money is only reachable on your tailnet, so the phone needs Tailscale on.</p>
      </Card>
      <Card title="Countries" sub="Each country's fiscal receipts are read by their own plug-in; more can be added.">
        <table className="table compact">
          <thead><tr><th>Country</th><th>Currency</th><th>Items</th><th /></tr></thead>
          <tbody>
            {(providers || []).map(p => (
              <tr key={p.id}><td><b>{p.name}</b><div className="muted small">{p.country}</div></td><td>{p.currency}</td><td>{p.items ? '✓' : '—'}</td><td className="muted small">{p.note}</td></tr>
            ))}
          </tbody>
        </table>
      </Card>
    </div>
  )
}
