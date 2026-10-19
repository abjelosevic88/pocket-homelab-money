import { useEffect, useMemo, useRef, useState } from 'react'
import { api, upload, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { addDays, decimalsOf, fmtBytes, fmtMoney, todayISO } from '../lib/format'
import type { Attachment, Category, Kind, Subscription, Transaction } from '../lib/types'

interface Part { category_id: number | null; amount: number | null; note: string }
import { Field, Modal, useDebounce, useToast } from './ui'
import { CurrencySelect, MoneyInput } from './Money'
import { AccountSelect, CategoryGrid, CategorySelect, KindTabs, TagInput } from './pickers'
import { Select } from './Select'

export interface TxSeed {
  kind?: Kind; amount?: number | null; currency?: string; account_id?: number; to_account_id?: number | null; category_id?: number | null
  description?: string; payee?: string; tags?: string[]; date?: string; time?: string; notes?: string; template_id?: number
}

interface Suggestion { text: string; count: number; category_id: number | null; payee?: string; last_amount: number; account_id: number; kind: Kind }

function nowHM() { const d = new Date(); return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }

/** The full transaction form. `tx` = edit, otherwise create from `seed`. */
export function TxEditor({ tx, seed, onSaved, onCancel, onDeleted, page = false }: { tx?: Transaction; seed?: TxSeed; onSaved: (t: Transaction, again?: boolean) => void; onCancel?: () => void; onDeleted?: () => void; page?: boolean }) {
  const { accounts, settings, currencies, acctById, reloadCatalog, reloadAccounts, users, catById } = useApp()
  const { toast } = useToast()
  const writable = accounts.filter(a => a.role !== 'viewer' && !a.archived)
  // the configured default, else the everyday account used most recently
  const recent = [...writable].filter(a => a.kind !== 'savings').sort((a, b) => (b.last_tx_date || '').localeCompare(a.last_tx_date || ''))[0]
  const defaultAcct = settings?.default_account_id && writable.some(a => a.id === settings.default_account_id) ? settings.default_account_id : recent?.id || writable[0]?.id || 0
  const [kind, setKind] = useState<Kind>(tx?.kind || seed?.kind || 'expense')
  const [accountId, setAccountId] = useState<number>(tx?.account_id || seed?.account_id || defaultAcct)
  const acct = acctById(accountId)
  const [currency, setCurrency] = useState<string>(tx ? (tx.foreign_amount > 0 ? tx.foreign_currency : tx.currency) : seed?.currency || acct?.currency || settings?.home_currency || 'EUR')
  const [amount, setAmount] = useState<number | null>(tx ? (tx.foreign_amount > 0 ? tx.foreign_amount : tx.amount) : seed?.amount ?? null)
  const [accountAmount, setAccountAmount] = useState<number | null>(tx && tx.foreign_amount > 0 ? tx.amount : null)
  const [rate, setRate] = useState<number | null>(null)
  const [toAccountId, setToAccountId] = useState<number | null>(tx?.to_account_id ?? seed?.to_account_id ?? null)
  const [toAmount, setToAmount] = useState<number | null>(tx?.kind === 'transfer' ? tx.to_amount : null)
  const [categoryId, setCategoryId] = useState<number | null>(tx?.category_id ?? seed?.category_id ?? null)
  const [description, setDescription] = useState(tx?.description ?? seed?.description ?? '')
  const [payee, setPayee] = useState(tx?.payee ?? seed?.payee ?? '')
  const [tags, setTags] = useState<string[]>(tx?.tags ?? seed?.tags ?? [])
  const [date, setDate] = useState(tx?.date || seed?.date || todayISO())
  const [time, setTime] = useState(tx ? tx.time : seed?.time ?? nowHM())
  const [notes, setNotes] = useState(tx?.notes ?? seed?.notes ?? '')
  // split parts are in the receipt currency (what the big amount shows)
  const [parts, setParts] = useState<Part[]>(tx?.splits?.length ? tx.splits.map(p => ({ category_id: p.category_id, amount: tx.foreign_amount > 0 ? p.foreign_amount : p.amount, note: p.note })) : [])
  const [subId, setSubId] = useState<number | null>(tx?.subscription_id ?? null)
  const [subs, setSubs] = useState<Subscription[]>([])
  const [more, setMore] = useState(Boolean(tx && (tx.notes || tx.payee || tx.subscription_id)))
  const [busy, setBusy] = useState(false)
  const [atts, setAtts] = useState<Attachment[]>(tx?.attachments || [])
  const [pending, setPending] = useState<File[]>([])
  const [sugg, setSugg] = useState<Suggestion[]>([])
  const [showSugg, setShowSugg] = useState(false)
  const touchedCat = useRef(Boolean(tx?.category_id || seed?.category_id))
  // an account shared with you books in its owner's categories, so a household account keeps one list
  const shared = acct && acct.role && acct.role !== 'owner' ? acct : undefined
  const [sharedCats, setSharedCats] = useState<Category[] | undefined>(undefined)
  const owner = shared ? users.find(u => u.id === shared.owner_id) : undefined
  useEffect(() => {
    if (!shared) { setSharedCats(undefined); return }
    let live = true
    api.get<Category[]>(`${V1}/accounts/${shared.id}/categories`).then(list => {
      if (!live) return
      setSharedCats(list)
      // carry a picked category over by name, else clear it
      const remap = (id: number | null) => {
        if (!id || list.some(c => c.id === id)) return id
        const mine = catById(id)
        return (mine && list.find(c => !c.archived && c.name.toLowerCase() === mine.name.toLowerCase())?.id) || null
      }
      setCategoryId(remap)
      setParts(ps => ps.map(p => ({ ...p, category_id: remap(p.category_id) })))
    }).catch(() => live && setSharedCats([]))
    return () => { live = false }
  }, [shared?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  const catList = shared ? sharedCats || [] : undefined
  const dq = useDebounce(description, 200)
  const toAcct = acctById(toAccountId)
  const foreign = acct && currency && currency !== acct.currency
  const crossTransfer = kind === 'transfer' && acct && toAcct && acct.currency !== toAcct.currency

  // follow the account's currency unless the user picked another one on purpose
  const prevAcctCur = useRef(acct?.currency)
  // accounts may arrive after the form mounted (page opened directly)
  useEffect(() => { if (!accountId && defaultAcct) setAccountId(defaultAcct) }, [defaultAcct]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (acct && prevAcctCur.current !== acct.currency) {
      if (!currency || currency === prevAcctCur.current || (prevAcctCur.current === undefined && !tx && !seed?.currency)) setCurrency(acct.currency)
      prevAcctCur.current = acct.currency
    }
  }, [acct?.currency]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!foreign || !acct) { setRate(null); return }
    api.get<{ rate: number }>(`${V1}/fx?from=${currency}&to=${acct.currency}&date=${date}`).then(r => setRate(r.rate)).catch(() => setRate(null))
  }, [currency, acct?.currency, date, foreign]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (more && subs.length === 0) api.get<{ items: Subscription[] }>(`${V1}/subscriptions`).then(r => setSubs(r.items)).catch(() => {}) }, [more]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!dq.trim() || (tx && dq === tx.description)) { setSugg([]); return }
    api.get<Suggestion[]>(`${V1}/suggest?q=${encodeURIComponent(dq)}`).then(setSugg).catch(() => setSugg([]))
  }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps

  const converted = useMemo(() => {
    if (!foreign || amount == null || rate == null || !acct) return null
    const fd = decimalsOf(currency, currencies), td = decimalsOf(acct.currency, currencies)
    return Math.round((amount / Math.pow(10, fd)) * rate * Math.pow(10, td))
  }, [foreign, amount, rate, acct, currency, currencies])

  const splitting = parts.length > 0 && kind !== 'transfer'
  const partsSum = parts.reduce((n, p) => n + (p.amount || 0), 0)
  const remaining = (amount || 0) - partsSum
  const empty = parts.findIndex(p => !p.amount)
  const restAt = empty >= 0 ? empty : parts.length - 1
  const startSplit = () => setParts([{ category_id: categoryId, amount, note: '' }, { category_id: null, amount: null, note: '' }])
  const setPart = (i: number, p: Partial<Part>) => setParts(ps => ps.map((x, j) => (j === i ? { ...x, ...p } : x)))
  const dropPart = (i: number) => setParts(ps => {
    const rest = ps.filter((_, j) => j !== i)
    if (rest.length > 1) return rest
    if (rest[0]?.category_id) setCategoryId(rest[0].category_id)
    return []
  })

  const pick = (s: Suggestion) => {
    setDescription(s.text)
    if (!touchedCat.current && s.category_id) {
      const mine = catById(s.category_id)
      const id = !catList ? s.category_id : catList.some(c => c.id === s.category_id) ? s.category_id : catList.find(c => mine && c.name.toLowerCase() === mine.name.toLowerCase())?.id
      if (id) setCategoryId(id)
    }
    if (!payee && s.payee) setPayee(s.payee)
    setShowSugg(false)
  }

  const save = async (again = false) => {
    if (!amount) { toast('Type an amount', 'err'); return }
    if (!accountId) { toast('Choose an account', 'err'); return }
    if (kind === 'transfer' && !toAccountId) { toast('Choose where the money goes', 'err'); return }
    if (splitting && remaining !== 0) { toast(`The parts are ${fmtMoney(Math.abs(remaining), currency, decimalsOf(currency, currencies))} ${remaining > 0 ? 'short of' : 'over'} the total`, 'err'); return }
    setBusy(true)
    const body: Record<string, unknown> = {
      kind, date, time, account_id: accountId, amount, currency, description, payee, category_id: kind === 'transfer' ? 0 : categoryId || 0, tags, notes,
      to_account_id: kind === 'transfer' ? toAccountId : 0, subscription_id: subId || 0,
    }
    if (foreign && accountAmount) body.account_amount = accountAmount
    if (crossTransfer && toAmount) body.to_amount = toAmount
    if (!tx && seed?.template_id) body.template_id = seed.template_id
    if (splitting) body.splits = parts.map(p => ({ category_id: p.category_id || 0, amount: p.amount || 0, note: p.note }))
    else if (tx?.splits?.length) body.splits = []
    try {
      let saved = tx ? await api.put<Transaction>(`${V1}/transactions/${tx.id}`, body) : await api.post<Transaction>(`${V1}/transactions`, body)
      for (const f of pending) await upload(`${V1}/transactions/${saved.id}/attachments`, 'file', f)
      if (pending.length) saved = await api.get<Transaction>(`${V1}/transactions/${saved.id}`)
      if (tags.length) reloadCatalog()
      reloadAccounts()
      toast(tx ? 'Saved' : 'Added')
      onSaved(saved, again)
      if (again && !tx) { setAmount(null); setAccountAmount(null); setDescription(''); setPayee(''); setNotes(''); setPending([]); touchedCat.current = false; setCategoryId(null) }
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const del = async () => {
    if (!tx || !confirm('Delete this transaction?')) return
    await api.del(`${V1}/transactions/${tx.id}`)
    reloadAccounts()
    toast('Deleted')
    onDeleted?.()
  }
  const dup = async () => {
    if (!tx) return
    const t = await api.post<Transaction>(`${V1}/transactions/${tx.id}/duplicate`)
    toast('Copied to today')
    onSaved(t)
  }
  const removeAtt = async (a: Attachment) => {
    if (!confirm(`Remove ${a.name}?`)) return
    await api.del(`${V1}/attachments/${a.id}`)
    setAtts(x => x.filter(y => y.id !== a.id))
  }
  const accDec = decimalsOf(acct?.currency || 'EUR', currencies)

  return (
    <form onSubmit={e => { e.preventDefault(); save(false) }} className="grid" style={{ gap: 14 }}>
      <KindTabs value={kind} onChange={k => { setKind(k); if (k === 'transfer') setCategoryId(null) }} />
      <div>
        <div className="big-amount">
          <MoneyInput value={amount} code={currency} onChange={setAmount} autoFocus={!tx} ariaLabel="Amount" />
          <CurrencySelect value={currency} onChange={setCurrency} />
        </div>
        {foreign && acct && (
          <div className="row wrap mt-sm small">
            <span className="muted">Charged to {acct.name}:</span>
            <div style={{ width: 140 }}><MoneyInput value={accountAmount ?? converted} code={acct.currency} onChange={setAccountAmount} ariaLabel="Charged amount" /></div>
            <span className="muted">{acct.currency}{rate ? ` · 1 ${currency} = ${rate.toFixed(4)} ${acct.currency}` : ''}{accountAmount != null && converted != null && accountAmount !== converted ? ' · your bank rate' : ''}</span>
          </div>
        )}
      </div>
      <div className="two-col tight">
        <Field label={kind === 'income' ? 'Into' : 'From'}><AccountSelect value={accountId} onChange={setAccountId} /></Field>
        {kind === 'transfer'
          ? <Field label="To"><AccountSelect value={toAccountId} onChange={id => setToAccountId(id || null)} exclude={accountId} /></Field>
          : <Field label="Date"><input type="date" value={date} onChange={e => setDate(e.target.value)} /></Field>}
      </div>
      {crossTransfer && toAcct && (
        <Field label={`Arrives in ${toAcct.name}`} help="Leave empty to convert at the day's rate.">
          <MoneyInput value={toAmount} code={toAcct.currency} onChange={setToAmount} />
        </Field>
      )}
      <Field label="Description">
        <div style={{ position: 'relative' }}>
          <input value={description} onChange={e => { setDescription(e.target.value); setShowSugg(true) }} onFocus={() => setShowSugg(true)} onBlur={() => setTimeout(() => setShowSugg(false), 150)} placeholder={kind === 'income' ? 'Salary, refund…' : 'Coffee, groceries…'} />
          {showSugg && sugg.length > 0 && (
            <div className="suggest">{sugg.map(s => <div key={s.text} className="r" onMouseDown={e => { e.preventDefault(); pick(s) }}><span>{s.text}</span><span className="muted small">{s.count}× · {fmtMoney(s.last_amount, acctById(s.account_id)?.currency || 'EUR', accDec)}</span></div>)}</div>
          )}
        </div>
      </Field>
      {splitting && (
        <Field label="Split across categories" group help={remaining === 0 ? 'The parts add up.' : <span className="bad">{fmtMoney(Math.abs(remaining), currency, decimalsOf(currency, currencies))} {remaining > 0 ? 'left to assign' : 'too much'}</span>}>
          <div className="split-parts">
            {parts.map((p, i) => (
              <div key={i} className="split-part">
                <CategorySelect value={p.category_id} kind={kind} list={catList} onChange={id => setPart(i, { category_id: id })} />
                <MoneyInput value={p.amount} code={currency} onChange={v => setPart(i, { amount: v })} ariaLabel={`Part ${i + 1} amount`} />
                <input value={p.note} onChange={e => setPart(i, { note: e.target.value })} placeholder="Note" aria-label={`Part ${i + 1} note`} />
                <button type="button" className="btn ghost icon" onClick={() => dropPart(i)} aria-label="Remove part">✕</button>
              </div>
            ))}
            <div className="row">
              <button type="button" className="btn ghost sm" onClick={() => setParts(ps => [...ps, { category_id: null, amount: remaining > 0 ? remaining : null, note: '' }])}>＋ Part</button>
              {remaining > 0 && <button type="button" className="btn ghost sm" onClick={() => setPart(restAt, { amount: (parts[restAt].amount || 0) + remaining })}>Put the rest in {parts[restAt].amount ? 'the last part' : 'the empty part'}</button>}
              <span className="spacer" />
              <button type="button" className="btn ghost sm" onClick={() => { if (parts[0]?.category_id) setCategoryId(parts[0].category_id); setParts([]) }}>Don't split</button>
            </div>
          </div>
        </Field>
      )}
      {kind !== 'transfer' && !splitting && (
        <Field label="Category" group>
          {page ? <CategoryGrid value={categoryId} kind={kind} list={catList} onChange={id => { touchedCat.current = true; setCategoryId(id) }} />
            : <CategorySelect value={categoryId} kind={kind} list={catList} onChange={id => { touchedCat.current = true; setCategoryId(id) }} />}
          {shared && <span className="muted small">Shared account: {owner?.name || owner?.email || 'its owner'}'s categories</span>}
          <button type="button" className="btn ghost sm" style={{ alignSelf: 'flex-start', marginTop: 6 }} onClick={startSplit} disabled={!amount}>✂ Split across categories</button>
        </Field>
      )}
      <Field label="Tags" group><TagInput value={tags} onChange={setTags} /></Field>
      {kind === 'transfer' && <Field label="Date"><input type="date" value={date} onChange={e => setDate(e.target.value)} /></Field>}
      {page && kind !== 'transfer' && (
        <div className="date-chips">
          <button type="button" className={`chip ${date === todayISO() ? 'active' : ''}`} onClick={() => setDate(todayISO())}>Today</button>
          <button type="button" className={`chip ${date === addDays(todayISO(), -1) ? 'active' : ''}`} onClick={() => setDate(addDays(todayISO(), -1))}>Yesterday</button>
          <button type="button" className={`chip ${date === addDays(todayISO(), -2) ? 'active' : ''}`} onClick={() => setDate(addDays(todayISO(), -2))}>2 days ago</button>
          <input type="time" value={time} onChange={e => setTime(e.target.value)} style={{ width: 120 }} aria-label="Time" />
        </div>
      )}
      {!more && <button type="button" className="btn ghost sm" style={{ justifySelf: 'start' }} onClick={() => setMore(true)}>More: payee, notes, time, receipt, subscription…</button>}
      {more && <>
        <div className="two-col tight">
          <Field label={kind === 'income' ? 'From (payer)' : 'Payee / shop'}><input value={payee} onChange={e => setPayee(e.target.value)} /></Field>
          <Field label="Time"><input type="time" value={time} onChange={e => setTime(e.target.value)} /></Field>
        </div>
        <Field label="Notes"><textarea value={notes} onChange={e => setNotes(e.target.value)} rows={2} /></Field>
        <Field label="Pays subscription" group>
          <Select value={subId ? String(subId) : ''} onChange={v => setSubId(v ? Number(v) : null)} options={subs.map(s => ({ value: String(s.id), label: `${s.icon || s.category_icon || '↻'} ${s.name}`, hint: s.next_date }))} placeholder="None" clearLabel="None" />
        </Field>
        <Field label="Receipts" group>
          <div className="receipts">
            {atts.map(a => (
              <a key={a.id} className="receipt" href={`${V1}/attachments/${a.id}`} target="_blank" rel="noreferrer" style={a.mime.startsWith('image/') ? { backgroundImage: `url(${V1}/attachments/${a.id})` } : undefined}>
                {!a.mime.startsWith('image/') && <span>{a.name}<br />{fmtBytes(a.size)}</span>}
                <button type="button" className="x" onClick={e => { e.preventDefault(); removeAtt(a) }}>✕</button>
              </a>
            ))}
            {pending.map((f, i) => <span key={i} className="receipt">{f.name}</span>)}
            <label className="receipt" style={{ cursor: 'pointer' }}>📷<br />Add<input type="file" accept="image/*,application/pdf" capture="environment" style={{ display: 'none' }} onChange={e => { const f = e.target.files?.[0]; if (f) setPending(p => [...p, f]) }} /></label>
          </div>
        </Field>
      </>}
      <div className="form-actions">
        {tx && <button type="button" className="btn ghost danger-text" onClick={del}>Delete</button>}
        {tx && <button type="button" className="btn ghost" onClick={dup}>Duplicate</button>}
        <span className="spacer" />
        {onCancel && <button type="button" className="btn" onClick={onCancel}>Cancel</button>}
        {!tx && <button type="button" className="btn" disabled={busy} onClick={() => save(true)}>Save & add another</button>}
        <button className="btn primary" disabled={busy}>{busy ? 'Saving…' : tx ? 'Save' : 'Add'}</button>
      </div>
      {tx && <div className="muted small">Added {tx.created_at.slice(0, 10)}{tx.created_by_name ? ` by ${tx.created_by_name}` : ''} · via {tx.source}{tx.subscription_name ? ` · pays ${tx.subscription_name}` : ''}</div>}
    </form>
  )
}

/** The editor in a modal; loads the full transaction (tags, receipts) first. */
export function TxModal({ id, seed, onClose, onChanged }: { id?: number; seed?: TxSeed; onClose: () => void; onChanged: () => void }) {
  const [tx, setTx] = useState<Transaction | null>(null)
  useEffect(() => { if (id) api.get<Transaction>(`${V1}/transactions/${id}`).then(setTx).catch(() => onClose()) }, [id]) // eslint-disable-line react-hooks/exhaustive-deps
  if (id && !tx) return null
  return (
    <Modal title={tx ? 'Transaction' : 'New transaction'} onClose={onClose}>
      <TxEditor tx={tx || undefined} seed={seed} onCancel={onClose} onSaved={(_, again) => { onChanged(); if (!again) onClose() }} onDeleted={() => { onChanged(); onClose() }} />
    </Modal>
  )
}
