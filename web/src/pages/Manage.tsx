import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtDate, fmtMoney } from '../lib/format'
import type { Category, Condition, Kind, Rule, Tag, Template, Transaction } from '../lib/types'
import { Card, Empty, Field, Loading, Modal, Segmented, clickRow, ownClicks, useAsync, useToast } from '../components/ui'
import { Select } from '../components/Select'
import { CurrencySelect, MoneyInput } from '../components/Money'
import { AccountSelect, CategorySelect, KindTabs, TagInput } from '../components/pickers'
import { TxList } from '../components/TxList'

// ---------------- categories ----------------

export function Categories() {
  const { categories, reloadCatalog } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<Category> | null>(null)
  const [del, setDel] = useState<Category | null>(null)
  const [moveTo, setMoveTo] = useState<number | null>(null)
  const parents = categories.filter(c => !c.parent_id)
  const remove = async () => {
    if (!del) return
    await api.del(`${V1}/categories/${del.id}${moveTo ? `?move_to=${moveTo}` : ''}`)
    toast('Category removed'); setDel(null); setMoveTo(null); reloadCatalog()
  }
  return (
    <Card title="Categories" actions={<button className="btn primary sm" onClick={() => setEdit({ kind: 'expense' })}>+ Category</button>}>
      <p className="muted small">Shared by everyone. One level of sub-categories (Food › Bakery): reports roll children up into their parent, budgets on a parent include its children.</p>
      {categories.length === 0 ? <Empty title="No categories yet" /> : (
        <table className="table compact">
          <thead><tr><th>Category</th><th>Kind</th><th className="num">Transactions</th><th /></tr></thead>
          <tbody>
            {parents.flatMap(p => [p, ...categories.filter(c => c.parent_id === p.id)]).map(c => (
              <tr key={c.id} className={`clickable ${c.archived ? 'muted' : ''}`} {...clickRow(() => setEdit(c))} title="Edit">
                <td style={{ paddingLeft: c.parent_id ? 34 : undefined }}>{c.icon || '•'} {c.name}{c.archived && <span className="badge" style={{ marginLeft: 6 }}>archived</span>}</td>
                <td className="muted">{c.kind}</td>
                <td className="num">{c.tx_count}</td>
                <td className="right" {...ownClicks}><Link className="btn ghost sm" to={`/transactions?category=${c.id}`}>Show</Link><button className="btn ghost sm danger-text" onClick={() => setDel(c)}>Remove</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {edit && <CategoryModal cat={edit} parents={parents.filter(p => p.id !== edit.id)} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadCatalog() }} />}
      {del && (
        <Modal title={`Remove ${del.name}?`} onClose={() => setDel(null)}>
          <p>{del.tx_count} transactions use it. Move them to:</p>
          <CategorySelect value={moveTo} onChange={setMoveTo} kind="any" placeholder="No category" />
          <div className="form-actions"><button className="btn" onClick={() => setDel(null)}>Cancel</button><button className="btn danger" onClick={remove}>Remove</button></div>
        </Modal>
      )}
    </Card>
  )
}

const EMOJI = ['🛒', '🥐', '🍽️', '☕', '🍷', '🚗', '⛽', '🚇', '🏠', '🧾', '⚡', '📱', '💊', '🐾', '👕', '💄', '💻', '🔧', '🎁', '✈️', '🏋️', '📚', '🎸', '🧒', '🌾', '🏢', '💰', '💼', '🛡️', '🎬', '🎧', '📦']

function CategoryModal({ cat, parents, onClose, onSaved }: { cat: Partial<Category>; parents: Category[]; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: cat.name || '', icon: cat.icon || '', kind: cat.kind || 'expense', parent_id: cat.parent_id ?? null as number | null, archived: cat.archived ?? false })
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const body = { ...f, parent_id: f.parent_id || 0 }
      if (cat.id) await api.put(`${V1}/categories/${cat.id}`, body); else await api.post(`${V1}/categories`, body)
      onSaved()
    } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <Modal title={cat.id ? `Edit ${cat.name}` : 'New category'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <div className="row"><input value={f.icon} onChange={e => setF({ ...f, icon: e.target.value })} style={{ width: 64, textAlign: 'center', fontSize: 20 }} maxLength={4} placeholder="🙂" aria-label="Emoji" /><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} placeholder="Name" autoFocus required /></div>
        <div className="chips">{EMOJI.map(e => <button type="button" key={e} className={`chip sm ${f.icon === e ? 'active' : ''}`} onClick={() => setF({ ...f, icon: e })}>{e}</button>)}</div>
        <Field label="Used for" group><Segmented value={f.kind} onChange={v => setF({ ...f, kind: v })} options={[{ value: 'expense', label: 'Expenses' }, { value: 'income', label: 'Income' }, { value: 'both', label: 'Both' }]} /></Field>
        <Field label="Inside (optional)" group><Select value={f.parent_id ? String(f.parent_id) : ''} onChange={v => setF({ ...f, parent_id: v ? Number(v) : null })} options={parents.map(p => ({ value: String(p.id), label: `${p.icon || '•'} ${p.name}` }))} clearLabel="Top level" placeholder="Top level" /></Field>
        {cat.id && <label className="row"><input type="checkbox" checked={f.archived} onChange={e => setF({ ...f, archived: e.target.checked })} style={{ width: 'auto' }} /> Archived (kept on old transactions, hidden from pickers)</label>}
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

// ---------------- tags ----------------

export function TagsAdmin() {
  const { tags, reloadCatalog } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Tag | null>(null)
  const [name, setName] = useState('')
  const [merge, setMerge] = useState('')
  const save = async () => {
    if (!edit) return
    try {
      if (merge) { await api.post(`${V1}/tags/${edit.id}/merge`, { into: Number(merge) }); toast('Merged') }
      else await api.put(`${V1}/tags/${edit.id}`, { name, archived: edit.archived })
      setEdit(null); setMerge(''); reloadCatalog()
    } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Card title="Tags">
      <p className="muted small">Free labels across categories: a trip (<code>lustica 2026</code>), a vehicle (<code>skoda</code>), a person. Pocket Travel and Pocket Garage read their spending by tag.</p>
      {tags.length === 0 ? <Empty title="No tags yet">Type #tag in quick add or use the tag field.</Empty> : (
        <table className="table compact">
          <thead><tr><th>Tag</th><th className="num">Transactions</th><th>Last used</th><th /></tr></thead>
          <tbody>{tags.map(t => <tr key={t.id} className={`clickable ${t.archived ? 'muted' : ''}`} {...clickRow(() => { setEdit(t); setName(t.name) })} title="Edit"><td>#{t.name}</td><td className="num">{t.tx_count}</td><td className="muted">{t.last_used ? fmtDate(t.last_used) : '—'}</td><td className="right" {...ownClicks}><Link className="btn ghost sm" to={`/transactions?tag=${encodeURIComponent(t.name)}`}>Show</Link><button className="btn ghost sm danger-text" onClick={async () => { if (confirm(`Remove #${t.name} from ${t.tx_count} transactions?`)) { await api.del(`${V1}/tags/${t.id}`); reloadCatalog() } }}>Remove</button></td></tr>)}</tbody>
        </table>
      )}
      {edit && (
        <Modal title={`#${edit.name}`} onClose={() => setEdit(null)}>
          <div className="grid" style={{ gap: 12 }}>
            <Field label="Rename"><input value={name} onChange={e => setName(e.target.value)} /></Field>
            <label className="row"><input type="checkbox" checked={edit.archived} onChange={e => setEdit({ ...edit, archived: e.target.checked })} style={{ width: 'auto' }} /> Archived (hidden from suggestions)</label>
            <Field label="…or merge into" group><Select value={merge} onChange={setMerge} options={tags.filter(t => t.id !== edit.id).map(t => ({ value: String(t.id), label: `#${t.name}` }))} clearLabel="Don't merge" placeholder="Don't merge" /></Field>
            <div className="form-actions"><button className="btn" onClick={() => setEdit(null)}>Cancel</button><button className="btn primary" onClick={save}>{merge ? 'Merge' : 'Save'}</button></div>
          </div>
        </Modal>
      )}
    </Card>
  )
}

// ---------------- templates ----------------

export function TemplatesAdmin() {
  const { currencies, acctById } = useApp()
  const { data, reload } = useAsync(() => api.get<Template[]>(`${V1}/templates`), [])
  const [edit, setEdit] = useState<Partial<Template> | null>(null)
  if (!data) return <Loading />
  return (
    <Card title="Templates" actions={<button className="btn primary sm" onClick={() => setEdit({ kind: 'expense', tags: [] })}>+ Template</button>}>
      <p className="muted small">One tap on Home or Add books a template with an amount; without one it opens the form. Quick add matches them by the start of the name or an alias — <code>cg h 20</code> finds “cg hrana”. Most used come first, on every device.</p>
      {data.length === 0 ? <Empty title="No templates yet" /> : (
        <table className="table compact">
          <thead><tr><th>Name</th><th>Aliases</th><th className="num">Amount</th><th>Category</th><th>Account</th><th className="num">Used</th></tr></thead>
          <tbody>{data.map(t => {
            const cur = t.currency || acctById(t.account_id)?.currency || ''
            return <tr key={t.id} className="clickable" {...clickRow(() => setEdit(t))} title="Edit"><td>{t.pinned && '📌 '}{t.category_icon} {t.name}</td><td className="muted">{t.aliases}</td><td className="num">{t.amount != null ? fmtMoney(t.amount, cur, decimalsOf(cur, currencies)) : '—'}{t.currency && <span className="badge" style={{ marginLeft: 4 }}>{t.currency}</span>}</td><td>{t.category_name}</td><td className="muted">{t.account_name}</td><td className="num">{t.use_count}</td></tr>
          })}</tbody>
        </table>
      )}
      {edit && <TemplateModal t={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload() }} />}
    </Card>
  )
}

function TemplateModal({ t, onClose, onSaved }: { t: Partial<Template>; onClose: () => void; onSaved: () => void }) {
  const { acctById, home } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: t.name || '', aliases: t.aliases || '', kind: (t.kind || 'expense') as Kind, amount: t.amount ?? null as number | null, currency: t.currency || '',
    account_id: t.account_id ?? null as number | null, to_account_id: t.to_account_id ?? null as number | null, category_id: t.category_id ?? null as number | null,
    tags: t.tags || [] as string[], description: t.description || '', payee: t.payee || '', notes: t.notes || '', pinned: t.pinned ?? false })
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const cur = f.currency || acctById(f.account_id)?.currency || home
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const body = { ...f, amount: f.amount || 0, clear_amount: !f.amount, account_id: f.account_id || 0, to_account_id: f.to_account_id || 0, category_id: f.category_id || 0 }
    try { if (t.id) await api.put(`${V1}/templates/${t.id}`, body); else await api.post(`${V1}/templates`, body); onSaved() } catch (err) { toast((err as Error).message, 'err') }
  }
  const del = async () => { if (t.id && confirm(`Delete ${t.name}?`)) { await api.del(`${V1}/templates/${t.id}`); onSaved() } }
  return (
    <Modal title={t.id ? `Edit ${t.name}` : 'New template'} onClose={onClose} size="lg">
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <KindTabs value={f.kind} onChange={k => set('kind', k)} />
        <div className="two-col tight">
          <Field label="Name" help="What you type or tap"><input value={f.name} onChange={e => set('name', e.target.value)} autoFocus required /></Field>
          <Field label="Aliases" help="Comma separated other names"><input value={f.aliases} onChange={e => set('aliases', e.target.value)} placeholder="kafa, coffee" /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Amount (empty = ask)" group><div className="input-group"><MoneyInput value={f.amount} code={cur} onChange={v => set('amount', v)} /><CurrencySelect value={f.currency} onChange={v => set('currency', v)} /></div></Field>
          <Field label=" " group>{f.currency && <button type="button" className="btn ghost sm" onClick={() => set('currency', '')}>Use the account's currency</button>}</Field>
        </div>
        <div className="two-col tight">
          <Field label="Account" group><AccountSelect value={f.account_id} onChange={id => set('account_id', id || null)} clearLabel="Default account" placeholder="Default account" /></Field>
          {f.kind === 'transfer' ? <Field label="To" group><AccountSelect value={f.to_account_id} onChange={id => set('to_account_id', id || null)} exclude={f.account_id} /></Field>
            : <Field label="Category" group><CategorySelect value={f.category_id} kind={f.kind} onChange={v => set('category_id', v)} /></Field>}
        </div>
        <div className="two-col tight">
          <Field label="Description" help="Empty = the template name"><input value={f.description} onChange={e => set('description', e.target.value)} /></Field>
          <Field label="Payee"><input value={f.payee} onChange={e => set('payee', e.target.value)} /></Field>
        </div>
        <Field label="Tags" group><TagInput value={f.tags} onChange={v => set('tags', v)} /></Field>
        <label className="row"><input type="checkbox" checked={f.pinned} onChange={e => set('pinned', e.target.checked)} style={{ width: 'auto' }} /> Pin to the front</label>
        <div className="form-actions">{t.id && <button type="button" className="btn ghost danger-text" onClick={del}>Delete</button>}<span className="spacer" /><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

// ---------------- rules ----------------

const FIELDS = [{ value: 'text', label: 'Description, payee or notes' }, { value: 'description', label: 'Description' }, { value: 'payee', label: 'Payee' }, { value: 'notes', label: 'Notes' }, { value: 'amount', label: 'Amount' }, { value: 'account', label: 'Account' }, { value: 'kind', label: 'Kind' }, { value: 'currency', label: 'Currency paid in' }]
const TEXT_OPS = [{ value: 'contains', label: 'contains' }, { value: 'not_contains', label: 'does not contain' }, { value: 'equals', label: 'is' }, { value: 'starts', label: 'starts with' }, { value: 'ends', label: 'ends with' }, { value: 'regex', label: 'matches regex' }]
const NUM_OPS = [{ value: 'gt', label: 'more than' }, { value: 'lt', label: 'less than' }, { value: 'between', label: 'between' }, { value: 'equals', label: 'exactly' }]

export function Rules() {
  const { catById } = useApp()
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<Rule[]>(`${V1}/rules`), [])
  const [edit, setEdit] = useState<Partial<Rule> | null>(null)
  if (!data) return <Loading />
  const apply = async (r: Rule) => {
    const force = confirm(`Run “${r.name}” over all existing transactions?\n\nOK = also overwrite categories already set · Cancel = only fill empty ones`)
    const res = await api.post<{ changed: number; matched: number }>(`${V1}/rules/${r.id}/apply`, { force })
    toast(`${res.matched} matched, ${res.changed} changed`); reload()
  }
  return (
    <Card title="Rules" actions={<button className="btn primary sm" onClick={() => setEdit({ match: 'all', conditions: [{ field: 'text', op: 'contains', value: '' }], actions: {}, active: true })}>+ Rule</button>}>
      <p className="muted small">Run on every new transaction (typed, imported, from the API), top to bottom. A rule fills the category only when none was chosen, adds tags, sets the payee or renames. Besides rules, Pocket Money already learns: a description you used before gets the category it usually had.</p>
      {data.length === 0 ? <Empty title="No rules yet">e.g. description contains “uber” → Transport.</Empty> : (
        <table className="table compact">
          <thead><tr><th>Rule</th><th>Then</th><th className="num">Hits</th><th /></tr></thead>
          <tbody>{data.map(r => (
            <tr key={r.id} className={`clickable ${r.active ? '' : 'muted'}`} {...clickRow(() => setEdit(r))} title="Edit">
              <td>{r.name}<div className="muted small">{r.conditions.map(c => `${c.field} ${c.op} ${c.value}`).join(r.match === 'any' ? ' or ' : ' and ')}</div></td>
              <td className="small">{[r.actions.category_id ? `${catById(r.actions.category_id)?.icon || ''} ${catById(r.actions.category_id)?.name || 'category'}` : '', ...(r.actions.add_tags || []).map(t => '#' + t), r.actions.payee ? `payee ${r.actions.payee}` : '', r.actions.description ? `rename “${r.actions.description}”` : ''].filter(Boolean).join(' · ')}</td>
              <td className="num">{r.hits}</td>
              <td className="right" {...ownClicks}><button className="btn ghost sm" onClick={() => apply(r)}>Run now</button></td>
            </tr>
          ))}</tbody>
        </table>
      )}
      {edit && <RuleModal rule={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload() }} />}
    </Card>
  )
}

function RuleModal({ rule, onClose, onSaved }: { rule: Partial<Rule>; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [r, setR] = useState<Partial<Rule>>(rule)
  const [preview, setPreview] = useState<{ count: number; items: Transaction[] } | null>(null)
  const conds = r.conditions || []
  const setCond = (i: number, c: Partial<Condition>) => setR({ ...r, conditions: conds.map((x, j) => j === i ? { ...x, ...c } : x) })
  const save = async () => {
    try { if (r.id) await api.put(`${V1}/rules/${r.id}`, r); else await api.post(`${V1}/rules`, r); onSaved() } catch (e) { toast((e as Error).message, 'err') }
  }
  const test = async () => { try { setPreview(await api.post(`${V1}/rules/test`, r)) } catch (e) { toast((e as Error).message, 'err') } }
  const del = async () => { if (r.id && confirm('Delete this rule?')) { await api.del(`${V1}/rules/${r.id}`); onSaved() } }
  return (
    <Modal title={r.id ? 'Edit rule' : 'New rule'} onClose={onClose} size="lg">
      <div className="grid" style={{ gap: 12 }}>
        <Field label="Name"><input value={r.name || ''} onChange={e => setR({ ...r, name: e.target.value })} placeholder="Ride-hailing is transport" /></Field>
        <div className="row wrap">When <Segmented size="sm" value={r.match || 'all'} onChange={v => setR({ ...r, match: v })} options={[{ value: 'all', label: 'all' }, { value: 'any', label: 'any' }]} /> of these hold:</div>
        {conds.map((c, i) => (
          <div key={i} className="row wrap">
            <div style={{ width: 210 }}><Select value={c.field} onChange={v => setCond(i, { field: v, op: v === 'amount' ? 'gt' : v === 'account' || v === 'kind' || v === 'currency' ? 'equals' : 'contains' })} options={FIELDS} /></div>
            {c.field === 'account' ? <div style={{ flex: 1 }}><AccountSelect value={Number(c.value) || null} onChange={id => setCond(i, { value: String(id) })} all /></div>
              : c.field === 'kind' ? <Segmented size="sm" value={c.value || 'expense'} onChange={v => setCond(i, { value: v })} options={[{ value: 'expense', label: 'expense' }, { value: 'income', label: 'income' }, { value: 'transfer', label: 'transfer' }]} />
                : <>
                  {c.field !== 'currency' && <div style={{ width: 160 }}><Select value={c.op} onChange={v => setCond(i, { op: v })} options={c.field === 'amount' ? NUM_OPS : TEXT_OPS} /></div>}
                  <input value={c.value} onChange={e => setCond(i, { value: e.target.value })} style={{ flex: 1, minWidth: 120 }} placeholder={c.field === 'currency' ? 'EUR' : ''} />
                  {c.op === 'between' && <input value={c.value2 || ''} onChange={e => setCond(i, { value2: e.target.value })} style={{ width: 100 }} />}
                </>}
            {conds.length > 1 && <button className="icon-btn" onClick={() => setR({ ...r, conditions: conds.filter((_, j) => j !== i) })}>✕</button>}
          </div>
        ))}
        <button className="btn ghost sm" style={{ justifySelf: 'start' }} onClick={() => setR({ ...r, conditions: [...conds, { field: 'text', op: 'contains', value: '' }] })}>+ condition</button>
        <hr />
        <div className="two-col tight">
          <Field label="Set category" group><CategorySelect value={r.actions?.category_id ?? null} kind="any" onChange={v => setR({ ...r, actions: { ...r.actions, category_id: v } })} /></Field>
          <Field label="Add tags" group><TagInput value={r.actions?.add_tags || []} onChange={v => setR({ ...r, actions: { ...r.actions, add_tags: v } })} /></Field>
        </div>
        <div className="two-col tight">
          <Field label="Set payee"><input value={r.actions?.payee || ''} onChange={e => setR({ ...r, actions: { ...r.actions, payee: e.target.value } })} /></Field>
          <Field label="Rename description to"><input value={r.actions?.description || ''} onChange={e => setR({ ...r, actions: { ...r.actions, description: e.target.value } })} /></Field>
        </div>
        <div className="row wrap"><label className="row"><input type="checkbox" checked={r.active ?? true} onChange={e => setR({ ...r, active: e.target.checked })} style={{ width: 'auto' }} /> Active</label><label className="row"><input type="checkbox" checked={r.stop ?? false} onChange={e => setR({ ...r, stop: e.target.checked })} style={{ width: 'auto' }} /> Stop after this rule</label></div>
        {preview && <div className="callout"><b>{preview.count}</b> transactions from the last year match.{preview.items.length > 0 && <div className="mt-sm" style={{ maxHeight: 240, overflow: 'auto' }}><TxList items={preview.items.slice(0, 12)} onOpen={() => {}} compact /></div>}</div>}
        <div className="form-actions">{r.id && <button className="btn ghost danger-text" onClick={del}>Delete</button>}<span className="spacer" /><button className="btn" onClick={test}>Test</button><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save}>Save</button></div>
      </div>
    </Modal>
  )
}
