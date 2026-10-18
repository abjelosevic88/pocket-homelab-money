import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Kind, Template } from '../lib/types'
import { Card, useAsync } from '../components/ui'
import { QuickAdd } from '../components/QuickAdd'
import { TxEditor, type TxSeed } from '../components/TxEditor'
import { Templates } from './Home'

/** The phone's main screen: type a line, tap a template, or fill the form. */
export default function Add() {
  const [sp] = useSearchParams()
  const navigate = useNavigate()
  const { data: tpls, reload } = useAsync(() => api.get<Template[]>(`${V1}/templates`), [])
  const [seed, setSeed] = useState<TxSeed>({ kind: (sp.get('kind') as Kind) || 'expense' })
  const [key, setKey] = useState(0)
  return (
    <div className="add-page grid" style={{ gap: 14 }}>
      <QuickAdd autoFocus={false} onAdded={() => navigate('/')} />
      {tpls && tpls.length > 0 && <Templates items={tpls.slice(0, 18)} onBooked={() => { reload(); navigate('/') }} onOpen={s => { setSeed(s); setKey(k => k + 1); window.scrollTo({ top: 400, behavior: 'smooth' }) }} />}
      <Card title="Or fill it in">
        <TxEditor key={key} seed={seed} page onSaved={(_, again) => { if (!again) navigate('/') }} onCancel={() => navigate(-1)} />
      </Card>
    </div>
  )
}
