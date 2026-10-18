import { useState } from 'react'
import { useApp } from '../lib/app-context'
import { fmtDate, fmtMoney } from '../lib/format'
import { SERIES } from '../lib/types'

function useColor() { const { theme } = useApp(); return (k: string) => (SERIES[k] || SERIES.a)[theme] }

/** Income vs expense per period: two thin bars side by side, net as a dot. */
export function FlowBars({ data, currency, decimals = 2, height = 190, onPick }: { data: { label: string; income: number; expense: number; net: number }[]; currency: string; decimals?: number; height?: number; onPick?: (i: number) => void }) {
  const color = useColor()
  const [hover, setHover] = useState<number | null>(null)
  if (data.length === 0) return <p className="muted small">Nothing yet.</p>
  const max = Math.max(1, ...data.map(d => Math.max(d.income, d.expense)))
  const W = 100, H = height, padT = 6, padB = 2
  const n = data.length, slot = W / n, bw = Math.min(slot * 0.32, 4.2)
  const y = (v: number) => padT + (H - padT - padB) * (1 - v / max)
  const h = hover == null ? null : data[hover]
  return (
    <div className="chart">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="chart-svg" style={{ height }} role="img" aria-label="Income and expenses per month">
        {[0.5, 1].map(f => <line key={f} x1="0" x2={W} y1={y(max * f)} y2={y(max * f)} className="grid-line" />)}
        {data.map((d, i) => {
          const x = i * slot + slot / 2
          const dim = hover != null && hover !== i ? 0.45 : 1
          return (
            <g key={i} onMouseEnter={() => setHover(i)} onMouseLeave={() => setHover(null)} onTouchStart={() => setHover(i)} onClick={() => onPick?.(i)} style={{ cursor: onPick ? 'pointer' : undefined }}>
              <rect x={i * slot} y={0} width={slot} height={H} fill="transparent" />
              <rect x={x - bw - 0.3} y={y(d.income)} width={bw} height={Math.max(0, H - padB - y(d.income))} fill={color('income')} opacity={dim} rx={0.8} />
              <rect x={x + 0.3} y={y(d.expense)} width={bw} height={Math.max(0, H - padB - y(d.expense))} fill={color('expense')} opacity={dim} rx={0.8} />
            </g>
          )
        })}
      </svg>
      <div className="chart-axis" style={{ gridTemplateColumns: `repeat(${n}, minmax(0, 1fr))` }}>{data.map((d, i) => <span key={i}>{(n <= 12 || i % Math.ceil(n / 12) === 0) ? d.label.replace(/ 20(\d\d)$/, ' ’$1') : ''}</span>)}</div>
      <div className="chart-tip">{h ? <><b>{h.label}</b><span><span className="dot" style={{ background: color('income') }} />in {fmtMoney(h.income, currency, decimals)}</span><span><span className="dot" style={{ background: color('expense') }} />out {fmtMoney(h.expense, currency, decimals)}</span><span className={h.net >= 0 ? 'ok' : 'bad'}>net {fmtMoney(h.net, currency, decimals, { sign: true })}</span></> : <span className="legend" style={{ margin: 0 }}><span><span className="dot" style={{ background: color('income') }} />Income</span><span><span className="dot" style={{ background: color('expense') }} />Expenses</span></span>}</div>
    </div>
  )
}

export interface Pt { x: string; y: number; label?: string }

/** One-series line (area optional) with a hover crosshair. */
export function Line({ points, fmt, height = 160, color, area = false, zero = false, xFmt = (x: string) => fmtDate(x) }: { points: Pt[]; fmt: (y: number) => string; height?: number; color?: string; area?: boolean; zero?: boolean; xFmt?: (x: string) => string }) {
  const c = useColor()(color || 'net')
  const [hover, setHover] = useState<number | null>(null)
  if (points.length < 2) return <p className="muted small">Not enough data yet.</p>
  const W = 100, H = height, padT = 8, padB = 4, padX = 1
  const ys = points.map(p => p.y)
  let lo = Math.min(...ys), hi = Math.max(...ys)
  if (zero) { lo = Math.min(lo, 0); hi = Math.max(hi, 0) }
  if (hi === lo) { hi += 1; lo -= 1 }
  const pad = (hi - lo) * 0.08
  lo -= pad; hi += pad
  const x = (i: number) => padX + (W - 2 * padX) * (i / (points.length - 1))
  const y = (v: number) => padT + (H - padT - padB) * (1 - (v - lo) / (hi - lo))
  const d = points.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(2)},${y(p.y).toFixed(2)}`).join(' ')
  const h = hover == null ? null : points[hover]
  const onMove = (e: React.MouseEvent<SVGSVGElement> | React.TouchEvent<SVGSVGElement>) => {
    const r = (e.currentTarget as SVGSVGElement).getBoundingClientRect()
    const cx = 'touches' in e ? e.touches[0].clientX : e.clientX
    const i = Math.round(((cx - r.left) / r.width) * (points.length - 1))
    setHover(Math.max(0, Math.min(points.length - 1, i)))
  }
  return (
    <div className="chart">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="chart-svg" style={{ height }} onMouseMove={onMove} onTouchMove={onMove} onMouseLeave={() => setHover(null)} role="img">
        {lo < 0 && hi > 0 && <line x1="0" x2={W} y1={y(0)} y2={y(0)} className="grid-line dashed" />}
        {area && <path d={`${d} L${x(points.length - 1)},${H - padB} L${x(0)},${H - padB} Z`} fill={c} opacity={0.12} />}
        <path d={d} fill="none" stroke={c} strokeWidth="1.8" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
        {hover != null && <>
          <line x1={x(hover)} x2={x(hover)} y1={padT} y2={H - padB} className="grid-line" />
          <circle cx={x(hover)} cy={y(points[hover].y)} r="1.6" fill={c} className="pt-ring" vectorEffect="non-scaling-stroke" />
        </>}
      </svg>
      <div className="chart-axis two"><span>{xFmt(points[0].x)}</span><span>{xFmt(points[points.length - 1].x)}</span></div>
      <div className="chart-tip">{h ? <><b>{xFmt(h.x)}</b> · {fmt(h.y)}{h.label && <span className="muted"> {h.label}</span>}</> : <span className="muted">now {fmt(points[points.length - 1].y)} · hover for values</span>}</div>
    </div>
  )
}

/** Daily spend columns (a month at a glance). */
export function DayBars({ days, currency, decimals = 2, height = 70 }: { days: { date: string; amount: number }[]; currency: string; decimals?: number; height?: number }) {
  const c = useColor()('expense')
  const max = Math.max(1, ...days.map(d => d.amount))
  return (
    <div className="spark-bars" style={{ display: 'flex', alignItems: 'flex-end', gap: 2, height }}>
      {days.map(d => <span key={d.date} title={`${fmtDate(d.date)} · ${fmtMoney(d.amount, currency, decimals)}`} style={{ flex: 1, minHeight: 2, height: `${(d.amount / max) * 100}%`, background: c, opacity: d.amount ? 0.85 : 0.15, borderRadius: '2px 2px 0 0' }} />)}
    </div>
  )
}

/** Horizontal bars, longest first; an optional thin comparison bar under each. */
export function HBars({ rows, currency, decimals = 2, max: maxRows = 10, onPick }: { rows: { key: string | number; label: string; icon?: string; value: number; compare?: number }[]; currency: string; decimals?: number; max?: number; onPick?: (key: string | number) => void }) {
  const c = useColor()
  const sorted = [...rows].filter(r => r.value > 0).sort((a, b) => b.value - a.value).slice(0, maxRows)
  const max = Math.max(1, ...sorted.map(r => Math.max(r.value, r.compare || 0)))
  if (sorted.length === 0) return <p className="muted small">Nothing yet.</p>
  return (
    <div className="hbars">
      {sorted.map(r => (
        <div key={r.key} className={`hbar ${onPick ? 'hov' : ''}`} {...(onPick ? { onClick: () => onPick(r.key), tabIndex: 0, role: 'button', onKeyDown: (e: React.KeyboardEvent) => { if (e.key === 'Enter') onPick(r.key) } } : {})}>
          <span className="lbl">{r.icon} {r.label}</span>
          <span style={{ display: 'grid', gap: 2 }}>
            <span className="track"><span className="fill" style={{ width: `${r.value / max * 100}%`, background: c('expense') }} /></span>
            {r.compare != null && r.compare > 0 && <span className="track" style={{ height: 4 }}><span className="fill" style={{ width: `${r.compare / max * 100}%`, background: 'var(--border)' }} /></span>}
          </span>
          <span className="val">{fmtMoney(r.value, currency, decimals, { compact: true })}</span>
        </div>
      ))}
    </div>
  )
}

/** Progress ring for goals. */
export function Ring({ pct, icon, size = 74 }: { pct: number; icon: string; size?: number }) {
  const r = 30, C = 2 * Math.PI * r
  return (
    <div className="ring" style={{ width: size, height: size }}>
      <svg width={size} height={size} viewBox="0 0 74 74">
        <circle cx="37" cy="37" r={r} fill="none" stroke="var(--surface-2)" strokeWidth="7" />
        <circle cx="37" cy="37" r={r} fill="none" stroke="var(--accent)" strokeWidth="7" strokeLinecap="round" strokeDasharray={`${C * Math.min(1, pct / 100)} ${C}`} />
      </svg>
      <span className="c">{icon}</span>
      <span className="p">{Math.round(pct)}%</span>
    </div>
  )
}
