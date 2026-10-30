// Day review: every night the AI looks over the past day's bookings and
// flags what looks off. Report mode lists it here to apply or ignore; fix
// mode corrects category and description itself (undo is one click).
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney } from '../lib/format'
import type { DayFinding, DayFindingTx, DayReviewState } from '../lib/types'
import { Card, Empty, Loading, PageHeader, Segmented, SettingRow, Spinner, Switch, errMsg, useAsync, useToast } from '../components/ui'
import { Select } from '../components/Select'
import { Icon } from '../components/icons'

const isoDay = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const yesterday = () => { const d = new Date(); d.setDate(d.getDate() - 1); return isoDay(d) }

const STATUS: Record<string, { label: string; cls: string }> = {
  open: { label: 'to look at', cls: 'warn' }, applied: { label: 'applied', cls: 'ok' }, fixed: { label: 'corrected by AI', cls: 'ok' },
  ignored: { label: 'ignored', cls: '' }, done: { label: 'done', cls: '' }, undone: { label: 'undone', cls: '' },
}

export default function Review() {
  const { toast } = useToast()
  const { data, reload, setData } = useAsync(() => api.get<DayReviewState>(`${V1}/day-review`), [])
  const [day, setDay] = useState(yesterday())
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  const notifyBadge = () => window.dispatchEvent(new CustomEvent('pm:review'))
  const saveSettings = async (patch: Partial<DayReviewState['settings']>) => {
    try { setData(await api.put<DayReviewState>(`${V1}/day-review/settings`, patch)) } catch (e) { toast(errMsg(e), 'err') }
  }
  const runNow = async () => {
    setBusy(true)
    try {
      const r = await api.post<DayReviewState & { run: number }>(`${V1}/day-review/run`, { day })
      setData(r); setOpen(r.run); notifyBadge()
      const run = r.reviews.find(x => x.id === r.run)
      toast(run ? (run.findings.length ? `${run.findings.length} to look at` : `${run.checked} bookings, all fine`) : 'Done')
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const act = async (f: DayFinding, action: 'apply' | 'ignore' | 'done' | 'undo') => {
    try { setData(await api.post<DayReviewState>(`${V1}/day-review/findings/${f.id}/${action}`)); notifyBadge() } catch (e) { toast(errMsg(e), 'err'); reload() }
  }
  if (!data) return <Loading />
  const st = data.settings
  const reviews = data.reviews
  const shown = reviews.find(r => r.id === open) || reviews[0]
  const hours = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: `${String(h).padStart(2, '0')}:00` }))
  return (
    <>
      <PageHeader title="Day review" sub="Every night the AI looks over the past day's bookings — typed, from the phone, templates, receipts — and flags what looks off." actions={<>
        <input type="date" className="input" value={day} max={isoDay(new Date())} onChange={e => setDay(e.target.value)} aria-label="Day to review" style={{ width: 160 }} />
        <button type="button" className="btn primary" disabled={busy || !data.ai_ready} onClick={runNow}>{busy ? <><Spinner /> Reviewing…</> : <><Icon name="sparkles" size={15} />Review this day</>}</button>
      </>} />

      <Card title="Settings">
        <SettingRow title="Review every day" help={<>Looks over the day before. <b>Report</b> lists what looks off for you to apply or ignore. <b>Fix</b> also corrects categories and descriptions itself (undo below); possible duplicates and odd amounts are always left to you. {data.ai_ready ? <b>{data.ai_detail}.</b> : <span className="bad">{data.ai_detail}</span>}</>}>
          <Segmented size="sm" value={st.mode} onChange={v => saveSettings({ mode: v })} options={[{ value: 'off', label: 'Off' }, { value: 'report', label: 'Report', disabled: !data.ai_ready && st.mode !== 'report' }, { value: 'fix', label: 'Fix', disabled: !data.ai_ready && st.mode !== 'fix' }]} />
        </SettingRow>
        <SettingRow title="At" help="The past day is reviewed at this hour (00:00 = right after midnight).">
          <div style={{ width: 120 }}><Select value={String(st.hour)} onChange={v => saveSettings({ hour: Number(v) })} options={hours} /></div>
        </SettingRow>
        <SettingRow title="Notify me" help="A notification when the review found something (not when the day was fine)."><Switch checked={st.notify} disabled={st.mode === 'off'} onChange={v => saveSettings({ notify: v })} /></SettingRow>
      </Card>

      {reviews.length === 0 ? <Empty title="No reviews yet" icon="sparkles">The first one runs tonight, or review a day now.</Empty> : <>
        <div className="filters" style={{ margin: '16px 0 12px' }}>
          {reviews.slice(0, 14).map(r => {
            const n = r.findings.filter(f => f.status === 'open').length
            return (
              <button key={r.id} type="button" className={`chip ${shown?.id === r.id ? 'active' : ''}`} onClick={() => setOpen(r.id)}>
                {fmtDate(r.day, false)}{r.trigger === 'manual' ? ' · by hand' : ''}{n > 0 && <span className="badge danger">{n}</span>}
              </button>
            )
          })}
        </div>
        {shown && <ReviewRun run={shown} act={act} />}
      </>}
    </>
  )
}

function ReviewRun({ run, act }: { run: DayReviewState['reviews'][number]; act: (f: DayFinding, a: 'apply' | 'ignore' | 'done' | 'undo') => void }) {
  return <>
    <div className="stats">
      <div className="stat"><div className="n">{run.checked}</div><div className="l">bookings on {fmtDate(run.day, false)}</div></div>
      <div className="stat"><div className="n">{run.findings.filter(f => f.status === 'open').length}</div><div className="l">to look at</div></div>
      <div className="stat"><div className="n">{run.findings.filter(f => f.status === 'fixed' || f.status === 'applied').length}</div><div className="l">corrected</div></div>
    </div>
    {run.note && <div className="rc-note small" style={{ marginBottom: 12 }}>{run.note}</div>}
    {run.error && <div className="bad small" style={{ marginBottom: 12 }}>{run.error}</div>}
    {run.findings.length === 0 ? <Empty title="All fine" icon="check">Nothing on this day looked off.</Empty> : (
      <div style={{ display: 'grid', gap: 10 }}>{run.findings.map(f => <FindingCard key={f.id} f={f} act={act} />)}</div>
    )}
  </>
}

function TxLine({ t }: { t: DayFindingTx }) {
  const { currencies } = useApp()
  return (
    <Link to={`/transactions/${t.id}`} className="review-tx">
      <span className="muted small">{t.time || '—'}</span>
      <span className="review-tx-desc">{t.description || <i className="muted">no description</i>}{t.payee && t.payee !== t.description ? <span className="muted small"> · {t.payee}</span> : null}</span>
      <span className="muted small nowrap">{t.category_icon} {t.category_name || 'no category'}</span>
      <span className="nowrap" style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtMoney(t.kind === 'expense' ? -t.amount : t.amount, t.currency, decimalsOf(t.currency, currencies))}</span>
    </Link>
  )
}

function FindingCard({ f, act }: { f: DayFinding; act: (f: DayFinding, a: 'apply' | 'ignore' | 'done' | 'undo') => void }) {
  const s = STATUS[f.status] || { label: f.status, cls: '' }
  const changes = !!f.category_id || !!f.description
  const doneish = f.status !== 'open'
  return (
    <div className={`card review-finding ${doneish ? 'is-done' : ''}`}>
      <div className="review-head">
        <span className={`badge ${s.cls}`}>{s.label}</span>
        {f.note && <span className="small">{f.note}</span>}
      </div>
      {f.transaction ? <TxLine t={f.transaction} /> : <div className="muted small">The transaction was deleted.</div>}
      {changes && (
        <div className="review-change small">
          {f.category_id && <div><Icon name="tag" size={13} /> Category {f.status === 'fixed' || f.status === 'applied' ? <><s className="muted">{f.before_category_name || 'none'}</s> → </> : '→ '}<b>{f.category_icon} {f.category_name}</b></div>}
          {f.description && <div><Icon name="edit" size={13} /> Description {f.status === 'fixed' || f.status === 'applied' ? <><s className="muted">{f.before_description || 'empty'}</s> → </> : '→ '}<b>{f.description}</b></div>}
        </div>
      )}
      {f.duplicate_of && (
        <div className="review-change small">
          <div><Icon name="copy" size={13} /> Looks like a second booking of:</div>
          {f.duplicate ? <TxLine t={f.duplicate} /> : <div className="muted">a transaction that was deleted since</div>}
        </div>
      )}
      {f.odd_amount && <div className="review-change small"><Icon name="alert" size={13} /> The amount looks unusual — check it.</div>}
      <div className="row" style={{ gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
        {f.status === 'open' && changes && f.transaction && <button type="button" className="btn primary sm" onClick={() => act(f, 'apply')}><Icon name="check" size={14} />Apply</button>}
        {f.status === 'open' && !changes && <button type="button" className="btn sm" onClick={() => act(f, 'done')}><Icon name="check" size={14} />Done</button>}
        {f.status === 'open' && <button type="button" className="btn ghost sm" onClick={() => act(f, 'ignore')}>Ignore</button>}
        {(f.status === 'applied' || f.status === 'fixed') && <button type="button" className="btn ghost sm" onClick={() => act(f, 'undo')}><Icon name="undo" size={14} />Undo</button>}
      </div>
    </div>
  )
}
