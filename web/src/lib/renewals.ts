// Names, icons and formatting for Pocket Renewals' own words.
import type { Group, Kind, Renewal, Status, Unit } from './types'
import { fmtDate } from './format'

// Kinds and their defaults; the server has the same list (store/kinds.go).
export const KINDS: Kind[] = [
  { value: 'passport', label: 'Passport', group: 'identity', remind: [270, 180, 90, 30], term_n: 10, term_u: 'year', lead: 42, travel: true },
  { value: 'id_card', label: 'ID card', group: 'identity', remind: [90, 30, 7], term_n: 10, term_u: 'year', lead: 21, travel: true },
  { value: 'driving_licence', label: 'Driving licence', group: 'identity', remind: [90, 30, 7], term_n: 10, term_u: 'year', lead: 21, travel: false },
  { value: 'visa', label: 'Visa or residence permit', group: 'identity', remind: [120, 60, 30, 7], term_n: 1, term_u: 'year', lead: 30, travel: true },
  { value: 'insurance', label: 'Insurance', group: 'insurance', remind: [45, 14, 3], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'vehicle', label: 'Vehicle registration or inspection', group: 'vehicle', remind: [30, 14, 3], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'warranty', label: 'Warranty', group: 'warranty', remind: [30, 7], term_n: 0, term_u: 'year', lead: 0, travel: false },
  { value: 'domain', label: 'Domain name', group: 'online', remind: [60, 30, 7, 1], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'certificate', label: 'TLS certificate', group: 'online', remind: [21, 7, 2], term_n: 90, term_u: 'day', lead: 0, travel: false },
  { value: 'subscription', label: 'Subscription', group: 'subscription', remind: [14, 3], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'membership', label: 'Membership or season ticket', group: 'subscription', remind: [30, 7], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'contract', label: 'Contract (phone, energy, lease)', group: 'contract', remind: [60, 30, 7], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'health', label: 'Health (vaccination, prescription, check-up)', group: 'health', remind: [30, 7], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'licence', label: 'Licence or permit', group: 'other', remind: [60, 30, 7], term_n: 1, term_u: 'year', lead: 0, travel: false },
  { value: 'other', label: 'Something else', group: 'other', remind: [30, 7], term_n: 1, term_u: 'year', lead: 0, travel: false },
]
export const kindOf = (v: string): Kind => KINDS.find(k => k.value === v) || KINDS[KINDS.length - 1]

export const KIND_ICON: Record<string, string> = {
  passport: 'globe', id_card: 'user', driving_licence: 'route', visa: 'plane', insurance: 'shield', vehicle: 'route', warranty: 'package',
  domain: 'globe', certificate: 'lock', subscription: 'repeat', membership: 'star', contract: 'pen', health: 'heart-pulse', licence: 'flag', other: 'hourglass',
}

export const GROUPS: { value: Group; label: string; icon: string }[] = [
  { value: 'identity', label: 'Identity and travel', icon: 'user' },
  { value: 'insurance', label: 'Insurance', icon: 'shield' },
  { value: 'vehicle', label: 'Vehicles', icon: 'route' },
  { value: 'warranty', label: 'Warranties', icon: 'package' },
  { value: 'online', label: 'Domains and certificates', icon: 'globe' },
  { value: 'subscription', label: 'Subscriptions', icon: 'repeat' },
  { value: 'contract', label: 'Contracts', icon: 'pen' },
  { value: 'health', label: 'Health', icon: 'heart-pulse' },
  { value: 'other', label: 'Other', icon: 'hourglass' },
]
export const GROUP_NAME: Record<string, string> = Object.fromEntries(GROUPS.map(g => [g.value, g.label]))

export const SOURCE_NAME: Record<string, string> = {
  documents: 'Pocket Documents', home: 'Pocket Home', garage: 'Pocket Garage', inventory: 'Pocket Inventory', monitoring: 'Pocket Monitoring', wealth: 'Pocket Wealth', pets: 'Pocket Pets',
  money: 'Pocket Money', email: 'Pocket Email', web: 'Added here', demo: 'Example', import: 'Imported', api: 'API', auto: 'By itself',
}
export const SOURCE_ICON: Record<string, string> = {
  documents: 'file', home: 'home', garage: 'route', inventory: 'package', monitoring: 'activity', money: 'wallet', wealth: 'trending-up', pets: 'heart', email: 'mail', travel: 'plane',
  calendar: 'calendar', tasks: 'check-square', contacts: 'users',
}
export const fromApp = (r: Pick<Renewal, 'source'>) => ['documents', 'home', 'garage', 'inventory', 'monitoring', 'money', 'wealth', 'pets', 'email'].includes(r.source)

export const UNITS: { value: Unit; label: string }[] = [{ value: 'day', label: 'days' }, { value: 'week', label: 'weeks' }, { value: 'month', label: 'months' }, { value: 'year', label: 'years' }]

/** "Every 10 years", "Every year", "No usual term". */
export function termText(n: number, unit: string): string {
  if (!n) return 'No usual term'
  if (n === 1) return `Every ${unit}`
  return `Every ${n} ${unit}s`
}

export const STATUS_NAME: Record<Status, string> = { expired: 'Expired', soon: 'Coming up', ok: 'Later', undated: 'No date', archived: 'Archived' }

/** "Expired 3 days ago", "Today", "In 12 days", "In 4 months", "On 4 Mar 2031". */
export function dueText(r: Pick<Renewal, 'status' | 'days_left' | 'expires_on'>): string {
  if (r.status === 'archived') return 'Archived'
  if (r.status === 'undated' || !r.expires_on) return 'No date'
  const d = r.days_left
  if (d < -1) return d > -60 ? `Expired ${-d} days ago` : `Expired ${fmtDate(r.expires_on, true)}`
  if (d === -1) return 'Expired yesterday'
  if (d === 0) return 'Runs out today'
  if (d === 1) return 'Tomorrow'
  if (d <= 60) return `In ${d} days`
  if (d <= 365) return `In ${Math.round(d / 30.4)} months`
  return fmtDate(r.expires_on, true)
}

/** Minor units → "€1,234.50" in the currency. */
export function money(minor: number | null | undefined, currency = 'EUR', whole = false): string {
  const v = (minor || 0) / 100
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency, minimumFractionDigits: whole ? 0 : 2, maximumFractionDigits: whole ? 0 : 2 }).format(v)
  } catch {
    return `${v.toFixed(whole ? 0 : 2)} ${currency}`
  }
}

/** A typed amount ("12,50" or "12.50") → minor units, null when empty or bad. */
export function parseMoney(s: string): number | null {
  const t = s.trim().replace(/\s/g, '').replace(/,(\d{1,2})$/, '.$1').replace(/,/g, '')
  if (!t) return null
  const n = Number(t)
  if (!Number.isFinite(n) || n < 0) return null
  return Math.round(n * 100)
}
export const moneyInput = (minor: number | null | undefined) => minor == null || minor === 0 ? '' : (minor / 100).toFixed(2)

/** Adds n units to a YYYY-MM-DD date, keeping the day (clamped at month end), like the server. */
export function addInterval(iso: string, n: number, unit: Unit): string {
  if (!iso || !n) return iso
  const [y, m, d] = iso.split('-').map(Number)
  if (unit === 'day' || unit === 'week') {
    const t = new Date(Date.UTC(y, m - 1, d + (unit === 'week' ? 7 * n : n)))
    return t.toISOString().slice(0, 10)
  }
  const months = unit === 'year' ? 12 * n : n
  const total = (m - 1) + months
  const ny = y + Math.floor(total / 12), nm = ((total % 12) + 12) % 12
  const last = new Date(Date.UTC(ny, nm + 1, 0)).getUTCDate()
  return `${ny}-${String(nm + 1).padStart(2, '0')}-${String(Math.min(d, last)).padStart(2, '0')}`
}

/** "Me", "Ana" → "Ana's passport"-style names for a renewal. */
export function nameOf(r: Pick<Renewal, 'title' | 'holder'>): string {
  if (r.holder && !r.title.toLowerCase().includes(r.holder.toLowerCase())) return `${r.title} · ${r.holder}`
  return r.title
}
