// Left column: add button, sections, the accounts, settings — the Pocket Calendar sidebar.
import { useEffect, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney } from '../lib/format'
import { KIND_ICON, KIND_LABEL, type Account, type Subscription } from '../lib/types'
import { Icon } from './icons'
import type { CreateKind } from './Layout'
import { type Anchor, type MenuItem, Menu, rectOf } from './ui'

export function Sidebar({ onCreate, onNavigate, rev }: { onCreate: (k: CreateKind, account?: Account) => void; onNavigate: () => void; rev: number }) {
  const { accounts, status, settings } = useApp()
  const loc = useLocation()
  const [menu, setMenu] = useState<Anchor | null>(null)
  // bills due today or overdue, for the Subscriptions badge
  const [due, setDue] = useState(0)
  const onSubs = loc.pathname.startsWith('/subscriptions')
  useEffect(() => {
    let live = true
    api.get<{ items: Subscription[] }>(`${V1}/subscriptions`).then(d => { if (live) setDue((d?.items || []).filter(s => s.status === 'overdue' || s.status === 'today').length) }).catch(() => {})
    return () => { live = false }
  }, [rev, onSubs])
  const ownsAny = accounts.some(a => a.role === 'owner')
  const active = accounts.filter(a => !a.archived)
  const archived = accounts.filter(a => a.archived)
  const title = settings?.title || status?.title || 'Pocket Money'
  const createItems: MenuItem[] = [
    { label: 'Expense', icon: 'arrow-up', hint: 'N', onSelect: () => onCreate('expense') },
    { label: 'Income', icon: 'arrow-down', hint: 'I', onSelect: () => onCreate('income') },
    { label: 'Transfer', icon: 'transfer', hint: 'X', onSelect: () => onCreate('transfer') },
    { label: 'Quick entry page', icon: 'zap', onSelect: () => onCreate('page') },
    'sep',
    { label: 'Account', icon: 'bank', onSelect: () => onCreate('account') },
    { label: 'Subscription or bill', icon: 'repeat', onSelect: () => onCreate('subscription') },
  ]
  const nav = [
    { to: '/', label: 'Home', icon: 'home', end: true },
    { to: '/transactions', label: 'Transactions', icon: 'list' },
    { to: '/accounts', label: 'Accounts', icon: 'bank', end: true },
    { to: '/budgets', label: 'Budgets', icon: 'pie' },
    { to: '/subscriptions', label: 'Subscriptions', icon: 'repeat', badge: due },
    { to: '/goals', label: 'Goals', icon: 'target' },
    { to: '/reports', label: 'Reports', icon: 'chart' },
    ...(ownsAny ? [{ to: '/audit', label: 'Audit log', icon: 'history' }] : []),
  ]
  return (
    <aside className="sidebar" aria-label="Money">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Money' ? title : <>Pocket <b>Money</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^\d/.test(status.version) ? 'v' + status.version : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={17} strokeWidth={2.2} />Add<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems} onClose={() => setMenu(null)} width={240} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}{!!n.badge && <span className="badge danger" title={`${n.badge} due today or overdue`}>{n.badge}</span>}
          </NavLink>
        ))}
      </nav>

      <AccountGroup title="Accounts" list={active} onCreate={onCreate} onNavigate={onNavigate} add={() => onCreate('account')} />
      {archived.length > 0 && <AccountGroup title="Archived" list={archived} onCreate={onCreate} onNavigate={onNavigate} closed />}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pm:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function AccountGroup({ title, list, onCreate, onNavigate, add, closed }: { title: string; list: Account[]; onCreate: (k: CreateKind, a?: Account) => void; onNavigate: () => void; add?: () => void; closed?: boolean }) {
  const [open, setOpen] = useState(!closed)
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={() => setOpen(o => !o)}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
        {add && <button type="button" className="btn ghost icon-only xs" aria-label="New account" title="New account" onClick={add}><Icon name="plus" size={14} /></button>}
      </h4>
      {open && (list.length === 0
        ? <p className="sb-empty">No accounts yet. <button type="button" className="link-btn" onClick={add}>Add one</button></p>
        : <ul className="cal-list">{list.map(a => <AccountRow key={a.id} a={a} onCreate={onCreate} onNavigate={onNavigate} />)}</ul>)}
    </section>
  )
}

function AccountRow({ a, onCreate, onNavigate }: { a: Account; onCreate: (k: CreateKind, a?: Account) => void; onNavigate: () => void }) {
  const navigate = useNavigate()
  const { currencies } = useApp()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const go = (to: string) => { onNavigate(); navigate(to) }
  const canBook = a.role !== 'viewer' && !a.archived
  const items: MenuItem[] = [
    { label: 'Open', icon: 'bank', onSelect: () => go(`/accounts/${a.id}`) },
    ...(canBook ? [{ label: 'Add a transaction', icon: 'plus', onSelect: () => onCreate('expense', a) }] : []),
    { label: 'Transactions', icon: 'list', onSelect: () => go(`/transactions?account=${a.id}`) },
  ]
  const bal = fmtMoney(a.balance, a.currency, decimalsOf(a.currency, currencies), { compact: true })
  return (
    <li className={`cal-row ac-row ${a.archived ? 'off' : ''}`}>
      <NavLink to={`/accounts/${a.id}`} onClick={onNavigate} className={({ isActive }) => `cal-check ${isActive ? 'is-active' : ''}`} title={[a.name, a.institution || KIND_LABEL[a.kind]].filter(Boolean).join(' · ')}>
        <span className="ac-ico" style={{ ['--c' as string]: a.color || undefined }}>{a.icon || KIND_ICON[a.kind]}</span>
        <span className="cal-name ellipsis">{a.name}</span>
        <span className={`ac-meta ${a.balance < 0 ? 'neg' : ''}`}>{bal}</span>
      </NavLink>
      <button type="button" className="btn ghost icon-only xs cal-more" aria-label={`Options for ${a.name}`} aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(rectOf(e.currentTarget))}><Icon name="more" size={15} /></button>
      {menu && <Menu anchor={menu} items={items} onClose={() => setMenu(null)} width={210} />}
    </li>
  )
}
