import React, { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { api, V1 } from './api'
import { setDateFormats, type DateFormat, type TimeFormat } from './format'
import type { Account, Category, Currency, Meta, Settings, Tag, User } from './types'

export type ThemeMode = 'system' | 'light' | 'dark'

interface AuthStatus { needs_setup: boolean; auth_mode: string; authenticated: boolean; user: User | null; version: string; title: string; oidc?: boolean; oidc_name?: string; password_login?: boolean }
interface AppState {
  status: AuthStatus | null
  user: User | null
  settings: Settings | null
  users: User[]
  currencies: Currency[]
  meta: Meta | null
  accounts: Account[]
  categories: Category[]
  tags: Tag[]
  themeMode: ThemeMode
  theme: 'light' | 'dark'
  setThemeMode: (m: ThemeMode) => void
  toggleTheme: () => void
  refresh: () => Promise<void>
  reloadMeta: () => Promise<void>
  reloadAccounts: () => Promise<void>
  reloadCatalog: () => Promise<void>
  setUser: (u: User | null) => void
  setSettings: (s: Settings) => void
  home: string
  catById: (id: number | null | undefined) => Category | undefined
  acctById: (id: number | null | undefined) => Account | undefined
}
const Ctx = createContext<AppState>(null!)
export function useApp() { return useContext(Ctx) }

const EMPTY_META: Meta = { account_kinds: [], transaction_kinds: [], frequencies: [], subscription_modes: [], budget_periods: [] }

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [settings, setSettingsRaw] = useState<Settings | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [currencies, setCurrencies] = useState<Currency[]>([])
  const [meta, setMeta] = useState<Meta | null>(null)
  const [accounts, setAccounts] = useState<Account[]>([])
  const [categories, setCategories] = useState<Category[]>([])
  const [tags, setTags] = useState<Tag[]>([])
  // System unless picked; the old 'pm-theme' key was written on every visit, so it is not a choice
  const [themeMode, setThemeModeRaw] = useState<ThemeMode>(() => { localStorage.removeItem('pm-theme'); return (localStorage.getItem('pm-theme-mode') as ThemeMode) || 'system' })
  const [systemDark, setSystemDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches)
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const h = () => setSystemDark(mq.matches)
    mq.addEventListener('change', h)
    return () => mq.removeEventListener('change', h)
  }, [])
  const theme: 'light' | 'dark' = themeMode === 'system' ? (systemDark ? 'dark' : 'light') : themeMode
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document.querySelectorAll('meta[name="theme-color"]').forEach(m => m.setAttribute('content', theme === 'dark' ? '#111113' : '#ffffff'))
  }, [theme])
  const setThemeMode = useCallback((m: ThemeMode) => { setThemeModeRaw(m); if (m === 'system') localStorage.removeItem('pm-theme-mode'); else localStorage.setItem('pm-theme-mode', m) }, [])
  useEffect(() => { if (settings?.title) document.title = settings.title }, [settings?.title])
  const setSettings = useCallback((s: Settings) => { setSettingsRaw(s); setDateFormats(s.date_format as DateFormat, s.time_format as TimeFormat) }, [])
  const reloadAccounts = useCallback(async () => { setAccounts(await api.get<Account[]>(`${V1}/accounts?archived=1`)) }, [])
  const reloadCatalog = useCallback(async () => {
    const [c, t] = await Promise.all([api.get<Category[]>(`${V1}/categories`), api.get<Tag[]>(`${V1}/tags`)])
    setCategories(c); setTags(t)
  }, [])
  const reloadMeta = useCallback(async () => {
    const [u, c, m] = await Promise.all([api.get<User[]>(`${V1}/users`), api.get<Currency[]>(`${V1}/currencies`), api.get<Meta>(`${V1}/meta`).catch(() => EMPTY_META)])
    setUsers(u); setCurrencies(c); setMeta(m)
    await Promise.all([reloadAccounts(), reloadCatalog()])
  }, [reloadAccounts, reloadCatalog])
  const refresh = useCallback(async () => {
    const st = await api.get<AuthStatus>(`${V1}/auth/status`)
    setStatus(st)
    setUser(st.user)
    if (st.authenticated) {
      setSettings(await api.get<Settings>(`${V1}/settings`))
      await reloadMeta()
    }
  }, [reloadMeta, setSettings])
  useEffect(() => { refresh().catch(() => setStatus({ needs_setup: false, auth_mode: 'local', authenticated: false, user: null, version: '', title: 'Pocket Money' })) }, [refresh])
  useEffect(() => {
    const h = () => { setUser(null); setStatus(s => s ? { ...s, authenticated: false, user: null } : s) }
    window.addEventListener('pm:unauthorized', h)
    return () => window.removeEventListener('pm:unauthorized', h)
  }, [])
  const catById = useCallback((id: number | null | undefined) => (id ? categories.find(c => c.id === id) : undefined), [categories])
  const acctById = useCallback((id: number | null | undefined) => (id ? accounts.find(a => a.id === id) : undefined), [accounts])
  return (
    <Ctx.Provider value={{
      status, user, settings, users, currencies, meta, accounts, categories, tags, themeMode, theme, setThemeMode,
      toggleTheme: () => setThemeMode(theme === 'dark' ? 'light' : 'dark'), refresh, reloadMeta, reloadAccounts, reloadCatalog, setUser, setSettings,
      home: settings?.home_currency || 'EUR', catById, acctById,
    }}>{children}</Ctx.Provider>
  )
}
