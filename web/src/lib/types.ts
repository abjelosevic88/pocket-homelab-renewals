export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'
export type AppSlug = 'documents' | 'home' | 'garage' | 'inventory' | 'monitoring' | 'money' | 'wealth' | 'pets' | 'email' | 'travel' | 'calendar' | 'tasks' | 'contacts'

type ConnFields = { [K in AppSlug as `${K}_mode`]: ConnMode } & { [K in AppSlug as `${K}_via`]: ConnVia } & { [K in AppSlug as `${K}_detail` | `${K}_url` | `${K}_token` | `${K}_public_url` | `${K}_open_url`]: string } & { [K in AppSlug as `${K}_configured`]: boolean }

export type Settings = ConnFields & {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number; currency: string; soon_days: number
  notify_reminders: boolean; notify_expired: boolean; notify_found: boolean; notify_hour: number
  notify_url: string; notify_format: string; notify_configured: boolean
  live: boolean; scan_nightly: boolean; scan_sources: Record<string, boolean>; documents_ai: boolean; email_days: number; travel_months: number
  money_book: boolean; money_account_id: number; money_category_id: number
  calendar_sync: boolean; calendar_id: number
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string
}

export type Status = 'expired' | 'soon' | 'ok' | 'undated' | 'archived'
export type Unit = 'day' | 'week' | 'month' | 'year'
export type Group = 'identity' | 'insurance' | 'vehicle' | 'warranty' | 'online' | 'subscription' | 'contract' | 'health' | 'other'

export interface Renewal {
  id: number; title: string; kind: string; holder: string; provider: string; number: string; issued_on: string; expires_on: string
  renew_n: number; renew_unit: Unit; auto_renews: boolean; remind_days: string; lead_days: number; cost: number | null; currency: string
  renew_url: string; notes: string; document_id: number | null; document_title: string; contact_id: number | null; contact_name: string
  archived: boolean; needs_check: boolean; source: string; source_ref: string; source_url: string; found_by: string
  cal_event_id: number | null; task_id: number | null; created_at: string; updated_at: string
  group: Group; status: Status; days_left: number; act_by: string; renewals: number
  document_url?: string; open_url?: string
}

export interface History {
  id: number; renewal_id: number; renewed_on: string; old_expires: string; new_expires: string; cost: number; currency: string; note: string
  money_tx_id: number | null; source: string; created_at: string; title?: string
}

export interface Kind { value: string; label: string; group: Group; remind: number[]; term_n: number; term_u: Unit; lead: number; travel: boolean }

export interface TravelWarning {
  trip_id: number; trip: string; destination: string; start: string; end: string; trip_url?: string
  renewal_id: number; title: string; holder: string; kind: string; expires_on: string; level: 'before' | 'margin'; message: string
}

export interface Overview {
  counts: { total: number; expired: number; soon: number; check: number; undated: number; auto: number }
  groups: Record<string, number>; window: number
  expired: Renewal[]; soon: Renewal[]; check: Renewal[]; later: Renewal[]; recent: History[]; travel: TravelWarning[]; year_cost: Record<string, number>
}

export interface Scan { id: number; source: string; trigger: 'manual' | 'nightly' | 'weekly' | 'event'; started_at: string; finished_at: string; looked: number; found: number; added: number; updated: number; error: string }

export interface Source {
  name: string; label: string; what: string; ai: '' | 'optional' | 'required'; app: AppSlug; mode: ConnMode; connected: boolean; detail: string
  included: boolean; running: boolean; last: Scan | null; count: number; ai_ready: boolean; open_url: string
}

export interface Sources { sources: Source[]; live: boolean; scan_nightly: boolean; documents_ai: boolean; stream: { configured: boolean; connected: boolean; error: string; last_event_at?: string } }

export interface RenewalDetail { renewal: Renewal; history: History[]; reminders: number[]; next_expires: string; travel: TravelWarning[] }
export interface MoneyResult { ok: boolean; tx_id?: number; account?: string; url?: string; via?: string; error?: string; skipped?: boolean }

export interface Picked<T> { connected: boolean; detail?: string; items: T[] }
export interface MoneyAccount { id: number; name: string; kind: string; currency: string; everyday: boolean }
export interface MoneyCategory { id: number; name: string; full_name: string }
export interface CalendarItem { id: number; name: string; color: string; writable: boolean; is_default: boolean }
export interface ContactItem { id: number; fn: string; org: string; title: string; emails: string[] | null; phones: string[] | null }
export interface DocumentItem { id: number; title: string; original_name: string; created: string; added_at: string; correspondent_name: string; document_type_name: string }
