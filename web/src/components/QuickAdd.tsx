import { useEffect, useRef, useState } from 'react'
import { api, upload, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import type { Draft, Transaction } from '../lib/types'
import { useDebounce, useToast } from './ui'

/** One line → one (or more, split by ";" or new lines) transactions.
 *  Parsed on the server as you type; Enter books it. The ✨ button sends the
 *  text to the AI instead (several items, any wording), 🎙 dictates. */
export function QuickAdd({ onAdded, autoFocus = false, placeholder }: { onAdded: (t: Transaction[]) => void; autoFocus?: boolean; placeholder?: string }) {
  const { settings, currencies, reloadAccounts } = useApp()
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [drafts, setDrafts] = useState<Draft[]>([])
  const [aiDrafts, setAiDrafts] = useState<Draft[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [rec, setRec] = useState<MediaRecorder | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const dq = useDebounce(text, 160)
  const ai = settings?.ai_configured

  useEffect(() => {
    if (aiDrafts) return
    if (!dq.trim()) { setDrafts([]); return }
    api.post<Draft[]>(`${V1}/quick/parse`, { text: dq }).then(setDrafts).catch(() => setDrafts([]))
  }, [dq, aiDrafts])

  const shown = aiDrafts || drafts
  const ready = shown.length > 0 && shown.every(d => d.ready)
  const book = async () => {
    if (!text.trim() || busy) return
    setBusy(true)
    try {
      const body = aiDrafts ? { drafts: aiDrafts } : { text }
      const out = await api.post<Transaction[]>(`${V1}/quick`, body)
      toast(out.length === 1 ? `Added ${out[0].description || 'transaction'}` : `Added ${out.length} transactions`)
      setText(''); setDrafts([]); setAiDrafts(null)
      reloadAccounts()
      onAdded(out)
      input.current?.focus()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const askAI = async (t = text) => {
    if (!t.trim()) return
    setBusy(true)
    try {
      const out = await api.post<Draft[]>(`${V1}/ai/parse`, { text: t })
      if (out.length === 0) toast('The AI found no amounts in that', 'err')
      setAiDrafts(out)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const toggleMic = async () => {
    if (rec) { rec.stop(); return }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const mr = new MediaRecorder(stream)
      const chunks: Blob[] = []
      mr.ondataavailable = e => chunks.push(e.data)
      mr.onstop = async () => {
        stream.getTracks().forEach(t => t.stop())
        setRec(null)
        setBusy(true)
        try {
          const blob = new Blob(chunks, { type: mr.mimeType || 'audio/webm' })
          const ext = (mr.mimeType || '').includes('mp4') ? 'm4a' : 'webm'
          const r = await upload<{ text: string }>(`${V1}/ai/transcribe`, 'file', new File([blob], `note.${ext}`, { type: blob.type }))
          setText(r.text)
          setBusy(false)
          await askAI(r.text)
        } catch (e) { toast((e as Error).message, 'err'); setBusy(false) }
      }
      mr.start()
      setRec(mr)
    } catch { toast('Microphone not available (needs HTTPS and permission)', 'err') }
  }

  return (
    <div className="quick-bar">
      <div className="qinput">
        <span className="plus">＋</span>
        <input ref={input} value={text} autoFocus={autoFocus} placeholder={placeholder || 'kafa 3,5 · cg hrana 20 eur · +1500 plata · gorivo 80 juče'}
          onChange={e => { setText(e.target.value); setAiDrafts(null) }}
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); if (ready) book(); else if (ai && text.trim()) askAI() } if (e.key === 'Escape') { setText(''); setAiDrafts(null) } }}
          aria-label="Quick add" autoComplete="off" enterKeyHint="done" />
        {ai && <button type="button" className={`btn ghost icon mic ${rec ? 'rec' : ''}`} onClick={toggleMic} title="Dictate" disabled={busy && !rec}>🎙</button>}
        {ai && text.trim() && !aiDrafts && <button type="button" className="btn ghost sm" onClick={() => askAI()} disabled={busy} title="Let the AI read it (several items, any wording)">✨ AI</button>}
        <button type="button" className="btn primary" onClick={book} disabled={!ready || busy}>{busy ? '…' : shown.length > 1 ? `Add ${shown.length}` : 'Add'}</button>
      </div>
      {shown.length > 0 && (
        <div className="drafts">
          {shown.map((d, i) => {
            const dec = decimalsOf(d.currency || d.account_currency, currencies)
            return (
              <div key={i} className={`draft ${d.problem ? 'problem' : ''}`}>
                <span style={{ fontSize: 18 }}>{d.kind === 'transfer' ? '⇄' : d.category_icon || (d.kind === 'income' ? '💰' : '•')}</span>
                <span className="what">{d.description || '—'}</span>
                <span className="bits">
                  {d.amount != null && <b className={d.kind === 'income' ? 'ok' : ''}>{d.kind === 'income' ? '+' : d.kind === 'expense' ? '−' : ''}{fmtMoney(d.amount, d.currency || d.account_currency, dec)}</b>}
                  {d.account_amount != null && d.currency && d.currency !== d.account_currency && <span>≈ {fmtMoney(d.account_amount, d.account_currency, decimalsOf(d.account_currency, currencies))}</span>}
                  <span>{d.category_name || (d.kind === 'transfer' ? '' : 'no category')}</span>
                  <span>{d.account_name}{d.to_account_name ? ` → ${d.to_account_name}` : ''}</span>
                  {d.date !== todayISO() && <span>{fmtDate(d.date, false)}</span>}
                  {d.tags.map(t => <span key={t} className="badge tag">{t}</span>)}
                  {d.recognized.filter(r => r.startsWith('template') || r === 'AI').map(r => <span key={r} className="hint-pill">{r}</span>)}
                  {d.problem && <span className="bad">{d.problem}</span>}
                </span>
              </div>
            )
          })}
        </div>
      )}
      {!text && <div className="quick-help">Type what you paid: <code>kafa 3,5</code> · currency <code>20 eur</code> · income <code>+1500 plata</code> · day <code>juče</code> <code>-2d</code> <code>12.10.</code> · account <code>@cash</code> · tag <code>#trip</code>. Several at once with <code>;</code>.{ai ? ' ✨ or 🎙 for anything else.' : ''}</div>}
    </div>
  )
}
