import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['h'], 'Home'], [['t'], 'Transactions'], [['a'], 'Accounts'], [['b'], 'Budgets'], [['s'], 'Subscriptions & bills'], [['g'], 'Goals'], [['r'], 'Reports'], [[','], 'Settings']] },
  { title: 'Add', keys: [[['n'], 'Expense'], [['i'], 'Income'], [['x'], 'Transfer']] },
  { title: 'Search', keys: [[['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu']] },
  { title: 'Help', keys: [[['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
      <p className="muted small mt">A new transaction goes to the account you have open; quick entry on Home understands “kafa 3,5”.</p>
    </Modal>
  )
}
