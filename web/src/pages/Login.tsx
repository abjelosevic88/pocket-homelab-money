import { useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { Field } from '../components/ui'
import { Icon } from '../components/icons'

export default function Login({ mode }: { mode: 'login' | 'setup' }) {
  const { status, user, refresh } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [form, setForm] = useState({ email: '', password: '', name: '' })
  const [err, setErr] = useState(() => new URLSearchParams(window.location.search).get('error') || '')
  const [busy, setBusy] = useState(false)

  const from = (loc.state as { from?: string } | null)?.from || '/'
  if (status && user) return <Navigate to={from} replace />
  if (status && mode === 'setup' && !status.needs_setup) return <Navigate to="/login" replace />
  if (status && mode === 'login' && status.needs_setup) return <Navigate to="/setup" replace />

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true); setErr('')
    try {
      await api.post(`${V1}/auth/${mode}`, form)
      await refresh()
      navigate(from, { replace: true })
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  const sso = mode === 'login' && !!status?.oidc
  // passwords can be switched off on the server: then Pocket Core (OIDC) is the only way in
  const passwords = mode === 'setup' || status?.password_login !== false
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm(f => ({ ...f, [k]: e.target.value }))
  const title = status?.title || 'Pocket Money'

  return (
    <div className="login-wrap">
      <div className="login-card">
        <div className="login-brand">
          <img src="/favicon.svg" alt="" width={44} height={44} />
          <h1>{title}</h1>
          <p className="login-tagline">{mode === 'setup' ? 'Welcome! Create the first account to get started.' : 'Accounts in any currency, quick entry, bills, budgets and goals.'}</p>
        </div>
        {sso && <>
          <a className="btn primary lg block" href={`${V1}/auth/oidc/start?next=${encodeURIComponent(from)}`}>
            <Icon name="key" size={16} />Sign in with {status?.oidc_name || 'Pocket Core'}
          </a>
          {passwords && <div className="login-or"><span>or with a password</span></div>}
        </>}
        {passwords && <form onSubmit={submit} className="stack">
          {mode === 'setup' && <Field label="Your name"><input value={form.name} onChange={set('name')} autoFocus autoComplete="name" /></Field>}
          <Field label="Email"><input type="email" value={form.email} onChange={set('email')} required autoFocus={mode === 'login' && !sso} autoComplete="username" /></Field>
          <Field label="Password" help={mode === 'setup' ? 'At least 8 characters' : undefined}>
            <input type="password" value={form.password} onChange={set('password')} required minLength={mode === 'setup' ? 8 : 1} autoComplete={mode === 'setup' ? 'new-password' : 'current-password'} />
          </Field>
          {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
          <button type="submit" className={`btn lg block ${sso ? '' : 'primary'}`} disabled={busy}>{busy ? 'Please wait…' : mode === 'setup' ? 'Create account' : 'Sign in'}</button>
        </form>}
        {!passwords && err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
        {!passwords && !sso && <p className="login-note">Password sign-in is switched off and no single sign-on is set up on this server.</p>}
        {mode === 'setup' && <p className="login-note">You become the admin. Add the household under Settings → People and share accounts with them.</p>}
      </div>
      {status?.version && <p className="login-version">Pocket Money v{status.version.replace(/^v/, '')}</p>}
    </div>
  )
}
