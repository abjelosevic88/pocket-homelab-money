// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useMatch, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Account, Kind } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { ReceiptScanner } from './ReceiptScanner'
import { TxModal } from './TxEditor'
import { AccountForm } from '../pages/Accounts'
import { SubForm } from '../pages/Subscriptions'
import { Avatar, type MenuItem, MenuButton, modalOpen, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pm-sidebar'

export type CreateKind = Kind | 'page' | 'account' | 'subscription' | 'receipt'

export default function Layout() {
  const { accounts, reloadAccounts } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [modal, setModal] = useState<{ kind: CreateKind; account?: Account } | null>(null)
  // bumped when something was created from the shell: the page on screen remounts and reloads
  const [rev, setRev] = useState(0)

  // the account on screen is the default for a new transaction
  const am = useMatch('/accounts/:id')
  const current = accounts.find(a => String(a.id) === am?.params.id)

  const create = (kind: CreateKind, a?: Account) => {
    closeDrawer()
    if (kind === 'page') { navigate('/add'); return }
    setModal({ kind, account: a || (current && current.role !== 'viewer' && !current.archived ? current : undefined) })
  }
  const createRef = useRef(create)
  createRef.current = create

  useEffect(() => {
    const open = () => setShortcuts(true)
    window.addEventListener('pm:shortcuts', open)
    return () => window.removeEventListener('pm:shortcuts', open)
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if ((t instanceof Element && t.closest('input, textarea, select, [contenteditable="true"]')) || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': e.preventDefault(); createRef.current('expense'); break
        case 'i': e.preventDefault(); createRef.current('income'); break
        case 'x': e.preventDefault(); createRef.current('transfer'); break
        case 'q': e.preventDefault(); createRef.current('receipt'); break
        case 'h': go('/'); break
        case 't': go('/transactions'); break
        case 'a': go('/accounts'); break
        case 'b': go('/budgets'); break
        case 's': go('/subscriptions'); break
        case 'g': go('/goals'); break
        case 'r': go('/reports'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'expense', label: 'New expense', icon: 'arrow-up', hint: 'N', keywords: 'add transaction spend pay', run: () => create('expense') },
    { id: 'income', label: 'New income', icon: 'arrow-down', hint: 'I', keywords: 'add transaction salary', run: () => create('income') },
    { id: 'transfer', label: 'New transfer', icon: 'transfer', hint: 'X', keywords: 'add transaction move', run: () => create('transfer') },
    { id: 'receipt', label: 'Scan a receipt', icon: 'camera', hint: 'Q', keywords: 'qr fiscal racun račun bill scan', run: () => create('receipt') },
    { id: 'receipts', label: 'Receipts & scan log', icon: 'receipt', keywords: 'fiscal racun shops merchants', run: () => navigate('/receipts') },
    { id: 'products', label: 'Products', icon: 'package', keywords: 'items groceries milk mlijeko groups prices', run: () => navigate('/products') },
    { id: 'add', label: 'Quick entry page', icon: 'zap', keywords: 'add quick templates', run: () => navigate('/add') },
    { id: 'account', label: 'New account', icon: 'bank', keywords: 'create bank cash card', run: () => create('account') },
    { id: 'subscription', label: 'New subscription or bill', icon: 'repeat', keywords: 'create recurring rent', run: () => create('subscription') },
    { id: 'home', label: 'Home', icon: 'home', hint: 'H', run: () => navigate('/') },
    { id: 'transactions', label: 'Transactions', icon: 'list', hint: 'T', run: () => navigate('/transactions') },
    { id: 'accounts', label: 'Accounts', icon: 'bank', hint: 'A', run: () => navigate('/accounts') },
    { id: 'budgets', label: 'Budgets', icon: 'pie', hint: 'B', run: () => navigate('/budgets') },
    { id: 'subscriptions', label: 'Subscriptions & bills', icon: 'repeat', hint: 'S', run: () => navigate('/subscriptions') },
    { id: 'goals', label: 'Goals', icon: 'target', hint: 'G', run: () => navigate('/goals') },
    { id: 'reports', label: 'Reports', icon: 'chart', hint: 'R', run: () => navigate('/reports') },
    { id: 'audit', label: 'Audit log', icon: 'history', run: () => navigate('/audit') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  const changed = () => { setRev(r => r + 1); reloadAccounts().catch(() => {}) }
  const txKind = modal && (modal.kind === 'expense' || modal.kind === 'income' || modal.kind === 'transfer') ? modal.kind : null
  return (
    <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
      {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
      <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
        <Sidebar onCreate={create} onNavigate={closeDrawer} rev={rev} />
      </div>
      <div className="main">
        <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} />
        <main className="content"><div className="content-inner" key={rev}><Outlet /></div></main>
      </div>
      {mobile && loc.pathname !== '/add' && <button type="button" className="fab" aria-label="Add a transaction" title="Add a transaction" onClick={() => navigate('/add')}><Icon name="plus" size={26} strokeWidth={2.2} /></button>}
      {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
      {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
      {txKind && <TxModal seed={{ kind: txKind, ...(modal?.account ? { account_id: modal.account.id } : {}) }} onClose={() => setModal(null)} onChanged={changed} />}
      {modal?.kind === 'receipt' && <ReceiptScanner onClose={() => { setModal(null); changed() }} />}
      {modal?.kind === 'account' && <AccountForm onClose={() => setModal(null)} onSaved={a => { setModal(null); reloadAccounts().catch(() => {}); navigate(`/accounts/${a.id}`) }} />}
      {modal?.kind === 'subscription' && <SubForm sub={{}} onClose={() => setModal(null)} onSaved={s => { setModal(null); navigate(`/subscriptions/${s.id}`); setRev(r => r + 1) }} />}
    </div>
  )
}

const SECTION: Record<string, string> = { '': 'Home', add: 'Add', transactions: 'Transactions', accounts: 'Accounts', budgets: 'Budgets', subscriptions: 'Subscriptions', goals: 'Goals', reports: 'Reports', audit: 'Audit log', receipts: 'Receipts', products: 'Products' }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean }) {
  const { user, themeMode, setThemeMode, status, setUser, accounts } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const parts = loc.pathname.split('/')
  const top = parts[1] || ''
  const section = SECTION[top] || ''
  // a second level: the account's name, or "Subscription" / "Transaction"
  const acct = top === 'accounts' && parts[2] ? accounts.find(a => String(a.id) === parts[2]) : undefined
  const leaf = acct ? acct.name : parts[2] && top === 'subscriptions' ? 'Subscription' : parts[2] && top === 'transactions' ? 'Transaction' : ''
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pm:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {leaf ? <><button type="button" className="crumb" onClick={() => navigate(`/${top}`)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{leaf}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
