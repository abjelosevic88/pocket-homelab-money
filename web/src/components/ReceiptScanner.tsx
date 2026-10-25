import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney } from '../lib/format'
import { hostOf, isReceiptLink } from '../lib/receipts'
import type { ScanResult } from '../lib/types'
import { Modal, Spinner, errMsg } from './ui'
import { Select } from './Select'
import { Icon } from './icons'

type Detector = { detect: (src: CanvasImageSource) => Promise<{ rawValue: string }[]> }

/** The browser's own QR reader where there is one (Chrome on Android uses
 *  the system's); everywhere else, and when it finds nothing, ZXing. */
function makeDetector(): Detector | null {
  const BD = (window as unknown as { BarcodeDetector?: new (o: { formats: string[] }) => Detector }).BarcodeDetector
  try { return BD ? new BD({ formats: ['qr_code'] }) : null } catch { return null }
}

async function detect(detector: Detector | null, src: CanvasImageSource): Promise<string[]> {
  if (!detector) return []
  try { return (await detector.detect(src)).map(r => r.rawValue).filter(Boolean) } catch { return [] }
}

/** What the server read from the receipt, before anything is booked. */
interface Peek {
  supported: boolean; provider: string; country: string; message?: string; complete: boolean
  shop: string; location: string; date: string; time: string; total: number; currency: string; method: string; kind: string; invoice_number: string
  items: { name: string; quantity: number; unit: string; total: number; category?: string }[]
  account_name: string; category_id: number | null; category: string; reason: string; description: string
  existing?: { receipt_id: number; transaction_id: number | null; description: string; date: string; scanned_at: string }
}

type Step = 'idle' | 'scanning' | 'reading' | 'preview' | 'booking' | 'done'

const STATUS_ICON: Record<ScanResult['status'], string> = { booked: '✅', linked: '🔗', duplicate: '♻️', partial: '⏳', pending: '⏳', invalid: '⛔', skipped: '➖', unsupported: '❓', failed: '⚠️' }
const STATUS_TITLE: Record<ScanResult['status'], string> = {
  booked: 'Booked', linked: 'Linked to your transaction', duplicate: 'Already scanned', partial: 'Booked from the QR code',
  pending: 'Waiting for the tax office', invalid: 'Not a valid receipt', skipped: 'Nothing to book', unsupported: 'Not a receipt link', failed: 'Something went wrong',
}
const METHOD: Record<string, string> = { cash: '💵 cash', card: '💳 card', transfer: '🏦 transfer', voucher: '🎟 voucher', check: 'cheque', other: '' }

/** Scan a fiscal receipt's QR code with the camera, from a photo, or paste
 *  its link. The receipt is read and shown first; nothing is booked until
 *  the person confirms. QR codes that are not receipts (the shop's website,
 *  a loyalty app) are skipped while scanning. */
export function ReceiptScanner({ onClose, onBooked }: { onClose: () => void; onBooked?: (r: ScanResult) => void }) {
  const { accounts, currencies, reloadAccounts } = useApp()
  const video = useRef<HTMLVideoElement>(null)
  const canvas = useRef<HTMLCanvasElement | null>(null)
  const stream = useRef<MediaStream | null>(null)
  const [step, setStep] = useState<Step>('idle')
  const [camError, setCamError] = useState('')
  const [link, setLink] = useState('')
  const [account, setAccount] = useState('')
  const [found, setFound] = useState<{ url: string; source: string } | null>(null)
  const [peek, setPeek] = useState<Peek | null>(null)
  const [result, setResult] = useState<ScanResult | null>(null)
  const [error, setError] = useState('')
  const [other, setOther] = useState('') // a QR code that is not a receipt
  const [slow, setSlow] = useState(false)

  const stopCamera = () => {
    stream.current?.getTracks().forEach(t => t.stop())
    stream.current = null
  }
  useEffect(() => () => stopCamera(), [])
  const close = () => { stopCamera(); onClose() }

  /** Read the receipt (no booking) and show it. */
  const recognise = async (url: string, source: string) => {
    stopCamera()
    setFound({ url, source }); setStep('reading'); setError(''); setOther('')
    try {
      const p = await api.post<Peek>(`${V1}/receipts/peek`, { url })
      setPeek(p); setStep('preview')
    } catch (e) { setError(errMsg(e)); setStep('idle') }
  }

  const book = async () => {
    if (!found) return
    setStep('booking'); setError('')
    try {
      const r = await api.post<ScanResult>(`${V1}/receipts/scan`, { url: found.url, source: found.source, account_id: account ? Number(account) : 0 })
      setResult(r); setStep('done')
      if (r.transaction) { reloadAccounts().catch(() => {}); onBooked?.(r) }
    } catch (e) {
      const data = (e as { data?: Partial<ScanResult> }).data
      if (data?.status) { setResult(data as ScanResult); setStep('done') } else { setError(errMsg(e)); setStep('preview') }
    }
  }

  const start = async () => {
    setResult(null); setPeek(null); setFound(null); setError(''); setCamError(''); setSlow(false); setOther('')
    setStep('scanning')
    try {
      const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: 'environment' }, width: { ideal: 2560 }, height: { ideal: 1440 } }, audio: false })
      stream.current = s
      s.getVideoTracks()[0]?.applyConstraints({ advanced: [{ focusMode: 'continuous' } as MediaTrackConstraintSet] }).catch(() => {})
      const v = video.current!
      v.srcObject = s
      await v.play()
      const detector = makeDetector()
      const qr = await import('../lib/qr')
      canvas.current = canvas.current || document.createElement('canvas')
      const started = Date.now()
      let frame = 0
      const tick = async () => {
        if (!stream.current) return
        try {
          const c = canvas.current!
          const w = v.videoWidth, h = v.videoHeight
          if (w && h) {
            // full resolution: a receipt code needs every pixel
            if (c.width !== w || c.height !== h) { c.width = w; c.height = h }
            c.getContext('2d', { willReadFrequently: true })!.drawImage(v, 0, 0, w, h)
            let texts = await detect(detector, c)
            if (!texts.some(isReceiptLink)) texts = [...texts, ...await qr.decodeFrame(c, frame++, isReceiptLink)]
            const receipt = texts.find(isReceiptLink)
            if (receipt && stream.current) {
              navigator.vibrate?.(40)
              await recognise(receipt.trim(), 'camera')
              return
            }
            if (texts.length) setOther(hostOf(texts[0]))
            if (Date.now() - started > 7000) setSlow(true)
          }
        } catch { /* one bad frame must not stop the scanning */ }
        if (stream.current) setTimeout(tick, 60)
      }
      tick()
    } catch (e) {
      stopCamera()
      setStep('idle')
      setCamError((e as Error)?.name === 'NotAllowedError' ? 'Camera permission was refused.' : 'No camera available here (it needs HTTPS and a camera).')
    }
  }

  const fromPhoto = async (f: File) => {
    stopCamera()
    setResult(null); setPeek(null); setError(''); setOther('')
    setStep('reading')
    try {
      const bmp = await createImageBitmap(f)
      const qr = await import('../lib/qr')
      let texts = await detect(makeDetector(), bmp)
      if (!texts.some(isReceiptLink)) texts = [...texts, ...await qr.decodeImage(bmp, isReceiptLink)]
      const receipt = texts.find(isReceiptLink)
      if (receipt) { await recognise(receipt.trim(), 'photo'); return }
      setStep('idle')
      setError(texts.length
        ? `That photo has a QR code for ${hostOf(texts[0])}, not the fiscal one. Photograph the QR code at the bottom of the receipt.`
        : 'No QR code found in that photo. Take it closer, straight on, so the code fills most of the picture and is sharp.')
    } catch (e) { setError(errMsg(e)); setStep('idle') }
  }

  const writable = accounts.filter(a => !a.archived && a.role !== 'viewer')
  const r = result?.receipt
  const t = result?.transaction
  const p = peek
  const dec = (cur: string) => decimalsOf(cur, currencies)
  const scanning = step === 'scanning'
  return (
    <Modal title="Scan a receipt" onClose={close} className="scan-modal">
      <div className="scan">
        {(step === 'idle' || step === 'scanning' || step === 'reading') && <>
          <div className={`scan-view ${scanning ? 'on' : ''}`}>
            <video ref={video} playsInline muted />
            {scanning && <div className="scan-frame" aria-hidden />}
            {!scanning && step !== 'reading' && (
              <div className="scan-idle">
                <Icon name="camera" size={30} />
                <button type="button" className="btn primary" onClick={start}>{camError ? 'Try the camera again' : 'Start camera'}</button>
                {camError && <span className="bad small">{camError}</span>}
              </div>
            )}
            {step === 'reading' && <div className="scan-busy"><Spinner size={22} /> Reading the receipt…</div>}
            {scanning && other && <div className="scan-tip">That QR code is {other}, not the receipt's. Aim at the fiscal QR code at the bottom.</div>}
            {scanning && !other && slow && <div className="scan-tip">Hold the phone closer so the code fills the frame, and keep it still. Still nothing? Use <b>Take a photo</b>.</div>}
          </div>
          <div className="row wrap" style={{ gap: 8 }}>
            {scanning && <button type="button" className="btn" onClick={() => { stopCamera(); setStep('idle') }}>Stop camera</button>}
            <label className="btn"><Icon name="camera" size={15} />Take a photo<input type="file" accept="image/*" style={{ display: 'none' }} onChange={e => { const f = e.target.files?.[0]; if (f) fromPhoto(f); e.target.value = '' }} /></label>
          </div>
          <form className="row" style={{ gap: 8 }} onSubmit={e => { e.preventDefault(); if (link.trim()) recognise(link.trim(), 'web') }}>
            <input value={link} onChange={e => setLink(e.target.value)} placeholder="…or paste the receipt link (https://suf.poreskaupravars.org/v/?vl=…)" aria-label="Receipt link" />
            <button className="btn" disabled={!link.trim() || step === 'reading'}>Read</button>
          </form>
          {error && <div className="bad small">{error}</div>}
          <p className="muted small">Hold the phone close so the QR code at the bottom of the receipt fills the frame. Pocket Money shows what it read and books it only when you confirm.</p>
        </>}

        {(step === 'preview' || step === 'booking') && p && (
          <div className="scan-result">
            {!p.supported ? (
              <div className="scan-result-head"><span className="big">❓</span><div><b>Not a receipt Pocket Money can read</b><div className="muted small">{p.message}</div></div></div>
            ) : (
              <>
                <div className="scan-result-head">
                  <span className="big">{p.existing ? '♻️' : '🧾'}</span>
                  <div>
                    <b>{p.existing ? 'You scanned this one already' : 'Receipt recognised'}</b>
                    <div className="muted small">{p.existing ? `On ${fmtDate(p.existing.scanned_at)}${p.existing.description ? `, booked as “${p.existing.description}”` : ''}.` : p.message || 'Check it and book it.'}</div>
                  </div>
                </div>
                <div className="scan-sum">
                  <div className="row"><b>{p.shop || 'Shop to follow'}</b><span className="spacer" /><b>{p.kind === 'refund' ? '+' : '−'}{fmtMoney(p.total, p.currency, dec(p.currency))}</b></div>
                  <div className="muted small">{[p.date && fmtDate(p.date), p.time, p.location, METHOD[p.method] ?? p.method].filter(Boolean).join(' · ')}</div>
                  {p.items.length > 0 && (
                    <ul className="scan-items">
                      {p.items.slice(0, 8).map((it, i) => <li key={i}><span>{it.name}{it.category && <span className="muted"> · {it.category}</span>}</span><span className="muted">{fmtMoney(it.total, p.currency, dec(p.currency), { code: false })}</span></li>)}
                      {p.items.length > 8 && <li className="muted">+ {p.items.length - 8} more</li>}
                    </ul>
                  )}
                  {!p.existing && (
                    <div className="scan-will small">
                      <span>→ {p.description || p.shop}</span>
                      <span>{p.category ? <b>{p.category}</b> : <span className="muted">category picked when booking</span>}{p.reason && <span className="muted"> · {p.reason}</span>}</span>
                      <span className="muted">on {account ? writable.find(a => String(a.id) === account)?.name : p.account_name || 'your default account'}</span>
                    </div>
                  )}
                </div>
                {!p.existing && (
                  <div style={{ maxWidth: 280 }}><Select value={account} onChange={setAccount} options={writable.map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} placeholder={`Account: ${p.account_name || 'automatic'}`} clearLabel={`Automatic (${p.account_name || 'by payment'})`} ariaLabel="Account" /></div>
                )}
              </>
            )}
            {error && <div className="bad small">{error}</div>}
            <div className="row wrap" style={{ gap: 8 }}>
              {p.supported && !p.existing && <button type="button" className="btn primary" disabled={step === 'booking'} onClick={book}>{step === 'booking' ? <><Spinner /> Booking…</> : <><Icon name="check" size={15} />Book it</>}</button>}
              {p.existing?.transaction_id && <Link className="btn primary" to={`/transactions/${p.existing.transaction_id}`} onClick={close}>Open it</Link>}
              <button type="button" className="btn" disabled={step === 'booking'} onClick={start}><Icon name="camera" size={15} />Scan again</button>
              <span className="spacer" />
              <button type="button" className="btn ghost" onClick={close}>Cancel</button>
            </div>
          </div>
        )}

        {step === 'done' && result && (
          <div className={`scan-result ${result.status}`}>
            <div className="scan-result-head">
              <span className="big">{STATUS_ICON[result.status]}</span>
              <div>
                <b>{STATUS_TITLE[result.status]}</b>
                <div className="muted small">{result.message}</div>
              </div>
            </div>
            {r && t && <div className="small">{t.category_icon || '•'} {t.category_name || 'No category'} · {t.description} · {t.account_name}{r.category_reason && <span className="muted"> · {r.category_reason}</span>}</div>}
            <div className="row wrap" style={{ gap: 8 }}>
              <button type="button" className="btn primary" onClick={start}><Icon name="camera" size={15} />Scan the next one</button>
              {t && <Link className="btn" to={`/transactions/${t.id}`} onClick={close}>Open the transaction</Link>}
              <span className="spacer" />
              <button type="button" className="btn ghost" onClick={close}>Done</button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
