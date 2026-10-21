import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { ago, fmtDateTime } from '../lib/format'
import type { Delivery, Webhook } from '../lib/types'
import { Card, Confirm, Field, Modal, Segmented, clickRow, ownClicks, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'

const EVENT_LABEL: Record<string, string> = {
  'transaction.created': 'Transaction added', 'transaction.updated': 'Transaction changed', 'transaction.deleted': 'Transaction deleted',
  'budget.alert': 'Budget alert', 'subscription.due': 'Bill due',
}

/** Settings → Webhooks: tell other apps the moment something changes. */
export function Webhooks() {
  const { toast } = useToast()
  const { data, reload } = useAsync(() => api.get<{ items: Webhook[]; events: string[] }>(`${V1}/webhooks`), [])
  const [editing, setEditing] = useState<Partial<Webhook> | null>(null)
  const [log, setLog] = useState<Webhook | null>(null)
  const [fresh, setFresh] = useState<Webhook | null>(null)
  const test = async (h: Webhook) => {
    const r = await api.post<{ ok: boolean; status: number; error: string }>(`${V1}/webhooks/${h.id}/test`)
    toast(r.ok ? `Delivered (HTTP ${r.status})` : `Failed: ${r.error || `HTTP ${r.status}`}`, r.ok ? 'ok' : 'err')
    reload()
  }
  return (
    <div className="two-col">
      <Card title="Webhooks" actions={<button type="button" className="btn primary sm" onClick={() => setEditing({})}><Icon name="plus" size={14} />Webhook</button>}>
        {fresh?.secret && <div className="callout success mb"><b>Signing secret for “{fresh.name || fresh.url}” — copy it now, it is shown once.</b><pre className="mono" style={{ margin: '8px 0 0' }}>{fresh.secret}</pre></div>}
        {!data ? null : data.items.length === 0 ? <p className="muted">None yet. Add one to push changes to Pocket Day, n8n, Home Assistant or a script instead of having them poll.</p> : (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Events</th><th>Last delivery</th><th /></tr></thead>
            <tbody>{data.items.map(h => (
              <tr key={h.id} className={`clickable ${h.active ? '' : 'muted'}`} {...clickRow(() => setEditing(h))}>
                <td><b>{h.name || 'Webhook'}</b><div className="muted small mono" style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis' }}>{h.url}</div></td>
                <td className="small">{h.events.length === data.events.length ? 'everything' : h.events.map(e => EVENT_LABEL[e] || e).join(', ')}{h.format === 'firefly' ? ' · Firefly format' : ''}</td>
                <td className="small">{h.last_at ? <><span className={h.last_status >= 200 && h.last_status < 300 ? 'ok' : 'bad'}>{h.last_status || 'no answer'}</span> · {ago(h.last_at)}</> : <span className="muted">never</span>}</td>
                <td className="right" {...ownClicks}><button type="button" className="btn ghost sm" onClick={() => test(h)}>Test</button><button type="button" className="btn ghost sm" onClick={() => setLog(h)}>Log</button></td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </Card>
      <Card title="How they are sent">
        <p className="muted">A <code>POST</code> with a JSON body <code>{'{"id", "event", "created_at", "data"}'}</code> — <code>data.transaction</code> is the transaction as the API returns it (for deletes, the last state). Failed deliveries are retried after 5 and 30 seconds.</p>
        <p className="muted">Every request carries <code>X-Pocket-Event</code> and <code>X-Pocket-Signature: sha256=&lt;HMAC-SHA256 of the body with the secret&gt;</code> — check it before trusting the payload.</p>
        <p className="muted"><b>Firefly format</b> sends Firefly III's webhook body (<code>trigger</code>, <code>response</code>, <code>content</code> = the transaction group) and its <code>Signature: t=…,v1=…</code> header, so a receiver written for Firefly works unchanged. It carries only the transaction events.</p>
      </Card>
      {editing && data && <WebhookForm hook={editing} events={data.events} onClose={() => setEditing(null)} onSaved={h => { setEditing(null); if (h.secret && !h.secret.includes('•')) setFresh(h); reload() }} />}
      {log && <DeliveryLog hook={log} onClose={() => setLog(null)} />}
    </div>
  )
}

function WebhookForm({ hook, events, onClose, onSaved }: { hook: Partial<Webhook>; events: string[]; onClose: () => void; onSaved: (h: Webhook) => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({
    name: hook.name || '', url: hook.url || '', secret: hook.secret || '', format: hook.format || 'native', active: hook.active ?? true,
    events: hook.events || ['transaction.created', 'transaction.updated', 'transaction.deleted'],
  })
  const [del, setDel] = useState(false)
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }))
  const toggle = (e: string) => set('events', f.events.includes(e) ? f.events.filter(x => x !== e) : [...f.events, e])
  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    try {
      const h = hook.id ? await api.put<Webhook>(`${V1}/webhooks/${hook.id}`, f) : await api.post<Webhook>(`${V1}/webhooks`, f)
      toast('Saved'); onSaved(h)
    } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title={hook.id ? `Edit ${hook.name || 'webhook'}` : 'New webhook'} onClose={onClose}>
      <form onSubmit={save} className="grid" style={{ gap: 12 }}>
        <Field label="Name"><input value={f.name} onChange={e => set('name', e.target.value)} placeholder="Pocket Day, n8n…" autoFocus /></Field>
        <Field label="URL"><input value={f.url} onChange={e => set('url', e.target.value)} placeholder="https://…" required /></Field>
        <Field label="Events" group>
          <div className="grid" style={{ gap: 6 }}>{events.map(e => (
            <label key={e} className="row"><input type="checkbox" checked={f.events.includes(e)} onChange={() => toggle(e)} style={{ width: 'auto' }} disabled={f.format === 'firefly' && !e.startsWith('transaction.')} /> {EVENT_LABEL[e] || e} <span className="muted small mono">{e}</span></label>
          ))}</div>
        </Field>
        <Field label="Format" group>
          <Segmented value={f.format} onChange={v => set('format', v)} options={[{ value: 'native', label: 'Pocket Money' }, { value: 'firefly', label: 'Firefly III' }]} />
        </Field>
        <Field label="Signing secret" help={hook.id ? 'Leave as is to keep it; type a new one to replace it.' : 'Leave empty to generate one.'}>
          <input value={f.secret} onChange={e => set('secret', e.target.value)} className="mono" placeholder="generated" />
        </Field>
        {hook.id && <label className="row"><input type="checkbox" checked={f.active} onChange={e => set('active', e.target.checked)} style={{ width: 'auto' }} /> Active</label>}
        <div className="form-actions">
          {hook.id && <button type="button" className="btn ghost danger-text" onClick={() => setDel(true)}>Delete</button>}
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button>
        </div>
      </form>
      {del && <Confirm title="Delete this webhook?" message="Its delivery log goes with it." onCancel={() => setDel(false)} onConfirm={async () => { await api.del(`${V1}/webhooks/${hook.id}`); onSaved(hook as Webhook) }} />}
    </Modal>
  )
}

function DeliveryLog({ hook, onClose }: { hook: Webhook; onClose: () => void }) {
  const { data } = useAsync(() => api.get<Delivery[]>(`${V1}/webhooks/${hook.id}/deliveries`), [hook.id])
  return (
    <Modal title={`Deliveries · ${hook.name || hook.url}`} onClose={onClose} size="lg">
      {!data ? null : data.length === 0 ? <p className="muted">Nothing sent yet.</p> : (
        <table className="table compact">
          <thead><tr><th>When</th><th>Event</th><th>What</th><th className="num">Status</th><th className="num">Tries</th><th className="num">Took</th></tr></thead>
          <tbody>{data.map(d => (
            <tr key={d.id}>
              <td className="small">{fmtDateTime(d.created_at)}</td><td className="small mono">{d.event}</td>
              <td className="small">{d.subject}{d.error && <div className="bad">{d.error}</div>}</td>
              <td className={`num ${d.status >= 200 && d.status < 300 ? 'ok' : 'bad'}`}>{d.status || '—'}</td><td className="num">{d.attempts}</td><td className="num">{d.duration_ms} ms</td>
            </tr>
          ))}</tbody>
        </table>
      )}
    </Modal>
  )
}
