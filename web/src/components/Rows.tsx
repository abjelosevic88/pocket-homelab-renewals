// The rows every list shares: a renewal with its date, and a past renewal.
import { Link } from 'react-router-dom'
import { GROUP_NAME, KIND_ICON, SOURCE_ICON, SOURCE_NAME, dueText, fromApp, money } from '../lib/renewals'
import { fmtDate } from '../lib/format'
import type { History, Renewal } from '../lib/types'
import { Icon } from './icons'

export function StatusPill({ r }: { r: Pick<Renewal, 'status' | 'days_left' | 'expires_on' | 'auto_renews'> }) {
  const icon = r.status === 'expired' ? 'alert' : r.status === 'soon' ? 'clock' : r.status === 'undated' ? 'help' : r.status === 'archived' ? 'archive' : r.auto_renews ? 'repeat' : 'calendar'
  return <span className={`pill st-${r.status}`}><Icon name={icon} size={12} />{dueText(r)}</span>
}

export function RenewalRow({ r, showGroup }: { r: Renewal; showGroup?: boolean }) {
  const sub = [r.holder, r.provider, showGroup && GROUP_NAME[r.group], r.expires_on && r.days_left > 60 ? '' : r.expires_on && fmtDate(r.expires_on)].filter(Boolean).join(' · ')
  return (
    <Link to={`/r/${r.id}`} className={`list-row ${r.archived ? 'is-archived' : ''}`}>
      <span className={`list-ico gr-${r.group}`}><Icon name={KIND_ICON[r.kind] || 'hourglass'} size={16} /></span>
      <span className="grow row-main">
        <span className="row-title"><span className="ellipsis">{r.title}</span>{r.needs_check && <span className="pill check" title={r.found_by}>Check</span>}</span>
        <span className="row-sub muted small">
          {fromApp(r) && <span className="src-chip" title={`Found in ${SOURCE_NAME[r.source]}`}><Icon name={SOURCE_ICON[r.source] || 'layers'} size={11} /></span>}
          <span className="ellipsis">{sub || ' '}</span>
        </span>
      </span>
      <StatusPill r={r} />
    </Link>
  )
}

export function HistoryRow({ h, showTitle }: { h: History; showTitle?: boolean }) {
  const sub = [fmtDate(h.renewed_on), h.old_expires && `was until ${fmtDate(h.old_expires)}`, h.note].filter(Boolean).join(' · ')
  const row = (
    <>
      <span className="list-ico st-ok"><Icon name={h.source === 'auto' ? 'repeat' : 'check'} size={16} /></span>
      <span className="grow row-main"><span className="row-title">{showTitle && h.title ? h.title : `Until ${fmtDate(h.new_expires)}`}</span><span className="muted small ellipsis">{showTitle ? `until ${fmtDate(h.new_expires)} · ` : ''}{sub}</span></span>
      {h.money_tx_id && <Icon name="wallet" size={14} className="muted" title="Booked in Pocket Money" />}
      <span className="tnum row-amount">{h.cost ? money(h.cost, h.currency) : ''}</span>
    </>
  )
  return showTitle ? <Link to={`/r/${h.renewal_id}`} className="list-row">{row}</Link> : <div className="list-row">{row}</div>
}
