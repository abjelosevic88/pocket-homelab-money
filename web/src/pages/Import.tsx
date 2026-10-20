import { useEffect, useState } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtDateTime, fmtMoney } from '../lib/format'
import { Card, Field, Segmented, useToast } from '../components/ui'
import { Select } from '../components/Select'
import { AccountSelect } from '../components/pickers'
import { useSettingsForm } from './Settings'

interface ImportResult { started: string; finished: string; accounts: number; categories: number; tags: number; subscriptions: number; goals: number; templates: number; created: number; updated: number; deleted: number; skipped: number; balances: string[]; warnings: string[]; error?: string }

export function ImportTab() {
  return <div className="grid" style={{ gap: 16 }}><FireflyImport /><CSVImport /></div>
}

function FireflyImport() {
  const { f, setF, save } = useSettingsForm()
  const { reloadMeta } = useApp()
  const { toast } = useToast()
  const [st, setSt] = useState<{ running: boolean; last: ImportResult | null } | null>(null)
  const poll = () => api.get<{ running: boolean; last: ImportResult | null }>(`${V1}/import/firefly/status`).then(setSt).catch(() => {})
  useEffect(() => { poll() }, [f?.firefly_configured])
  useEffect(() => {
    if (!st?.running) return
    const t = setInterval(poll, 1500)
    return () => clearInterval(t)
  }, [st?.running])
  useEffect(() => { if (st && !st.running && st.last) reloadMeta() }, [st?.running]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!f) return null
  const run = async (mode: 'all' | 'transactions') => {
    try { await api.post(`${V1}/import/firefly`, { mode }); toast('Import started'); setSt(s => ({ running: true, last: s?.last || null })); setTimeout(poll, 800) } catch (e) { toast((e as ApiError).message, 'err') }
  }
  const last = f.firefly_configured ? st?.last : null
  return (
    <Card title="🔥 From Firefly III (and Firefly Pico)">
      <p className="muted small">Uses your own Firefly III and brings it into your own ledger. Brings over asset accounts with their exact current balance, categories (with an emoji picked from the name), tags, every transaction (foreign amounts, notes and locations included), bills and recurring transactions merged into subscriptions (the <code>notify: N</code> note becomes the reminder), piggy banks as goals, and Firefly Pico's templates with their <code>[currency:XXX]</code>. Everything is keyed by its Firefly id: run it again and it updates instead of duplicating.</p>
      <div className="grid" style={{ gap: 12 }}>
        <div className="two-col tight">
          <Field label="Firefly III URL"><input value={f.firefly_url} onChange={e => setF({ ...f, firefly_url: e.target.value })} placeholder="http://host.docker.internal:8083" /></Field>
          <Field label="Personal access token"><input value={f.firefly_token} onChange={e => setF({ ...f, firefly_token: e.target.value })} autoComplete="off" /></Field>
        </div>
        <Field label="Firefly Pico URL (optional, for templates)"><input value={f.pico_url} onChange={e => setF({ ...f, pico_url: e.target.value })} placeholder="http://host.docker.internal:6977" /></Field>
        <label className="row"><input type="checkbox" checked={f.firefly_mirror} onChange={e => setF({ ...f, firefly_mirror: e.target.checked })} style={{ width: 'auto' }} />
          <span>Keep mirroring while Firefly is still in use — new and changed Firefly transactions of the last 45 days come over every 30 minutes, and automatic subscription booking waits (Firefly's recurring transactions still do it). Turn off on the day you switch.</span></label>
        <div className="row wrap">
          <button className="btn" onClick={async () => { await save(); await poll() }}>Save</button>
          <button className="btn primary" onClick={() => run('all')} disabled={!f.firefly_configured || st?.running}>{st?.running ? 'Importing…' : 'Import everything'}</button>
          <button className="btn" onClick={() => run('transactions')} disabled={!f.firefly_configured || st?.running}>Transactions only</button>
        </div>
        {last && (
          <div className={`callout ${last.error ? 'danger' : 'success'}`}>
            <b>{last.error ? `Import failed: ${last.error}` : `Last import ${fmtDateTime(last.finished)}`}</b>
            {!last.error && <div className="small mt-sm">{last.accounts} new accounts · {last.categories} categories · {last.tags} tags · {last.subscriptions} subscriptions · {last.goals} goals · {last.templates} templates · transactions +{last.created} / updated {last.updated} / removed {last.deleted}{last.skipped ? ` / skipped ${last.skipped}` : ''}</div>}
            {last.balances?.length > 0 && <div className="small mt-sm">Balances now: {last.balances.join(' · ')}</div>}
            {last.warnings?.length > 0 && <details className="small mt-sm"><summary>{last.warnings.length} notes</summary><ul>{last.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul></details>}
          </div>
        )}
      </div>
    </Card>
  )
}

interface Mapping { date: number; amount: number; debit: number; credit: number; description: number; payee: number; category: number; notes: number; currency: number; date_format: string; decimal: string; invert: boolean; has_header: boolean; delimiter: string }
interface Preview { header: string[]; rows: string[][]; total: number; mapping: Mapping }
interface RowResult { row: number; status: string; reason?: string; date: string; amount: number; kind: string; text: string }

function CSVImport() {
  const { acctById, currencies, reloadAccounts } = useApp()
  const { toast } = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [pv, setPv] = useState<Preview | null>(null)
  const [m, setM] = useState<Mapping | null>(null)
  const [account, setAccount] = useState<number | null>(null)
  const [result, setResult] = useState<{ created: number; duplicates: number; errors: number; rows: RowResult[]; dry: boolean } | null>(null)
  const [busy, setBusy] = useState(false)
  const load = async (fl: File) => {
    setFile(fl); setResult(null)
    const fd = new FormData(); fd.append('file', fl)
    const res = await fetch(`${V1}/import/csv/preview`, { method: 'POST', body: fd, credentials: 'same-origin' })
    const j = await res.json()
    if (!res.ok) { toast(j.error || 'Could not read the file', 'err'); return }
    setPv(j); setM(j.mapping)
  }
  const go = async (dry: boolean) => {
    if (!file || !m || !account) return
    setBusy(true)
    try {
      const fd = new FormData(); fd.append('file', file); fd.append('mapping', JSON.stringify(m)); fd.append('account_id', String(account)); if (dry) fd.append('dry_run', '1')
      const res = await fetch(`${V1}/import/csv`, { method: 'POST', body: fd, credentials: 'same-origin' })
      const j = await res.json()
      if (!res.ok) throw new Error(j.error)
      setResult({ ...j, dry })
      if (!dry) { toast(`${j.created} transactions imported`); reloadAccounts() }
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const cols = (pv?.header || []).map((h, i) => ({ value: String(i), label: h || `Column ${i + 1}` }))
  const col = (k: keyof Mapping, label: string) => m && (
    <Field label={label} group><Select value={(m[k] as number) >= 0 ? String(m[k]) : ''} onChange={v => setM({ ...m, [k]: v === '' ? -1 : Number(v) })} options={cols} clearLabel="—" placeholder="—" /></Field>
  )
  const acct = acctById(account)
  return (
    <Card title="📄 Bank statement (CSV)">
      <p className="muted small">Any bank's CSV export: pick the columns once, check with a dry run, import. Rows already in Pocket Money (same day, amount and text) are skipped, so overlapping statements are fine. Rules and learned categories apply to every row.</p>
      <div className="grid" style={{ gap: 12 }}>
        <div className="two-col tight">
          <Field label="File"><input type="file" accept=".csv,text/csv,text/plain" onChange={e => { const fl = e.target.files?.[0]; if (fl) load(fl) }} /></Field>
          <Field label="Into account" group><AccountSelect value={account} onChange={id => setAccount(id || null)} /></Field>
        </div>
        {pv && m && <>
          <div className="three-col tight" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))' }}>
            {col('date', 'Date')}{col('amount', 'Amount (±)')}{col('debit', 'or: money out')}{col('credit', 'and: money in')}{col('description', 'Description')}{col('payee', 'Payee')}{col('category', 'Category')}{col('notes', 'Notes')}{col('currency', 'Currency')}
          </div>
          <div className="row wrap">
            <Segmented size="sm" value={m.date_format} onChange={v => setM({ ...m, date_format: v })} options={[{ value: 'auto', label: 'Date: auto' }, { value: 'dmy', label: 'D.M.Y' }, { value: 'mdy', label: 'M/D/Y' }, { value: 'ymd', label: 'Y-M-D' }]} />
            <Segmented size="sm" value={m.decimal} onChange={v => setM({ ...m, decimal: v })} options={[{ value: 'auto', label: 'Decimal: auto' }, { value: 'comma', label: '1.234,56' }, { value: 'dot', label: '1,234.56' }]} />
            <label className="row small"><input type="checkbox" checked={m.invert} onChange={e => setM({ ...m, invert: e.target.checked })} style={{ width: 'auto' }} /> Positive amounts are expenses</label>
            <label className="row small"><input type="checkbox" checked={m.has_header} onChange={e => setM({ ...m, has_header: e.target.checked })} style={{ width: 'auto' }} /> First row is a header</label>
          </div>
          <div style={{ overflowX: 'auto' }}><table className="table compact small"><thead><tr>{pv.header.map((h, i) => <th key={i}>{h}</th>)}</tr></thead><tbody>{pv.rows.slice(0, 6).map((r, i) => <tr key={i}>{r.map((c, j) => <td key={j}>{c}</td>)}</tr>)}</tbody></table></div>
          <div className="row"><span className="muted small">{pv.total} rows</span><span className="spacer" /><button className="btn" disabled={!account || busy} onClick={() => go(true)}>Dry run</button><button className="btn primary" disabled={!account || busy} onClick={() => go(false)}>{busy ? 'Working…' : 'Import'}</button></div>
        </>}
        {result && (
          <div className="callout">
            <b>{result.dry ? 'Dry run' : 'Imported'}:</b> {result.dry ? `${result.rows.filter(r => r.status === 'ready').length} ready` : `${result.created} created`} · {result.duplicates} already there · {result.errors} errors
            <div style={{ maxHeight: 260, overflow: 'auto' }} className="mt-sm">
              <table className="table compact small"><tbody>{result.rows.slice(0, 200).map(r => <tr key={r.row} className={r.status === 'duplicate' ? 'muted' : ''}><td>{r.row}</td><td>{r.date ? fmtDate(r.date) : ''}</td><td>{r.text}</td><td className="num">{r.amount ? `${r.kind === 'expense' ? '−' : '+'}${fmtMoney(r.amount, acct?.currency || 'EUR', decimalsOf(acct?.currency || 'EUR', currencies))}` : ''}</td><td>{r.status}{r.reason ? `: ${r.reason}` : ''}</td></tr>)}</tbody></table>
            </div>
          </div>
        )}
      </div>
    </Card>
  )
}
