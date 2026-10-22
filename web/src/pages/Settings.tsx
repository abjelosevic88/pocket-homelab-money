import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, ConnMode, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime } from '../lib/format'
import { Confirm, Field, Loading, Modal, Segmented, SettingRow, Switch, Tabs, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { CurrencySelect } from '../components/Money'
import { AccountSelect } from '../components/pickers'
import { Avatar } from '../components/people'
import { Categories, Rules, TagsAdmin, TemplatesAdmin } from './Manage'
import { ImportTab } from './Import'
import { Webhooks } from './Webhooks'

type TabId = 'general' | 'categories' | 'tags' | 'templates' | 'rules' | 'notifications' | 'ai' | 'apps' | 'import' | 'webhooks' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string; group: string }[] = [
  { id: 'general', label: 'General', icon: 'settings', group: 'Money', sub: 'Home currency, the default account, your month and week, date and time formats.' },
  { id: 'categories', label: 'Categories', icon: 'layers', group: 'Money', sub: 'Where the money goes. Sub-categories roll up into their parent in budgets and reports.' },
  { id: 'tags', label: 'Tags', icon: 'tag', group: 'Money', sub: 'Labels across categories — a trip, the car, a project.' },
  { id: 'templates', label: 'Templates', icon: 'zap', group: 'Money', sub: 'One-tap bookings, and prefixes quick entry understands.' },
  { id: 'rules', label: 'Rules', icon: 'filter', group: 'Money', sub: 'Pick a category and tags for new transactions by their text or amount.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', group: 'Connections', sub: 'Payment reminders, budget warnings and the weekly digest — through Apprise, ntfy or a webhook.' },
  { id: 'ai', label: 'AI', icon: 'sparkles', group: 'Connections', sub: 'Free text and voice into transactions, and Ask your money. Optional.' },
  { id: 'apps', label: 'Connected apps', icon: 'link', group: 'Connections', sub: 'Pocket Travel through Pocket Core or directly, and Firefly III for the import and mirroring.' },
  { id: 'import', label: 'Import', icon: 'download', group: 'Connections', sub: 'Bring everything over from Firefly III, or import a bank statement (CSV).' },
  { id: 'webhooks', label: 'Webhooks', icon: 'webhook', group: 'Connections', sub: 'Tell other apps the moment a transaction, a budget or a bill changes.' },
  { id: 'profile', label: 'Profile', icon: 'user', group: 'You', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', group: 'You', sub: 'Everyone who has their own books on this box.' },
  { id: 'tokens', label: 'API & apps', icon: 'key', group: 'You', sub: 'Personal tokens, and the Firefly III-compatible API for Raycast, the phone app and scripts.' },
  { id: 'data', label: 'Backup & data', icon: 'database', group: 'System', sub: 'Download the database, or export your own data as CSV or JSON.' },
  { id: 'about', label: 'About', icon: 'info', group: 'System', sub: 'Version, numbers and the server.' },
]
// the old ?tab= names
const ALIAS: Record<string, TabId> = { integrations: 'notifications', users: 'people', api: 'tokens' }

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const legacy = new URLSearchParams(window.location.search).get('tab') || ''
  const want = raw || legacy
  const tab: TabId = (TABS.find(t => t.id === (ALIAS[want] || want))?.id) || 'general'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  // /settings?tab=import → /settings/import
  useEffect(() => { if (!raw && legacy) navigate(`/settings/${tab}`, { replace: true }) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop') && !(e.target as HTMLElement).closest('input, textarea')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])

  let group = ''
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to Money" title="Back to Money (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => {
              const head = t.group !== group ? <div className="set-nav-h" key={'h' + t.group}>{(group = t.group)}</div> : null
              return (
                <React.Fragment key={t.id}>
                  {head}
                  <button type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                    <Icon name={t.icon} size={16} />{t.label}
                  </button>
                </React.Fragment>
              )
            })}
          </nav>
        )}
        <main className="set-content">
          <div key={tab} className={`set-inner ${WIDE.includes(tab) ? 'wide' : ''}`}>
            <div className="set-title">
              <h2>{current.label}</h2>
              <p>{current.sub}</p>
            </div>
            {tab === 'general' && <General />}
            {tab === 'categories' && <Categories />}
            {tab === 'tags' && <TagsAdmin />}
            {tab === 'templates' && <TemplatesAdmin />}
            {tab === 'rules' && <Rules />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'ai' && <AI />}
            {tab === 'apps' && <Integrations />}
            {tab === 'import' && <ImportTab />}
            {tab === 'webhooks' && <Webhooks />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}
// lists and tables that need the room
const WIDE: TabId[] = ['categories', 'tags', 'templates', 'rules', 'import', 'webhooks', 'tokens']

/** A settings card: title, explanation, actions on the right — Pocket Calendar's section. */
function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

export function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function General() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Money" sub="Every report, budget and net worth is in the home currency.">
        <SettingRow title="Home currency" help={isAdmin ? "Changing it re-values all transactions at their day's rate." : 'Set by an admin for everyone on this box.'}>
          <div style={{ width: 200 }}><CurrencySelect value={f.home_currency} onChange={c => setF({ ...f, home_currency: c })} disabled={!isAdmin} /></div>
        </SettingRow>
        <SettingRow title="Default account" help="Quick entry books here unless you say otherwise (@cash). Empty = the one used most recently.">
          <div style={{ width: 200 }}><AccountSelect value={f.default_account_id || null} onChange={id => setF({ ...f, default_account_id: id })} clearLabel="Most recently used" /></div>
        </SettingRow>
        <SettingRow title="Month starts on day" help="Payday month: 25 = budgets and reports run from the 25th to the 24th.">
          <input type="number" min={1} max={28} value={f.month_start_day} onChange={e => setF({ ...f, month_start_day: Math.min(28, Math.max(1, Number(e.target.value))) })} style={{ width: 90 }} />
        </SettingRow>
        <SettingRow title="Week starts on"><Segmented size="sm" value={f.week_start} onChange={v => setF({ ...f, week_start: v })} options={[{ value: 1, label: 'Monday' }, { value: 0, label: 'Sunday' }]} /></SettingRow>
      </Section>
      <Section title="Date & time">
        <SettingRow title="Date format"><div style={{ width: 200 }}><Select value={f.date_format} onChange={v => setF({ ...f, date_format: v })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></div></SettingRow>
        <SettingRow title="Time format"><div style={{ width: 200 }}><Select value={f.time_format} onChange={v => setF({ ...f, time_format: v })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></div></SettingRow>
      </Section>
      <Section title="This box" sub={isAdmin ? 'These apply to everyone on this box.' : 'Set by an admin for everyone.'}>
        <SettingRow title="App title" help="Shown in the sidebar and the browser tab."><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} disabled={!isAdmin} style={{ width: 220 }} /></SettingRow>
        <SettingRow title="Look exchange rates up automatically" help="ECB via Frankfurter, open.er-api.com; euro pegs like BAM are fixed.">
          <Switch checked={f.fx_auto} onChange={v => setF({ ...f, fx_auto: v })} disabled={!isAdmin} label="Look exchange rates up automatically" />
        </SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function useIntegrationTest() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `ok:${r.message}` : `err:${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `err:${(e as Error).message}` })) }
  }
  const status = (which: string) => {
    const v = test[which]
    if (!v) return null
    const ok = v.startsWith('ok:'), bad = v.startsWith('err:')
    return <span className={`small row ${ok ? 'ok' : bad ? 'bad' : 'muted'}`} style={{ gap: 4 }}>{ok ? <Icon name="check" size={14} /> : bad ? <Icon name="x" size={14} /> : null}{ok || bad ? v.slice(v.indexOf(':') + 1) : v}</span>
  }
  return { run, status }
}
const badge = (on: boolean) => <span className={`badge ${on ? 'ok' : ''}`}>{on ? 'connected' : 'not set'}</span>

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { toast } = useToast()
  const { run, status } = useIntegrationTest()
  if (!f) return <Loading />
  const preview = async () => {
    try { const r = await api.post<{ messages: { title: string; body: string }[] }>(`${V1}/notify/run?dry=1&digest=1`); toast(r.messages.length ? r.messages.map(m => m.title).join(' · ') : 'Nothing to send right now') } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <div className="stack loose">
      <Section title="Where to send" icon="bell" actions={badge(f.notify_configured)} sub="Payment reminders (each subscription says how many days ahead), budget warnings at your alert % and when over, and a weekly digest on Monday. Works with an Apprise gateway, ntfy or any JSON webhook.">
        <div className="stack">
          <Field label="URL"><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/money" /></Field>
          <SettingRow title="Format"><Segmented size="sm" value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} /></SettingRow>
          <SettingRow title="Send at" help="Hour of the day, 0–23."><input type="number" min={0} max={23} value={f.notify_hour} onChange={e => setF({ ...f, notify_hour: Number(e.target.value) })} style={{ width: 90 }} /></SettingRow>
          <SettingRow title="Weekly digest" help="On Monday: last week, what is due and how the budgets stand."><Switch checked={f.notify_digest} onChange={v => setF({ ...f, notify_digest: v })} label="Weekly digest" /></SettingRow>
          <div className="row wrap"><button type="button" className="btn sm" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}>Send a test</button><button type="button" className="btn sm" onClick={preview}>What would go out now?</button>{status('notify')}</div>
        </div>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function AI() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useIntegrationTest()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="AI provider" icon="sparkles" actions={badge(f.ai_configured)} sub="Turns free text and voice notes into transactions (several at once, in Bosnian or English), and answers questions about your money under Reports → Ask. Pocket AI is preferred when connected through Core.">
        <div className="stack">
          <Field label="Text generation" help="Automatic prefers your enabled Pocket AI connection. Without one, it uses your saved OpenAI settings."><select value={f.ai_provider || 'auto'} onChange={e => setF({ ...f, ai_provider: e.target.value })}><option value="auto">Automatic — prefer Pocket AI</option><option value="pocket">Pocket AI</option><option value="openai">OpenAI / compatible API</option></select></Field>
          <p className="muted small" role="status">Saved selection: {f.ai_status?.detail}. Pocket AI uses your Claude account and model from Pocket AI settings.</p>
          <p className="muted small">OpenAI settings below apply when OpenAI is selected and are required separately for dictation. Pocket AI does not transcribe audio.</p>
          <Field label="OpenAI API key"><input value={f.ai_key} onChange={e => setF({ ...f, ai_key: e.target.value })} autoComplete="off" /></Field>
          <div className="two-col tight">
            <Field label="Model" help={`Empty = ${f.ai_model_effective}`}><input value={f.ai_model} onChange={e => setF({ ...f, ai_model: e.target.value })} placeholder={f.ai_model_effective} /></Field>
            <Field label="Base URL" help="Empty = api.openai.com"><input value={f.ai_url} onChange={e => setF({ ...f, ai_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
          </div>
          <div className="two-col tight">
            <Field label="Dictation language" help="ISO code (bs, hr, sr, en); empty = detect"><input value={f.ai_language} onChange={e => setF({ ...f, ai_language: e.target.value })} /></Field>
            <Field label="Transcription model"><input value={f.ai_transcribe_model} onChange={e => setF({ ...f, ai_transcribe_model: e.target.value })} placeholder="gpt-4o-mini-transcribe" /></Field>
          </div>
          <Field label="Words the AI should know" help='e.g. "maraka = BAM; sa unicredit = account UniCredit; selo = trips to the village"'><textarea value={f.ai_context} onChange={e => setF({ ...f, ai_context: e.target.value })} rows={2} /></Field>
          <div className="row wrap"><button type="button" className="btn sm" onClick={() => run('ai', f.ai_url, f.ai_key, { model: f.ai_model })} disabled={!f.ai_key}>Test key</button>{status('ai')}</div>
        </div>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function Integrations() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useIntegrationTest()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Pocket Travel" icon="plane" actions={connBadge(f.travel_mode, f.travel_via)} sub="While a trip is running, new expenses get the trip's tag automatically — Pocket Travel then shows them in the trip budget.">
        <Connection app="Pocket Travel" mode={f.travel_mode} via={f.travel_via} detail={f.travel_detail} core={f.core_configured} hasToken={!!f.travel_token}
          onMode={m => save({ travel_mode: m })} onDisconnect={() => save({ travel_url: '', travel_token: '', travel_public_url: '' })}
          test={<><button type="button" className="btn sm" onClick={() => run('travel', f.travel_url, f.travel_token)}>Test connection</button>{status('travel')}</>}
          direct={<>
            <div className="two-col tight">
              <Field label="URL" help="Empty = found through Pocket Core"><input value={f.travel_url} onChange={e => setF({ ...f, travel_url: e.target.value })} placeholder="http://pocket-travel:8080" /></Field>
              <Field label="API token" help="Pocket Travel → Settings → API tokens"><input value={f.travel_token} onChange={e => setF({ ...f, travel_token: e.target.value })} autoComplete="off" /></Field>
            </div>
            <Field label="Public URL (optional)" help="What the browser opens for trip links when the URL above is internal."><input value={f.travel_public_url} onChange={e => setF({ ...f, travel_public_url: e.target.value })} placeholder="https://travel.example.com" /></Field>
          </>} />
        {f.travel_mode !== 'off' && <div className="mt-sm"><SettingRow title="Tag new expenses with the running trip"><Switch checked={f.travel_auto_tag} onChange={v => setF({ ...f, travel_auto_tag: v })} label="Tag new expenses with the running trip" /></SettingRow></div>}
      </Section>
      <Section title="Firefly III" icon="database" actions={badge(f.firefly_configured)} sub="Only needed to import (Settings → Import) or to keep mirroring while both run side by side.">
        <div className="stack">
          <div className="two-col tight">
            <Field label="URL"><input value={f.firefly_url} onChange={e => setF({ ...f, firefly_url: e.target.value })} placeholder="http://host.docker.internal:8083" /></Field>
            <Field label="Personal access token"><input value={f.firefly_token} onChange={e => setF({ ...f, firefly_token: e.target.value })} autoComplete="off" /></Field>
          </div>
          <Field label="Firefly Pico URL (for templates)"><input value={f.pico_url} onChange={e => setF({ ...f, pico_url: e.target.value })} placeholder="http://host.docker.internal:6977" /></Field>
          <div className="row wrap"><button type="button" className="btn sm" onClick={() => run('firefly', f.firefly_url, f.firefly_token)} disabled={!f.firefly_url}>Test Firefly</button>{status('firefly')}<button type="button" className="btn sm" onClick={() => run('pico', f.pico_url, '')} disabled={!f.pico_url}>Test Pico</button>{status('pico')}</div>
        </div>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

/**
 * One Pocket app connection: Pocket Core, Direct (URL + token) or Off, with
 * what it actually is right now. The mode saves at once; Disconnect removes
 * the saved URL and token. With Pocket Core on the box the switch is
 * Pocket Core | Off and Direct is an advanced option behind a link (always
 * shown while it is the mode in use); without Core, Direct is the only way
 * in and shows as usual.
 */
function Connection({ app, mode, via, detail, core, onMode, onDisconnect, hasToken, test, direct }: {
  app: string; mode: ConnMode; via: string; detail: string; core: boolean; onMode: (m: ConnMode) => void; onDisconnect: () => void
  hasToken: boolean; test: React.ReactNode; direct: React.ReactNode
}) {
  const ok = via !== ''
  const [advanced, setAdvanced] = useState(false)
  const showDirect = !core || mode === 'direct' || advanced
  const line = mode === 'off' ? `Off: Money does not talk to ${app}.`
    : mode === 'core' ? (ok ? `Connected through Pocket Core, as you. Nothing to fill in; switch it off for yourself in Pocket Core → Settings → Apps acting for me.` : `Not connected: ${detail}.`)
    : (ok ? `Direct connection with your own ${app} URL and token; use Test connection to check it.` : `Not connected: ${detail || 'add the URL and a token'}.`)
  return (
    <div className="conn-block">
      <div className="conn-mode">
        <Segmented size="sm" value={mode} onChange={m => onMode(m)} options={[
          { value: 'core' as ConnMode, label: 'Pocket Core', title: core ? 'Through Pocket Core: per person, nothing to paste' : 'There is no Pocket Core on this box' },
          ...(showDirect ? [{ value: 'direct' as ConnMode, label: 'Direct', title: 'Your own URL and API token' }] : []),
          { value: 'off' as ConnMode, label: 'Off' },
        ]} />
        {!showDirect && <button type="button" className="conn-advanced" onClick={() => setAdvanced(true)}>Advanced: connect directly with a URL and key</button>}
      </div>
      <div className={`conn-status ${mode === 'off' ? '' : ok ? 'ok' : 'bad'}`}><span className="dot" />{line}</div>
      {mode === 'direct' && <>
        {direct}
        <div className="row wrap">{test}{hasToken && <button type="button" className="btn sm ghost danger-text" onClick={onDisconnect} title="Remove the saved URL and token">Disconnect</button>}</div>
      </>}
      {mode === 'core' && <div className="row wrap">{test}</div>}
    </div>
  )
}

const connBadge = (mode: ConnMode, via: string) => mode === 'off' ? <span className="badge">off</span>
  : via ? <span className="badge cat">{via === 'core' ? 'Pocket Core' : 'direct'}</span>
  : <span className="badge warn">not connected</span>

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadMeta(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title="Household" icon="users" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add a person</button>}>
      <p className="muted">Everyone has their own accounts, budgets, subscriptions and goals. Share an account from its page to let someone see it or book on it (a joint account, the household cash); whoever books on it picks from the owner's categories. Categories, tags, templates, rules, webhooks, settings and integrations are each person's own — nobody, admins included, sees anyone else's; a template its owner switches to Share can be used by whoever may book on its account, and a category hidden from sharing stays out of the shared list.</p>
      <table className="table compact">
        <thead><tr><th /><th>Name</th><th>Email</th><th>Role</th><th /></tr></thead>
        <tbody>
          {users.map(u => (
            <tr key={u.id}>
              <td style={{ width: 32 }}><Avatar user={u} /></td>
              <td>{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge" style={{ marginLeft: 6 }}>you</span>}</td>
              <td className="muted">{u.email}</td>
              <td><span className={`badge ${u.role === 'admin' ? 'cat' : ''}`}>{u.role}</span></td>
              <td className="right">{isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit.</p>}
      {status?.oidc && <p className="muted small mt">People sign in with {status.oidc_name} using the email address set here{status.password_login === false ? ' — passwords are switched off' : ''}.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local' && status?.password_login !== false} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm title={`Remove ${del.name || del.email}?`} message="Their account and the money accounts they own (with all transactions) are deleted." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'User saved' : 'User added'); onSaved() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit user' : 'Add user'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add user'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Section title="You" icon="user">
      <form onSubmit={save} className="grid" style={{ gap: 14, maxWidth: 480 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        {status?.auth_mode === 'local' && <>
          <hr />
          <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
          <Field label="New password" help="At least 8 characters. Leave empty to keep the current one."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
        </>}
        <div><button className="btn primary" disabled={busy}>Save</button></div>
      </form>
    </Section>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="API tokens" icon="key">
        <p className="muted">Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you: the native API (<code>/api/v1/…</code>, see <code>docs/api.md</code>), the Firefly-compatible API, a Homepage widget (<code>GET /api/v1/dashboard</code>) and the payments calendar (<code>/cal/subscriptions.ics?token=…</code>).</p>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Raycast)" /><button className="btn primary">Create</button></form>
        {fresh && <div className="callout success mb"><b>Copy it now — it is shown once.</b><pre className="mono" style={{ margin: '8px 0 0' }}>{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Everything that spoke to Firefly III" icon="code">
        <p className="muted">Pocket Money answers the Firefly III API at <code>{base}/firefly/api/v1/…</code> — accounts, categories, tags (with <code>/tags/&lt;tag&gt;/transactions</code>), transactions (list, read, create, update, delete), bills with pay dates, currencies, exchange rates and <code>insight/expense/total</code> — plus Firefly Pico's <code>{base}/firefly/api/transaction-templates</code>.</p>
        <p className="muted">So the <b>Raycast</b> extension, the <b>Pocket Homelab</b> phone app, <b>Pocket Garage</b>, <b>Pocket Travel</b>, <b>Pocket Day</b> and scripts keep working: set their Firefly URL (and Pico URL) to <code>{base}/firefly</code> and use a token from above as the personal access token.</p>
      </Section>
    </div>
  )
}

function Data() {
  const { user } = useApp()
  return (
    <div className="stack loose">
      {user?.role === 'admin' && <Section title="Backup" icon="database">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>). Receipts live next to it under <code>files/</code>. Restore by stopping the app and replacing <code>pocket-money.db</code> in the data directory. The CLI does the same: <code>pocket-money backup</code>.</p>
        <p className="muted small">The database holds everyone's money, so the download only works while you are the only person here; otherwise back the server up from the host and use Export for your own data.</p>
        <a className="btn primary" href={`${V1}/backup`}>Download database</a>
      </Section>}
      <Section title="Export" icon="download">
        <p className="muted">Everything you can see as CSV (one row per transaction, with the home-currency value) or as one JSON file with accounts, categories, tags, transactions, subscriptions, budgets, goals, templates and rules.</p>
        <div className="row"><a className="btn" href={`${V1}/export/transactions.csv`}>Transactions CSV</a><a className="btn" href={`${V1}/export/money.json`}>Everything (JSON)</a></div>
      </Section>
    </div>
  )
}

function About() {
  const { status } = useApp()
  const { data } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const counts = (data?.counts || {}) as Record<string, number>
  return (
    <Section title="Pocket Money" icon="wallet">
      <p><b>Pocket Money</b> is personal finance for a homelab: accounts in any currency, quick entry that understands “kafa 3,5” and “20 eur juče”, one-tap templates, subscriptions that remind you or book themselves, budgets with a pace line, savings goals, a forecast of what is safe to spend, and reports — with a Firefly III-compatible API so the rest of the homelab keeps working. One binary, one SQLite file, one volume.</p>
      <p className="muted small">Open source (MIT) — <a href="https://github.com/abjelosevic88/pocket-homelab-money" target="_blank" rel="noreferrer">github.com/abjelosevic88/pocket-homelab-money</a>. Sibling projects: Pocket Habits, Inventory, Bookshelf, Split, Clips, Subtitles, Invoicing, Travel, Garage, Kitchen and Day.</p>
      {data && (
        <table className="table compact mt">
          <tbody>
            <tr><th>Version</th><td className="mono">{String(data.version)}</td></tr>
            <tr><th>Runtime</th><td className="mono">{String(data.go)} · {String(data.os)}/{String(data.arch)}</td></tr>
            <tr><th>Auth mode</th><td className="mono">{String(data.auth_mode)}</td></tr>
            <tr><th>Sign-in</th><td>{[status?.password_login !== false && 'password', status?.oidc && status.oidc_name].filter(Boolean).join(' + ')}{' · API tokens'}</td></tr>
            {data.db_path !== undefined && <tr><th>Database</th><td className="mono">{String(data.db_path)} · {fmtBytes(Number(data.db_bytes))} · receipts {fmtBytes(Number(data.files_bytes))}</td></tr>}
            <tr><th>Timezone</th><td className="mono">{String(data.timezone)}</td></tr>
            <tr><th>Uptime</th><td className="mono">{String(data.uptime)}</td></tr>
            <tr><th>Your rows</th><td className="mono">{counts.accounts} accounts · {counts.transactions} transactions · {counts.categories} categories · {counts.tags} tags · {counts.subscriptions} subscriptions · {counts.budgets} budgets · {counts.goals} goals · {counts.templates} templates · {counts.rules} rules</td></tr>
          </tbody>
        </table>
      )}
    </Section>
  )
}
