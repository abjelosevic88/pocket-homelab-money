import { useEffect, useState } from 'react'
import { useApp } from '../lib/app-context'
import { decimalsOf, fmtMoney, toMajor, toMinor } from '../lib/format'
import { Select } from './Select'

/** Formatted money that knows the currency list. */
export function Money({ minor, code, sign = false, className = '', showCode = false, compact = false }: { minor: number; code: string; sign?: boolean; className?: string; showCode?: boolean; compact?: boolean }) {
  const { currencies } = useApp()
  const tone = sign ? (minor > 0 ? 'ok' : minor < 0 ? 'bad' : 'muted') : ''
  return <span className={`money ${tone} ${className}`}>{fmtMoney(minor, code, decimalsOf(code, currencies), { sign, code: showCode, compact })}</span>
}

/** Text input for an amount in major units; reports minor units (or null). */
export function MoneyInput({ value, code, onChange, autoFocus, placeholder = '0.00', className = '', disabled, ariaLabel }: { value: number | null; code: string; onChange: (minor: number | null) => void; autoFocus?: boolean; placeholder?: string; className?: string; disabled?: boolean; ariaLabel?: string }) {
  const { currencies } = useApp()
  const decimals = decimalsOf(code, currencies)
  const [text, setText] = useState(value == null || value === 0 ? '' : toMajor(value, decimals))
  useEffect(() => { if (value == null || value === 0) { if (text !== '' && toMinor(text, decimals) !== null && toMinor(text, decimals) !== 0) setText('') } else if (toMinor(text, decimals) !== value) setText(toMajor(value, decimals)) }, [value, decimals]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <input className={`money-input ${className}`} inputMode="decimal" value={text} placeholder={placeholder} autoFocus={autoFocus} disabled={disabled} aria-label={ariaLabel}
      onChange={e => { setText(e.target.value); onChange(toMinor(e.target.value, decimals)) }}
      onBlur={() => { const m = toMinor(text, decimals); if (m !== null && m !== 0) setText(toMajor(m, decimals)) }} />
  )
}

const COMMON = ['EUR', 'USD', 'GBP', 'CHF', 'BAM', 'RSD', 'HRK', 'JPY', 'CAD', 'AUD', 'PLN', 'CZK', 'HUF', 'TRY', 'THB']
export function CurrencySelect({ value, onChange, className = '', disabled, ariaLabel = 'Currency' }: { value: string; onChange: (code: string) => void; className?: string; disabled?: boolean; ariaLabel?: string }) {
  const { currencies } = useApp()
  const isCommon = (c: string) => COMMON.includes(c)
  const options = [
    ...COMMON.map(code => currencies.find(x => x.code === code)).filter((c): c is NonNullable<typeof c> => Boolean(c)).map(c => ({ value: 'c:' + c.code, label: c.code, hint: c.name, group: 'Common' })),
    ...currencies.map(c => ({ value: c.code, label: c.code, hint: c.name, group: 'All currencies' })),
  ]
  const cur = value ? (isCommon(value) ? 'c:' + value : value) : ''
  return <Select className={`currency-select ${className}`} value={cur} onChange={v => onChange(v.replace(/^c:/, ''))} options={options} placeholder="Currency…" ariaLabel={ariaLabel} disabled={disabled} />
}

/** Amount + currency as one control. */
export function AmountInput({ amount, currency, onAmount, onCurrency, autoFocus }: { amount: number | null; currency: string; onAmount: (m: number | null) => void; onCurrency: (c: string) => void; autoFocus?: boolean }) {
  return (
    <div className="input-group">
      <MoneyInput value={amount} code={currency} onChange={onAmount} autoFocus={autoFocus} ariaLabel="Amount" />
      <CurrencySelect value={currency} onChange={onCurrency} />
    </div>
  )
}
