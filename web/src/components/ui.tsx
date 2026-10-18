import React, { createContext, useCallback, useContext, useEffect, useState } from 'react'

type Toast = { id: number; msg: string; kind: 'ok' | 'err' }
const ToastCtx = createContext<{ toast: (msg: string, kind?: 'ok' | 'err') => void }>({ toast: () => {} })
export function useToast() { return useContext(ToastCtx) }
export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<Toast[]>([])
  const toast = useCallback((msg: string, kind: 'ok' | 'err' = 'ok') => {
    const id = Date.now() + Math.random()
    setItems(t => [...t, { id, msg, kind }])
    setTimeout(() => setItems(t => t.filter(x => x.id !== id)), kind === 'err' ? 6000 : 2500)
  }, [])
  return (
    <ToastCtx.Provider value={{ toast }}>
      {children}
      <div className="toasts">{items.map(t => <div key={t.id} className={`toast ${t.kind}`}>{t.msg}</div>)}</div>
    </ToastCtx.Provider>
  )
}

export function PageHeader({ title, sub, actions }: { title: React.ReactNode; sub?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="page-header">
      <div><h1>{title}</h1>{sub && <div className="sub">{sub}</div>}</div>
      {actions && <div className="actions">{actions}</div>}
    </div>
  )
}

export function Card({ title, actions, children, flush, className = '' }: { title?: React.ReactNode; actions?: React.ReactNode; children: React.ReactNode; flush?: boolean; className?: string }) {
  return (
    <section className={`card ${className}`}>
      {(title || actions) && <div className="card-head"><h2>{title}</h2>{actions && <div className="row">{actions}</div>}</div>}
      <div className={`card-body ${flush ? 'flush' : ''}`}>{children}</div>
    </section>
  )
}

/** A labelled form control. Use group for button groups and star inputs: a
 *  <label> would forward clicks on its text to the first button inside. */
export function Field({ label, help, children, className = '', group = false }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; className?: string; group?: boolean }) {
  const Tag = group ? 'div' : 'label'
  return <Tag className={`field ${className}`}><span className="label">{label}</span>{children}{help && <span className="help">{help}</span>}</Tag>
}

export function Loading() { return <div className="loading"><span className="spinner" /> Loading…</div> }

export function Empty({ title, children }: { title: string; children?: React.ReactNode }) {
  return <div className="empty"><h3>{title}</h3>{children && <p className="muted">{children}</p>}</div>
}

export function Modal({ title, onClose, children, size = '' }: { title: React.ReactNode; onClose: () => void; children: React.ReactNode; size?: '' | 'lg' }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className={`modal ${size}`} role="dialog" aria-modal="true">
        <div className="modal-head"><h2>{title}</h2><button className="btn ghost icon" onClick={onClose} aria-label="Close">✕</button></div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  )
}

export function Confirm({ title, message, onConfirm, onCancel, danger = true }: { title: string; message: React.ReactNode; onConfirm: () => void; onCancel: () => void; danger?: boolean }) {
  return (
    <Modal title={title} onClose={onCancel}>
      <p>{message}</p>
      <div className="form-actions">
        <button className="btn" onClick={onCancel}>Cancel</button>
        <button className={`btn ${danger ? 'danger' : 'primary'}`} onClick={onConfirm}>Confirm</button>
      </div>
    </Modal>
  )
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { id: T; label: string }[]; value: T; onChange: (t: T) => void }) {
  return <div className="tabs">{tabs.map(t => <button type="button" key={t.id} className={t.id === value ? 'active' : ''} onClick={() => onChange(t.id)}>{t.label}</button>)}</div>
}

export function Segmented<T extends string | number>({ options, value, onChange, size = '' }: { options: { value: T; label: React.ReactNode; title?: string }[]; value: T | null; onChange: (v: T) => void; size?: '' | 'sm' | 'lg' }) {
  return (
    <div className={`segmented ${size}`} role="radiogroup">
      {options.map(o => <button type="button" key={String(o.value)} role="radio" aria-checked={o.value === value} title={o.title} className={o.value === value ? 'active' : ''} onClick={() => onChange(o.value)}>{o.label}</button>)}
    </div>
  )
}

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const run = useCallback(() => {
    setLoading(true)
    return fn().then(d => { setData(d); setError('') }).catch(e => setError((e as Error).message)).finally(() => setLoading(false))
  }, deps) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { run() }, [run])
  return { data, setData, error, loading, reload: run }
}

export function useDebounce<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value)
  useEffect(() => { const t = setTimeout(() => setV(value), ms); return () => clearTimeout(t) }, [value, ms])
  return v
}

/** Props that make a non-button element (a table row, a list line) clickable with mouse and keyboard. */
export function clickRow(onClick: () => void) {
  return {
    onClick, tabIndex: 0, role: 'button' as const,
    onKeyDown: (e: React.KeyboardEvent) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick() } },
  }
}

/** Spread on a cell holding its own buttons so they don't also trigger the clickable row. */
export const ownClicks = {
  onClick: (e: React.MouseEvent) => e.stopPropagation(),
  onKeyDown: (e: React.KeyboardEvent) => e.stopPropagation(),
}
