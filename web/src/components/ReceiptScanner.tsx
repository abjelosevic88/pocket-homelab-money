import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney } from '../lib/format'
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

async function detect(detector: Detector | null, src: CanvasImageSource): Promise<string | null> {
  if (!detector) return null
  try { return (await detector.detect(src))[0]?.rawValue || null } catch { return null }
}

const STATUS_ICON: Record<ScanResult['status'], string> = { booked: '✅', linked: '🔗', duplicate: '♻️', partial: '⏳', pending: '⏳', invalid: '⛔', skipped: '➖', unsupported: '❓', failed: '⚠️' }
const STATUS_TITLE: Record<ScanResult['status'], string> = {
  booked: 'Booked', linked: 'Linked to your transaction', duplicate: 'Already scanned', partial: 'Booked from the QR code',
  pending: 'Waiting for the tax office', invalid: 'Not a valid receipt', skipped: 'Nothing to book', unsupported: 'Not a receipt link', failed: 'Something went wrong',
}

/** Scan a fiscal receipt's QR code with the camera, from a photo, or paste
 *  its link. The server reads the receipt and books it. */
export function ReceiptScanner({ onClose, onBooked }: { onClose: () => void; onBooked?: (r: ScanResult) => void }) {
  const { accounts, currencies, reloadAccounts } = useApp()
  const video = useRef<HTMLVideoElement>(null)
  const canvas = useRef<HTMLCanvasElement | null>(null)
  const stream = useRef<MediaStream | null>(null)
  const [camera, setCamera] = useState<'off' | 'starting' | 'on' | 'error'>('off')
  const [camError, setCamError] = useState('')
  const [link, setLink] = useState('')
  const [account, setAccount] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ScanResult | null>(null)
  const [error, setError] = useState('')
  const busyRef = useRef(false)

  const stop = () => {
    stream.current?.getTracks().forEach(t => t.stop())
    stream.current = null
    setCamera('off')
  }
  useEffect(() => () => stop(), [])

  const submit = async (url: string, source: string) => {
    if (busyRef.current || !url.trim()) return
    busyRef.current = true
    setBusy(true); setError('')
    try {
      const r = await api.post<ScanResult>(`${V1}/receipts/scan`, { url: url.trim(), source, account_id: account ? Number(account) : 0 })
      setResult(r)
      if (r.transaction) { reloadAccounts().catch(() => {}); onBooked?.(r) }
    } catch (e) {
      const data = (e as { data?: Partial<ScanResult> }).data
      if (data?.status) setResult(data as ScanResult); else setError(errMsg(e))
    } finally { busyRef.current = false; setBusy(false) }
  }

  const [slow, setSlow] = useState(false)
  const start = async () => {
    setResult(null); setError(''); setCamError(''); setSlow(false)
    setCamera('starting')
    try {
      const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: 'environment' }, width: { ideal: 2560 }, height: { ideal: 1440 } }, audio: false })
      stream.current = s
      // keep refocusing where the phone allows it
      s.getVideoTracks()[0]?.applyConstraints({ advanced: [{ focusMode: 'continuous' } as MediaTrackConstraintSet] }).catch(() => {})
      const v = video.current!
      v.srcObject = s
      await v.play()
      setCamera('on')
      const detector = makeDetector()
      const qr = await import('../lib/qr')
      canvas.current = canvas.current || document.createElement('canvas')
      const started = Date.now()
      let frame = 0
      const tick = async () => {
        if (!stream.current) return
        if (!busyRef.current) {
          try {
            const c = canvas.current!
            const w = v.videoWidth, h = v.videoHeight
            if (w && h) {
              // full resolution: a receipt code needs every pixel
              if (c.width !== w || c.height !== h) { c.width = w; c.height = h }
              c.getContext('2d', { willReadFrequently: true })!.drawImage(v, 0, 0, w, h)
              const text = (await detect(detector, c)) || (await qr.decodeFrame(c, frame++))
              if (text && stream.current) {
                stop()
                navigator.vibrate?.(60)
                await submit(text, 'camera')
                return
              }
              if (Date.now() - started > 7000) setSlow(true)
            }
          } catch { /* one bad frame must not stop the scanning */ }
        }
        if (stream.current) setTimeout(tick, 60)
      }
      tick()
    } catch (e) {
      stop()
      setCamera('error')
      setCamError((e as Error)?.name === 'NotAllowedError' ? 'Camera permission was refused.' : 'No camera available here (it needs HTTPS and a camera).')
    }
  }

  const fromPhoto = async (f: File) => {
    setResult(null); setError('')
    stop()
    setBusy(true)
    try {
      const bmp = await createImageBitmap(f)
      const qr = await import('../lib/qr')
      const text = (await detect(makeDetector(), bmp)) || (await qr.decodeImage(bmp))
      setBusy(false)
      if (text) { await submit(text, 'photo'); return }
      setError('No QR code found in that photo. Take it closer, straight on, so the code fills most of the picture and is sharp.')
    } catch (e) { setError(errMsg(e)) } finally { setBusy(false) }
  }

  const r = result?.receipt
  const t = result?.transaction
  const dec = r ? decimalsOf(r.currency, currencies) : 2
  const writable = accounts.filter(a => !a.archived && a.role !== 'viewer')
  return (
    <Modal title="Scan a receipt" onClose={() => { stop(); onClose() }} className="scan-modal">
      <div className="scan">
        {!result && <>
          <div className={`scan-view ${camera}`}>
            <video ref={video} playsInline muted />
            {camera === 'on' && <div className="scan-frame" aria-hidden />}
            {camera !== 'on' && (
              <div className="scan-idle">
                {camera === 'starting' ? <Spinner size={22} /> : <Icon name="camera" size={30} />}
                <button type="button" className="btn primary" onClick={start} disabled={camera === 'starting' || busy}>{camera === 'error' ? 'Try the camera again' : 'Start camera'}</button>
                {camError && <span className="bad small">{camError}</span>}
              </div>
            )}
            {busy && <div className="scan-busy"><Spinner size={22} /> Reading the receipt…</div>}
            {camera === 'on' && slow && !busy && <div className="scan-tip">Hold the phone closer so the code fills the frame, and keep it still. Still nothing? Use <b>Take a photo</b>.</div>}
          </div>
          <div className="row wrap" style={{ gap: 8 }}>
            {camera === 'on' && <button type="button" className="btn" onClick={stop}>Stop camera</button>}
            <label className="btn"><Icon name="camera" size={15} />Take a photo<input type="file" accept="image/*" style={{ display: 'none' }} onChange={e => { const f = e.target.files?.[0]; if (f) fromPhoto(f); e.target.value = '' }} /></label>
            <span className="spacer" />
            <div style={{ minWidth: 200 }}><Select value={account} onChange={setAccount} options={writable.map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} placeholder="Account: automatic" clearLabel="Automatic (by payment)" ariaLabel="Account" /></div>
          </div>
          <form className="row" style={{ gap: 8 }} onSubmit={e => { e.preventDefault(); submit(link, 'web') }}>
            <input value={link} onChange={e => setLink(e.target.value)} placeholder="…or paste the receipt link (https://suf.poreskaupravars.org/v/?vl=…)" aria-label="Receipt link" />
            <button className="btn" disabled={!link.trim() || busy}>Read</button>
          </form>
          {error && <div className="bad small">{error}</div>}
          <p className="muted small">Hold the phone close so the QR code at the bottom of the receipt fills the frame — receipt codes are dense. On an iPhone, <b>Take a photo</b> is the most reliable. Pocket Money asks the tax office for the shop and every item, books the purchase and links it to a matching transaction you already typed.</p>
        </>}
        {result && (
          <div className={`scan-result ${result.status}`}>
            <div className="scan-result-head">
              <span className="big">{STATUS_ICON[result.status]}</span>
              <div>
                <b>{STATUS_TITLE[result.status]}</b>
                <div className="muted small">{result.message}</div>
              </div>
            </div>
            {r && (
              <div className="scan-sum">
                <div className="row"><b>{r.merchant?.display_name || 'Unknown shop'}</b><span className="spacer" /><b>{r.kind === 'refund' ? '+' : '−'}{fmtMoney(r.total, r.currency, dec)}</b></div>
                <div className="muted small">{[r.date, r.time, r.location, r.item_count ? `${r.item_count} items` : '', r.method].filter(Boolean).join(' · ')}</div>
                {r.items.length > 0 && (
                  <ul className="scan-items">
                    {r.items.slice(0, 6).map(it => <li key={it.id}><span>{it.group_icon || '·'} {it.product_name || it.name}</span><span className="muted">{fmtMoney(it.total, r.currency, dec)}</span></li>)}
                    {r.items.length > 6 && <li className="muted">+ {r.items.length - 6} more</li>}
                  </ul>
                )}
                {t && <div className="small">{t.category_icon || '•'} {t.category_name || 'No category'} · {t.account_name}</div>}
              </div>
            )}
            <div className="row wrap" style={{ gap: 8 }}>
              <button type="button" className="btn primary" onClick={() => { setResult(null); setLink(''); start() }}><Icon name="camera" size={15} />Scan the next one</button>
              {t && <Link className="btn" to={`/transactions/${t.id}`} onClick={() => { stop(); onClose() }}>Open the transaction</Link>}
              <span className="spacer" />
              <button type="button" className="btn ghost" onClick={() => { stop(); onClose() }}>Done</button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
