import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney, todayISO, addDays, weekday } from '../lib/format'
import type { Transaction } from '../lib/types'
import { CatIcon, TxAmount } from './pickers'

function dayTitle(d: string) {
  const t = todayISO()
  if (d === t) return 'Today'
  if (d === addDays(t, -1)) return 'Yesterday'
  return `${weekday(d, true)}, ${fmtDate(d, d.slice(0, 4) !== t.slice(0, 4))}`
}

/** Transactions grouped by day; the day header shows what went out that day. */
export function TxList({ items, onOpen, selected, onToggle, showAccount = true, compact = false, perspective }: { items: Transaction[]; onOpen: (t: Transaction) => void; selected?: Set<number>; onToggle?: (id: number) => void; showAccount?: boolean; compact?: boolean; perspective?: number }) {
  const { home, currencies } = useApp()
  const groups: { date: string; items: Transaction[]; out: number; in: number }[] = []
  for (const t of items) {
    let g = groups[groups.length - 1]
    if (!g || g.date !== t.date) { g = { date: t.date, items: [], out: 0, in: 0 }; groups.push(g) }
    g.items.push(t)
    if (t.kind === 'expense') g.out += t.amount_home
    if (t.kind === 'income') g.in += t.amount_home
  }
  const dec = decimalsOf(home, currencies)
  return (
    <div className="tx-list">
      {groups.map(g => (
        <div key={g.date}>
          {!compact && <div className="tx-day"><span className="d">{dayTitle(g.date)}</span><span>{g.in > 0 && <span className="ok">+{fmtMoney(g.in, home, dec)} </span>}{g.out > 0 && <span>−{fmtMoney(g.out, home, dec)}</span>}</span></div>}
          {g.items.map(t => (
            <div key={t.id} className={`tx-row ${selected?.has(t.id) ? 'sel' : ''}`} onClick={() => onOpen(t)}>
              <span className="pick">
                {onToggle && <input type="checkbox" className="check" checked={selected?.has(t.id) || false} onClick={e => e.stopPropagation()} onChange={() => onToggle(t.id)} aria-label="Select" />}
                <CatIcon t={t} />
              </span>
              <span className="tx-main">
                <span className="t">{t.description || t.payee || t.category_name || (t.kind === 'transfer' ? 'Transfer' : '—')}{t.attachment_count > 0 && <span title="Receipt">📎</span>}{t.subscription_id && <span title={t.subscription_name}>↻</span>}</span>
                <span className="s">
                  {compact && <>{fmtDate(t.date, false)} · </>}
                  {t.kind === 'transfer' ? `${t.account_name} → ${t.to_account_name}` : [t.category_name || 'Uncategorised', showAccount ? t.account_name : '', t.payee && t.payee !== t.description ? t.payee : '', t.time].filter(Boolean).join(' · ')}
                  {t.tags.length > 0 && <> · {t.tags.map(x => <span key={x} className="badge tag">{x}</span>)}</>}
                </span>
              </span>
              <TxAmount t={t} perspective={perspective} />
            </div>
          ))}
        </div>
      ))}
    </div>
  )
}
