import { useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { Card, Field } from '../components/ui'

export default function Login({ mode }: { mode: 'login' | 'setup' }) {
  const { status, user, refresh } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [form, setForm] = useState({ email: '', password: '', name: '' })
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  if (status && user) return <Navigate to={(loc.state as { from?: string })?.from || '/'} replace />
  if (status && mode === 'setup' && !status.needs_setup) return <Navigate to="/login" replace />
  if (status && mode === 'login' && status.needs_setup) return <Navigate to="/setup" replace />

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true); setErr('')
    try { await api.post(`${V1}/auth/${mode}`, form); await refresh(); navigate('/') } catch (e: unknown) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm(f => ({ ...f, [k]: e.target.value }))

  return (
    <div className="login-wrap">
      <div className="login-card">
        <div className="row mb" style={{ justifyContent: 'center', gap: 10 }}><span className="logo" style={{ width: 36, height: 36, borderRadius: 10, background: 'var(--accent)', color: '#fff', display: 'grid', placeItems: 'center' }}><img src="/favicon.svg" alt="" /></span><h1>{status?.title || 'Pocket Money'}</h1></div>
        <Card title={mode === 'setup' ? 'Welcome! Create the first account' : 'Sign in'}>
          <form onSubmit={submit} className="grid" style={{ gap: 14 }}>
            {mode === 'setup' && <Field label="Your name"><input value={form.name} onChange={set('name')} autoFocus /></Field>}
            <Field label="Email"><input type="email" value={form.email} onChange={set('email')} required autoFocus={mode === 'login'} autoComplete="username" /></Field>
            <Field label="Password" help={mode === 'setup' ? 'At least 8 characters' : undefined}><input type="password" value={form.password} onChange={set('password')} required minLength={mode === 'setup' ? 8 : 1} autoComplete={mode === 'setup' ? 'new-password' : 'current-password'} /></Field>
            {err && <div className="callout danger">{err}</div>}
            <button className="btn primary lg" disabled={busy}>{busy ? 'Please wait…' : mode === 'setup' ? 'Create account' : 'Sign in'}</button>
          </form>
        </Card>
        {mode === 'setup' && <p className="muted small mt">You become the admin. Add the household under Settings → Users and share accounts with them.</p>}
        {status?.version && <p className="muted small right mt">v{status.version.replace(/^v/, '')}</p>}
      </div>
    </div>
  )
}
