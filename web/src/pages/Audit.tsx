import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { colorFor, fmtDateTime, initials } from '../lib/format'
import type { AuditActor, AuditEntry, AuditPage } from '../lib/types'
import { Card, Empty, Loading, PageHeader, clickRow } from '../components/ui'
import { Select } from '../components/Select'
import { Icon } from '../components/icons'

const ACTIONS = [
  { value: 'transaction.created', label: 'Added' },
  { value: 'transaction.updated', label: 'Edited' },
  { value: 'transaction.deleted', label: 'Deleted' },
  { value: 'attachment', label: 'Receipts' },
  { value: 'account', label: 'Account & sharing' },
  { value: 'import.summary', label: 'Imports & mirror' },
]

const SOURCE: Record<string, string> = {
  web: 'web', api: 'API', firefly_api: 'phone / Firefly API', quick: 'quick add', template: 'template', import: 'import',
  mirror: 'Firefly mirror', schedule: 'scheduled', bulk: 'bulk', reconcile: 'balance check',
}

const FIELD: Record<string, string> = {
  date: 'Date', kind: 'Kind', amount: 'Amount', foreign_amount: 'Foreign amount', account: 'Account', to_account: 'To account', to_amount: 'Received',
  category: 'Category', description: 'Description', payee: 'Payee', tags: 'Tags', notes: 'Notes', splits: 'Splits', file: 'File', member: 'Member',
  balance: 'Balance', adjustment: 'Adjustment', added: 'Added', updated: 'Updated', deleted: 'Deleted',
  opening_balance: 'Opening balance', opening_date: 'Opening date', institution: 'Bank', number: 'IBAN / number', credit_limit: 'Credit limit',
  include_net_worth: 'Net worth', everyday: 'Everyday money', direction: 'Direction', interest_rate: 'Interest rate', interest_auto: 'Auto interest',
  principal: 'Original amount', payment: 'Monthly payment', payment_day: 'Payment day', icon: 'Icon', color: 'Colour', archived: 'Archived', name: 'Name', currency: 'Currency',
}
const ORDER = Object.keys(FIELD)

function ActorBadge({ e }: { e: AuditEntry }) {
  const name = e.actor_name || e.actor_label
  const bg = e.actor_user_id ? colorFor(e.actor_user_id) : 'var(--muted)'
  return <span className="avatar" style={{ background: bg }} title={e.actor_label}>{e.actor_user_id ? initials(name) : '⚙'}</span>
}

function Diff({ e }: { e: AuditEntry }) {
  const keys = Object.keys(e.changes || {}).sort((a, b) => (ORDER.indexOf(a) + 1 || 99) - (ORDER.indexOf(b) + 1 || 99))
  if (keys.length === 0) return <div className="muted small">No details.</div>
  const deleted = e.action.endsWith('.deleted') || e.action === 'account.unshared'
  const created = e.action.endsWith('.created') || e.action.endsWith('.added') || e.action === 'import.summary' || (e.action === 'account.shared' && !e.changes.member?.from)
  return (
    <dl className="kv audit-diff">
      {keys.map(k => {
        const c = e.changes[k]
        return [
          <dt key={k + 'k'}>{FIELD[k] || k}</dt>,
          <dd key={k + 'v'}>
            {created ? (c.to || '—') : deleted ? <s className="muted">{c.from || '—'}</s> : <><span className="muted">{c.from || '—'}</span> → <b>{c.to || '—'}</b></>}
          </dd>,
        ]
      })}
    </dl>
  )
}

/** An audit log: one account's (accountId) or the merged feed of every account the caller owns. */
export function AuditLog({ accountId, showAccount = false }: { accountId?: number; showAccount?: boolean }) {
  const [actor, setActor] = useState('')
  const [action, setAction] = useState('')
  const [account, setAccount] = useState('')
  const [items, setItems] = useState<AuditEntry[] | null>(null)
  const [actors, setActors] = useState<AuditActor[]>([])
  const [next, setNext] = useState<number | null>(null)
  const [open, setOpen] = useState<Set<number>>(new Set())
  const [err, setErr] = useState('')
  const { accounts } = useApp()
  const owned = accounts.filter(a => a.role === 'owner')
  const url = (before?: number) => {
    const q = new URLSearchParams({ limit: '50' })
    if (actor) q.set('actor', actor)
    if (action) q.set('action', action)
    if (!accountId && account) q.set('account', account)
    if (before) q.set('before', String(before))
    return accountId ? `${V1}/accounts/${accountId}/audit?${q}` : `${V1}/audit?${q}`
  }
  useEffect(() => {
    setItems(null)
    api.get<AuditPage>(url()).then(p => { setItems(p.items); setNext(p.next_before); setActors(p.actors); setErr('') }).catch(e => setErr((e as Error).message))
  }, [accountId, actor, action, account]) // eslint-disable-line react-hooks/exhaustive-deps
  const more = async () => {
    if (!next) return
    const p = await api.get<AuditPage>(url(next))
    setItems(xs => [...(xs || []), ...p.items]); setNext(p.next_before)
  }
  const toggle = (id: number) => setOpen(s => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const filtered = Boolean(actor || action || account)
  return (
    <>
      <div className="toolbar">
        <Select value={actor} onChange={setActor} options={actors.map(a => ({ value: a.key, label: a.name, hint: a.user_id ? '' : 'job' }))} placeholder="Anyone" clearLabel="Anyone" ariaLabel="Person" />
        <Select value={action} onChange={setAction} options={ACTIONS} placeholder="Any change" clearLabel="Any change" ariaLabel="Action" />
        {!accountId && owned.length > 1 && <Select value={account} onChange={setAccount} options={owned.map(a => ({ value: String(a.id), label: a.name, hint: a.currency }))} placeholder="All my accounts" clearLabel="All my accounts" ariaLabel="Account" />}
      </div>
      {err ? <Empty title="Could not load the audit log">{err}</Empty> : !items ? <Loading /> : items.length === 0 ? <Empty title="Nothing logged">{filtered ? 'No change matches these filters.' : 'Changes made from now on — by you, people you share with, apps and background jobs — show up here.'}</Empty> : (
        <div className="audit-list">
          {items.map(e => (
            <div key={e.id} className={`audit-row ${open.has(e.id) ? 'open' : ''}`}>
              <div className="audit-head" {...clickRow(() => toggle(e.id))} aria-expanded={open.has(e.id)}>
                <ActorBadge e={e} />
                <span className="audit-main">
                  <span className="t">{e.summary}</span>
                  <span className="s">
                    {fmtDateTime(e.created_at)} · {e.actor_label}
                    <span className="badge">{SOURCE[e.source] || e.source}</span>
                    {showAccount && e.account_name && <span className="badge cat">{e.account_name}</span>}
                  </span>
                </span>
                <span className="muted small">{open.has(e.id) ? '▾' : '▸'}</span>
              </div>
              {open.has(e.id) && <div className="audit-body">
                <Diff e={e} />
                {e.transaction_id && !e.action.endsWith('.deleted') && <Link className="btn ghost sm mt-sm" to={`/transactions/${e.transaction_id}`}>Open transaction<Icon name="chevron-right" size={14} /></Link>}
              </div>}
            </div>
          ))}
          {next && <button type="button" className="btn ghost sm mt-sm" onClick={more}>Load older</button>}
        </div>
      )}
    </>
  )
}

/** Every change on the accounts I own, by anyone. */
export default function Audit() {
  return (
    <>
      <PageHeader title="Audit log" sub="Who added, edited or deleted what on the accounts you own — you, people you share with, apps and background jobs." />
      <Card><AuditLog showAccount /></Card>
    </>
  )
}
