import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Icon } from './icons'

/** A small mark that explains itself: the tip shows on hover with a mouse,
 *  on tap on a phone and on keyboard focus. Rendered in a portal so a
 *  scrolling table cannot clip it. */
export function Hint({ tip, className = '', children }: { tip: string; className?: string; children: React.ReactNode }) {
  const ref = useRef<HTMLButtonElement>(null)
  const [at, setAt] = useState<DOMRect | null>(null)
  const show = () => { if (ref.current) setAt(ref.current.getBoundingClientRect()) }
  const hide = () => setAt(null)
  useEffect(() => {
    if (!at) return
    const off = () => setAt(null)
    window.addEventListener('scroll', off, true)
    window.addEventListener('resize', off)
    document.addEventListener('pointerdown', off)
    return () => { window.removeEventListener('scroll', off, true); window.removeEventListener('resize', off); document.removeEventListener('pointerdown', off) }
  }, [at])
  const left = at ? Math.min(Math.max(at.left + at.width / 2, 130), window.innerWidth - 130) : 0
  return (
    <>
      <button ref={ref} type="button" className={`hint-btn ${className}`} aria-label={tip}
        onPointerEnter={e => { if (e.pointerType === 'mouse') show() }}
        onPointerLeave={e => { if (e.pointerType === 'mouse') hide() }}
        onPointerDown={e => e.stopPropagation()}
        onFocus={e => { if (e.currentTarget.matches(':focus-visible')) show() }}
        onBlur={hide}
        onClick={e => { e.stopPropagation(); e.preventDefault(); if (at) hide(); else show() }}>
        {children}
      </button>
      {at && createPortal(
        <div className={`hint-pop ${at.top < 80 ? 'below' : ''}`} role="tooltip" style={{ left, top: at.top < 80 ? at.bottom + 8 : at.top - 8 }}>{tip}</div>,
        document.body,
      )}
    </>
  )
}

const TIPS: Record<string, Record<string, string>> = {
  ai: {
    category: 'Sorted by AI. Pick another category and it becomes your own choice.',
    group: 'Placed in this group by AI. Pick another group to make it your own, or go back to keywords.',
  },
  similar: {
    category: '',
    group: 'Placed next to similar products you already grouped.',
  },
}

/** Where a product's category or group came from, as one uniform mark:
 *  purple sparkles = AI, grey link = similar products. With `slot` an
 *  empty placeholder keeps columns aligned when there is nothing to say. */
export function SourceMark({ source, what, slot = false }: { source?: string; what: 'category' | 'group'; slot?: boolean }) {
  const tip = source ? TIPS[source]?.[what] : ''
  if (!tip) return slot ? <span className="mark-slot" aria-hidden /> : null
  return (
    <Hint tip={tip} className={`src-mark ${source}`}>
      <Icon name={source === 'ai' ? 'sparkles' : 'link'} size={13} strokeWidth={2} />
    </Hint>
  )
}

/** "keywords" / "similar product" / "AI" as uniform pills (reports). */
export function HowPill({ how }: { how: string }) {
  const icon = how === 'ai' ? 'sparkles' : how === 'similar' ? 'link' : 'tag'
  const label = how === 'ai' ? 'AI' : how === 'similar' ? 'Similar' : 'Keywords'
  return <span className={`how-pill ${how}`}><Icon name={icon} size={12} strokeWidth={2} />{label}</span>
}
