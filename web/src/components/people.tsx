import type { User } from '../lib/types'
import { colorFor, initials } from '../lib/format'
export function Avatar({ user, size = '' }: { user: User; size?: '' | 'lg' }) {
  const name = user.name || user.email
  return <span className={`avatar ${size}`} style={{ background: colorFor(user.id) }} title={name}>{initials(name)}</span>
}
