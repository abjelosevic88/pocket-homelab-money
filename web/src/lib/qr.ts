// QR decoding for receipt codes, loaded on demand by the scanner.
//
// Fiscal receipt codes are dense (a ~800 character link, ~120×120 modules)
// and printed on thermal paper: jsQR gives up on them, ZXing (the C++
// reader, as WebAssembly) reads them. The .wasm file is served by Pocket
// Money itself, not a CDN.
import { prepareZXingModule, readBarcodes, type ReaderOptions } from 'zxing-wasm/reader'
import wasmUrl from 'zxing-wasm/reader/zxing_reader.wasm?url'

prepareZXingModule({
  overrides: { locateFile: (path: string, prefix: string) => (path.endsWith('.wasm') ? wasmUrl : prefix + path) },
})

const FAST: ReaderOptions = { formats: ['QRCode'], tryHarder: true, tryInvert: false, tryRotate: true, maxNumberOfSymbols: 4 }
const HARD: ReaderOptions = { ...FAST, tryInvert: true, tryDownscale: true, binarizer: 'LocalAverage' }

/** Every QR code in the picture (a receipt may also carry the shop's own). */
async function read(img: ImageData, opts: ReaderOptions): Promise<string[]> {
  const out = await readBarcodes(img, opts)
  return out.filter(r => r.isValid && r.text).map(r => r.text)
}

/** One camera frame: the middle of it at full resolution (where the code is
 *  aimed), and every few frames the whole frame. Pixels are never thrown
 *  away by downscaling: a receipt code needs ~4 px per module. */
export async function decodeFrame(c: HTMLCanvasElement, frame: number, wanted: (t: string) => boolean): Promise<string[]> {
  const ctx = c.getContext('2d', { willReadFrequently: true })
  if (!ctx) return []
  const { width: w, height: h } = c
  let seen: string[] = []
  if (frame % 3 !== 2) {
    const s = Math.round(Math.min(w, h) * 0.8)
    seen = await read(ctx.getImageData(Math.round((w - s) / 2), Math.round((h - s) / 2), s, s), FAST)
    if (seen.some(wanted)) return seen
  }
  return [...seen, ...await read(ctx.getImageData(0, 0, w, h), frame % 3 === 2 ? HARD : FAST)]
}

/** A photo (a full-resolution still is the most reliable way on an
 *  iPhone): the whole picture, then the middle, then smaller copies. */
export async function decodeImage(src: ImageBitmap, wanted: (t: string) => boolean): Promise<string[]> {
  const c = document.createElement('canvas')
  const ctx = c.getContext('2d', { willReadFrequently: true })!
  const tries: [number, number][] = [[1, 1], [1, 0.6], [0.5, 1], [1, 0.35]] // [scale, centre crop]
  const seen: string[] = []
  for (const [scale, part] of tries) {
    const sw = Math.round(src.width * part), sh = Math.round(src.height * part)
    c.width = Math.round(sw * scale); c.height = Math.round(sh * scale)
    ctx.drawImage(src, Math.round((src.width - sw) / 2), Math.round((src.height - sh) / 2), sw, sh, 0, 0, c.width, c.height)
    const found = await read(ctx.getImageData(0, 0, c.width, c.height), HARD)
    seen.push(...found)
    if (found.some(wanted)) return seen
  }
  return seen
}
