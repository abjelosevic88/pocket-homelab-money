import type { User } from '../lib/types'
import { colorFor, initials } from '../lib/format'
export function Avatar({ user, size = '' }: { user: User; size?: '' | 'lg' | number }) {
  const name = user.name || user.email
  const px = typeof size === 'number' ? size : size === 'lg' ? 30 : 22
  return <span className="avatar" style={{ background: colorFor(user.id), width: px, height: px, fontSize: Math.round(px * 0.42) }} title={name}>{initials(name)}</span>
}
