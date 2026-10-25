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

/** Reads a QR code from a canvas: the browser's BarcodeDetector when it has
 *  one (Chrome, Android), else jsQR (Safari on iPhone), loaded on demand. */
async function decodeCanvas(c: HTMLCanvasElement, detector: Detector | null): Promise<string | null> {
  if (detector) {
    try {
      const r = await detector.detect(c)
      if (r[0]?.rawValue) return r[0].rawValue
    } catch { /* fall through to jsQR */ }
  }
  const ctx = c.getContext('2d', { willReadFrequently: true })
  if (!ctx) return null
  const { default: jsQR } = await import('jsqr')
  const img = ctx.getImageData(0, 0, c.width, c.height)
  return jsQR(img.data, img.width, img.height, { inversionAttempts: 'dontInvert' })?.data || null
}

function makeDetector(): Detector | null {
  const BD = (window as unknown as { BarcodeDetector?: new (o: { formats: string[] }) => Detector }).BarcodeDetector
  try { return BD ? new BD({ formats: ['qr_code'] }) : null } catch { return null }
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

  const start = async () => {
    setResult(null); setError(''); setCamError('')
    setCamera('starting')
    try {
      const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment', width: { ideal: 1920 }, height: { ideal: 1080 } }, audio: false })
      stream.current = s
      const v = video.current!
      v.srcObject = s
      await v.play()
      setCamera('on')
      const detector = makeDetector()
      canvas.current = canvas.current || document.createElement('canvas')
      const tick = async () => {
        if (!stream.current || busyRef.current) return
        const c = canvas.current!
        const w = v.videoWidth, h = v.videoHeight
        if (w && h) {
          // receipt codes are dense: keep enough pixels, but not 4K
          const scale = Math.min(1, 1280 / Math.max(w, h))
          c.width = Math.round(w * scale); c.height = Math.round(h * scale)
          c.getContext('2d', { willReadFrequently: true })!.drawImage(v, 0, 0, c.width, c.height)
          const text = await decodeCanvas(c, detector)
          if (text && stream.current) {
            stop()
            navigator.vibrate?.(60)
            await submit(text, 'camera')
            return
          }
        }
        if (stream.current) setTimeout(tick, 180)
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
    setBusy(true)
    try {
      const bmp = await createImageBitmap(f)
      const c = document.createElement('canvas')
      const detector = makeDetector()
      // try the photo at a few sizes: jsQR likes ~1000–2000 px
      for (const max of [1600, 2400, 1000]) {
        const scale = Math.min(1, max / Math.max(bmp.width, bmp.height))
        c.width = Math.round(bmp.width * scale); c.height = Math.round(bmp.height * scale)
        c.getContext('2d', { willReadFrequently: true })!.drawImage(bmp, 0, 0, c.width, c.height)
        const text = await decodeCanvas(c, detector)
        if (text) { setBusy(false); await submit(text, 'photo'); return }
      }
      setError('No QR code found in that photo. Get closer so the code fills most of the picture.')
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
          </div>
          <div className="row wrap" style={{ gap: 8 }}>
            {camera === 'on' && <button type="button" className="btn" onClick={stop}>Stop camera</button>}
            <label className="btn"><Icon name="upload" size={15} />From a photo<input type="file" accept="image/*" style={{ display: 'none' }} onChange={e => { const f = e.target.files?.[0]; if (f) fromPhoto(f); e.target.value = '' }} /></label>
            <span className="spacer" />
            <div style={{ minWidth: 200 }}><Select value={account} onChange={setAccount} options={writable.map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} placeholder="Account: automatic" clearLabel="Automatic (by payment)" ariaLabel="Account" /></div>
          </div>
          <form className="row" style={{ gap: 8 }} onSubmit={e => { e.preventDefault(); submit(link, 'web') }}>
            <input value={link} onChange={e => setLink(e.target.value)} placeholder="…or paste the receipt link (https://suf.poreskaupravars.org/v/?vl=…)" aria-label="Receipt link" />
            <button className="btn" disabled={!link.trim() || busy}>Read</button>
          </form>
          {error && <div className="bad small">{error}</div>}
          <p className="muted small">Point the camera at the QR code at the bottom of the receipt. Pocket Money asks the tax office for the shop and every item, books the purchase and links it to a matching transaction you already typed.</p>
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
