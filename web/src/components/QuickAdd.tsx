import { useEffect, useRef, useState } from 'react'
import { api, upload, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO } from '../lib/format'
import type { Currency, Draft, QuickSuggestion, Transaction } from '../lib/types'
import { useDebounce, useToast } from './ui'
import { Icon } from './icons'

/** The words at the start of the line being typed that can still name a
 *  template (before the amount, @account, #tag…). Picking a suggestion or Esc
 *  hides the list until these change. */
function leadWords(text: string): string {
  const line = text.slice(Math.max(text.lastIndexOf('\n'), text.lastIndexOf(';')) + 1).trim().replace(/^\+(?=\D)/, '')
  const out: string[] = []
  for (const w of line.split(/\s+/)) {
    if (!w || /\d/.test(w) || /^[@#>→+-]/.test(w)) break
    out.push(w.toLowerCase())
  }
  return out.join(' ')
}

/** One line → one (or more, split by ";" or new lines) transactions.
 *  Parsed on the server as you type; Enter books it. Templates that fit the
 *  words typed so far are listed under the bar with what each would book
 *  (↑↓ + Tab/Enter or click picks one). A sum works as the amount
 *  ("4800-10.23"). The ✨ button sends the text to the AI instead (several
 *  items, any wording), 🎙 dictates. */
export function QuickAdd({ onAdded, autoFocus = false, placeholder }: { onAdded: (t: Transaction[]) => void; autoFocus?: boolean; placeholder?: string }) {
  const { settings, currencies, reloadAccounts } = useApp()
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [drafts, setDrafts] = useState<Draft[]>([])
  const [sugs, setSugs] = useState<QuickSuggestion[]>([])
  const [active, setActive] = useState(-1)
  const [closedLead, setClosedLead] = useState<string | null>(null)
  const [aiDrafts, setAiDrafts] = useState<Draft[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [rec, setRec] = useState<MediaRecorder | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const dq = useDebounce(text, 160)
  const ai = settings?.ai_configured

  useEffect(() => {
    if (aiDrafts) return
    if (!dq.trim()) { setDrafts([]); setSugs([]); return }
    api.post<Draft[]>(`${V1}/quick/parse`, { text: dq }).then(setDrafts).catch(() => setDrafts([]))
    api.post<{ suggestions: QuickSuggestion[] }>(`${V1}/quick/suggest`, { text: dq })
      .then(r => { setSugs(r.suggestions); setActive(-1) }).catch(() => setSugs([]))
  }, [dq, aiDrafts])

  const shown = aiDrafts || drafts
  const current = drafts.length ? drafts[drafts.length - 1].template_id : 0
  const lead = leadWords(text)
  // one suggestion that is already what Enter books has nothing to offer
  const showSugs = !aiDrafts && !!text.trim() && sugs.length > 0 && lead !== closedLead && !(sugs.length === 1 && sugs[0].template_id === current)
  const pick = (sg: QuickSuggestion) => {
    const cut = Math.max(text.lastIndexOf('\n'), text.lastIndexOf(';'))
    const next = (cut >= 0 ? text.slice(0, cut + 1) + ' ' : '') + sg.line + (sg.preview.amount == null ? ' ' : '')
    setText(next); setClosedLead(leadWords(next)); setActive(-1)
    input.current?.focus()
  }
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
        <span className="plus"><Icon name="plus" size={20} strokeWidth={2.2} /></span>
        <input ref={input} value={text} autoFocus={autoFocus} placeholder={placeholder || 'kafa 3,5 · cg hrana 20 eur · +1500 plata · gorivo 80 juče'}
          onChange={e => { setText(e.target.value); setAiDrafts(null) }}
          role="combobox" aria-expanded={showSugs} aria-controls="quick-suggest" aria-activedescendant={showSugs && active >= 0 ? `quick-sug-${active}` : undefined}
          onKeyDown={e => {
            if (showSugs) {
              if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); setActive(a => e.key === 'ArrowDown' ? Math.min(a + 1, sugs.length - 1) : Math.max(a - 1, -1)); return }
              if (e.key === 'Tab' && !e.shiftKey) { e.preventDefault(); pick(sugs[Math.max(active, 0)]); return }
              if (e.key === 'Enter' && active >= 0) { e.preventDefault(); pick(sugs[active]); return }
              if (e.key === 'Escape') { setClosedLead(lead); return }
            }
            if (e.key === 'Enter') { e.preventDefault(); if (ready) book(); else if (ai && text.trim()) askAI() }
            if (e.key === 'Escape') { setText(''); setAiDrafts(null) }
          }}
          aria-label="Quick add" autoComplete="off" enterKeyHint="done" />
        {settings?.ai_transcribe_configured && <button type="button" className={`btn ghost icon mic ${rec ? 'rec' : ''}`} onClick={toggleMic} title="Dictate" aria-label="Dictate" disabled={busy && !rec}><Icon name="mic" size={18} /></button>}
        {ai && text.trim() && !aiDrafts && <button type="button" className="btn ghost sm" onClick={() => askAI()} disabled={busy} title="Let the AI read it (several items, any wording)"><Icon name="sparkles" size={15} />AI</button>}
        <button type="button" className="btn primary" onClick={book} disabled={!ready || busy}>{busy ? '…' : shown.length > 1 ? `Add ${shown.length}` : 'Add'}</button>
      </div>
      {showSugs && (
        <div className="tpl-suggest" id="quick-suggest" role="listbox" aria-label="Templates">
          {sugs.map((sg, i) => (
            <div key={sg.template_id} id={`quick-sug-${i}`} role="option" aria-selected={i === active} className={`draft ${i === active ? 'active' : ''}`}
              onMouseDown={e => e.preventDefault()} onClick={() => pick(sg)} onMouseEnter={() => setActive(i)}>
              <DraftLine d={sg.preview} currencies={currencies} suggestion={sg} current={sg.template_id === current} />
            </div>
          ))}
          <div className="tpl-suggest-help">↑↓ choose · Tab or click to use · Esc hides</div>
        </div>
      )}
      {shown.length > 0 && (
        <div className="drafts">
          {shown.map((d, i) => (
            <div key={i} className={`draft ${d.problem ? 'problem' : ''}`}><DraftLine d={d} currencies={currencies} /></div>
          ))}
        </div>
      )}
      {!text && <div className="quick-help">Type what you paid: <code>kafa 3,5</code> · currency <code>20 eur</code> · income <code>+1500 plata</code> · day <code>juče</code> <code>-2d</code> <code>12.10.</code> · account <code>@cash</code> · tag <code>#trip</code>. Several at once with <code>;</code>.{ai ? ' ✨ for flexible wording.' : ''}{settings?.ai_transcribe_configured ? ' 🎙 to dictate.' : ''}</div>}
    </div>
  )
}

/** What a line will book: icon, description, the sum and its result,
 *  category, account, day, tags. A suggestion shows the template's name and
 *  leaves the missing amount quiet instead of flagging it. */
function DraftLine({ d, currencies, suggestion, current }: { d: Draft; currencies: Currency[]; suggestion?: QuickSuggestion; current?: boolean }) {
  const dec = decimalsOf(d.currency || d.account_currency, currencies)
  const sign = d.kind === 'income' ? '+' : d.kind === 'expense' ? '−' : ''
  return (
    <>
      <span style={{ fontSize: 18 }}>{d.kind === 'transfer' ? '⇄' : d.category_icon || (d.kind === 'income' ? '💰' : '•')}</span>
      {suggestion
        ? <span className="what">{suggestion.name}{suggestion.alias && <span className="alias"> · {suggestion.alias}</span>}{d.description && d.description !== suggestion.name && <span className="alias"> — {d.description}</span>}</span>
        : <span className="what">{d.description || '—'}</span>}
      <span className="bits">
        {d.calc && d.amount != null && <span className="calc">{d.calc} =</span>}
        {d.amount != null && <b className={d.kind === 'income' ? 'ok' : ''}>{sign}{fmtMoney(d.amount, d.currency || d.account_currency, dec)}</b>}
        {d.account_amount != null && d.currency && d.currency !== d.account_currency && <span>≈ {fmtMoney(d.account_amount, d.account_currency, decimalsOf(d.account_currency, currencies))}</span>}
        <span>{d.category_name || (d.kind === 'transfer' ? '' : 'no category')}</span>
        <span>{d.account_name}{d.to_account_name ? ` → ${d.to_account_name}` : ''}</span>
        {d.date !== todayISO() && <span>{fmtDate(d.date, false)}</span>}
        {d.tags.map(t => <span key={t} className="badge tag">{t}</span>)}
        {suggestion
          ? <>{d.amount == null && <span>no amount yet</span>}{current && <span className="hint-pill">↵ now</span>}</>
          : <>
            {d.recognized.filter(r => r.startsWith('template') || r === 'AI').map(r => <span key={r} className="hint-pill">{r}</span>)}
            {d.problem && <span className="bad">{d.problem}</span>}
          </>}
      </span>
    </>
  )
}
