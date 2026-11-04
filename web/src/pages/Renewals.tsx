// Everything that runs out, by month; one renewal with its dates, reminders, history and links to the other apps.
import type React from 'react'
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange, useData, useOnChange } from '../lib/data'
import { fmtDate, fmtDateTime } from '../lib/format'
import { GROUPS, GROUP_NAME, KIND_ICON, SOURCE_ICON, SOURCE_NAME, fromApp, kindOf, money, termText } from '../lib/renewals'
import type { Renewal, RenewalDetail } from '../lib/types'
import { Card, Confirm, CoreHint, Empty, Loading, errMsg, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { ContactPickModal, DocumentPickModal, RenewModal, RenewalModal } from '../components/Forms'
import { HistoryRow, RenewalRow, StatusPill } from '../components/Rows'
import { Select } from '../components/Select'
import { openCreate, useCrumb } from '../components/Layout'
import { TravelRow } from './Overview'

const STATUSES: { value: string; label: string }[] = [
  { value: '', label: 'All' }, { value: 'expired', label: 'Expired' }, { value: 'soon', label: 'Coming up' }, { value: 'check', label: 'To check' },
  { value: 'undated', label: 'No date' }, { value: 'archived', label: 'Archived' },
]

function monthKey(r: Renewal): string {
  if (r.status === 'archived') return 'Archived'
  if (!r.expires_on) return 'No date yet'
  if (r.status === 'expired') return 'Expired'
  const [y, m] = r.expires_on.split('-').map(Number)
  const label = new Date(y, m - 1, 1).toLocaleDateString(undefined, { month: 'long', year: 'numeric' })
  return r.days_left > 730 ? `${y}` : label
}

export default function Renewals() {
  const [sp, setSp] = useSearchParams()
  const { holders, overview } = useData()
  const [q, setQ] = useState('')
  const status = sp.get('status') || ''
  const group = sp.get('group') || ''
  const holder = sp.get('holder') || ''
  const source = sp.get('source') || ''
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  const { data, reload, error } = useAsync(() => api.get<Renewal[]>(`${V1}/renewals?${new URLSearchParams({ q, status, group, holder, source })}`), [q, status, group, holder, source])
  useOnChange(reload)
  const groups = new Map<string, Renewal[]>()
  for (const r of data || []) groups.set(monthKey(r), [...(groups.get(monthKey(r)) || []), r])
  const counts: Record<string, number | undefined> = { expired: overview?.counts.expired, soon: overview?.counts.soon, check: overview?.counts.check, undated: overview?.counts.undated }
  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow"><h1 className="page-title">{group ? GROUP_NAME[group] : source ? `Found in ${SOURCE_NAME[source] || source}` : 'Everything'}</h1><p className="muted">Soonest first. Each one reminds you before its date; renew it and the date moves on.</p></div>
        <button type="button" className="btn primary" onClick={() => openCreate('renewal')}><Icon name="plus" size={15} />Add</button>
      </div>
      <div className="chips" role="tablist" aria-label="Show">
        {STATUSES.map(s => <button key={s.value} type="button" role="tab" aria-selected={status === s.value} className={`chip ${status === s.value ? 'active' : ''}`} onClick={() => set('status', s.value)}>
          {s.label}{counts[s.value] ? <span className="count">{counts[s.value]}</span> : null}</button>)}
      </div>
      <div className="row wrap filters">
        <input className="search-in" value={q} onChange={e => setQ(e.target.value)} placeholder="Find by name, whose, provider, number…" aria-label="Find" />
        <Select ariaLabel="Kind" value={group} onChange={v => set('group', v)} clearLabel="Every kind" placeholder="Every kind" options={GROUPS.map(g => ({ value: g.value, label: g.label }))} />
        {holders.length > 1 && <Select ariaLabel="Whose" value={holder} onChange={v => set('holder', v)} clearLabel="Everyone's" placeholder="Everyone's" options={holders.map(h => ({ value: h, label: h }))} />}
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <Loading /> : data.length === 0 ? (
        <Empty title={q || status || group || holder ? 'Nothing matches' : 'Nothing here yet'} icon="hourglass">{!q && !status && !group && <>Add a passport, an insurance policy or a domain, or <Link to="/find">find them in your apps</Link>.</>}</Empty>
      ) : (
        <Card flush>
          <div className="list">{[...groups.entries()].map(([g, list]) => (
            <div key={g}><div className="month-h">{g} <span className="count">{list.length}</span></div>{list.map(r => <RenewalRow key={r.id} r={r} showGroup={!group} />)}</div>
          ))}</div>
        </Card>
      )}
    </div>
  )
}

export function RenewalPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { settings } = useApp()
  const { toast } = useToast()
  const { data, reload, error } = useAsync(() => api.get<RenewalDetail>(`${V1}/renewals/${id}`), [id])
  useOnChange(reload)
  const [modal, setModal] = useState<'' | 'edit' | 'renew' | 'doc' | 'contact' | 'delete'>('')
  const [busy, setBusy] = useState(false)
  useCrumb(data?.renewal.title)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const r = data.renewal
  const k = kindOf(r.kind)
  const coreOn = !!settings && settings.core_configured && settings.core_registered !== false
  const put = async (body: Record<string, unknown>, msg = 'Saved') => { try { await api.put(`${V1}/renewals/${r.id}`, body); toast(msg); announceChange() } catch (e) { toast(errMsg(e), 'err') } }
  const task = async () => {
    setBusy(true)
    try { const t = await api.post<{ task_id: number; url: string }>(`${V1}/renewals/${r.id}/task`); toast('Added to your tasks'); reload(); if (t.url) window.open(t.url, '_blank', 'noreferrer') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const tasksReady = !!settings && settings.tasks_via !== ''
  const docsReady = !!settings && settings.documents_via !== ''
  const contactsReady = !!settings && settings.contacts_via !== ''
  const rows: [string, React.ReactNode][] = [
    ['Kind', k.label], ['Whose', r.holder], ['Issued by', r.provider], ['Number', r.number && <code key="n">{r.number}</code>],
    ['Issued on', r.issued_on && fmtDate(r.issued_on)], ['Usual term', r.renew_n ? termText(r.renew_n, r.renew_unit) + (r.auto_renews ? ', renews by itself' : '') : r.auto_renews ? 'Renews by itself' : ''],
    ['A renewal costs', r.cost != null && r.cost > 0 ? money(r.cost, r.currency) : ''],
    ['Act by', r.act_by && <span key="a" className={r.act_by < new Date().toISOString().slice(0, 10) ? 'warn-text' : ''}>{fmtDate(r.act_by)} <span className="muted">({r.lead_days} days ahead)</span></span>],
    ['Renew at', r.renew_url && <a key="u" href={r.renew_url} target="_blank" rel="noreferrer">{r.renew_url.replace(/^https?:\/\//, '').split('/')[0]} <Icon name="external" size={12} /></a>],
  ]
  return (
    <div className="stack loose">
      <div className="detail-head">
        <span className={`detail-ico gr-${r.group}`}><Icon name={KIND_ICON[r.kind] || 'hourglass'} size={22} /></span>
        <div className="grow">
          <h1 className="page-title">{r.title}{r.archived && <span className="badge">archived</span>}</h1>
          <p className="muted">{[r.holder, GROUP_NAME[r.group]].filter(Boolean).join(' · ')}</p>
        </div>
      </div>
      <div className="row wrap toolbar">
        <button type="button" className="btn primary" onClick={() => setModal('renew')}><Icon name="refresh" size={15} />Renewed</button>
        <button type="button" className="btn" onClick={task} disabled={busy || !tasksReady || !!r.task_id} title={r.task_id ? 'Already on your task list' : tasksReady ? 'A task due by the last day to act' : settings?.tasks_detail || 'Connect Pocket Tasks in Settings → Connected apps'}>
          <Icon name="check-square" size={15} />{r.task_id ? 'On my tasks' : 'Add to my tasks'}</button>
        <span className="grow" />
        <button type="button" className="btn ghost" onClick={() => setModal('edit')}><Icon name="edit" size={15} />Edit</button>
        <button type="button" className="btn ghost danger-text" onClick={() => setModal('delete')}><Icon name="trash" size={15} />Delete</button>
      </div>
      {!tasksReady && !coreOn && settings?.tasks_mode === 'core' && <CoreHint />}
      {r.needs_check && <div className="callout info"><Icon name="sparkles" size={15} /><div className="grow">Added by itself from {SOURCE_NAME[r.source] || r.source}: {r.found_by}. Check the date and the details.</div>
        <button type="button" className="btn sm" onClick={() => put({ needs_check: false }, 'Marked as checked')}><Icon name="check" size={14} />It is right</button></div>}
      {data.travel.map(w => <div key={w.trip_id} className={`callout ${w.level === 'before' ? 'danger' : 'warn'}`}><Icon name="plane" size={15} /><div>{w.message}</div></div>)}
      <div className="two-grid">
        <div className="stack">
          <Card>
            <div className="date-card">
              <span className="muted small">{r.expires_on ? (r.status === 'expired' ? 'Ran out on' : 'Runs out on') : 'Runs out'}</span>
              <span className="big-date">{r.expires_on ? fmtDate(r.expires_on) : 'Not known yet'}</span>
              <div className="row wrap" style={{ gap: 8, marginTop: 6 }}><StatusPill r={r} />{data.next_expires && r.expires_on && <span className="muted small">Renewed, it would run to {fmtDate(data.next_expires)}</span>}</div>
              {r.expires_on && !r.archived && <div className="mt"><span className="muted small">Reminders</span><div className="reminder-list mt">{data.reminders.map(d => <span key={d} className="pill">{d === 0 ? 'on the day' : `${d} days before`}</span>)}</div></div>}
            </div>
          </Card>
          <Card title="Details">
            <dl className="set-dl">{rows.filter(([, v]) => v).map(([kk, v]) => <div key={kk}><dt>{kk}</dt><dd>{v}</dd></div>)}</dl>
            {r.notes && <p className="notes">{r.notes}</p>}
          </Card>
          <Card title="History" sub={data.history.length ? `Renewed ${data.history.length} ${data.history.length === 1 ? 'time' : 'times'}` : undefined} flush>
            {data.history.length === 0 ? <p className="muted pad">Not renewed here yet. “Renewed” keeps the old date, the new one and what it cost.</p>
              : <div className="list">{data.history.map(h => <HistoryRow key={h.id} h={h} />)}</div>}
          </Card>
        </div>
        <div className="stack">
          <Card title="In your other apps" flush>
            <div className="list">
              <div className="list-row">
                <span className="list-ico"><Icon name="file" size={16} /></span>
                <span className="grow row-main">{r.document_id ? <>{r.document_url ? <a href={r.document_url} target="_blank" rel="noreferrer" className="row-title">{r.document_title || `Document ${r.document_id}`}</a> : <span className="row-title">{r.document_title || `Document ${r.document_id}`}</span>}<span className="muted small">The paper, in Pocket Documents</span></>
                  : <><span className="row-title">The paper</span><span className="muted small">{docsReady ? 'Keep the scan from Pocket Documents with it' : 'Connect Pocket Documents in Settings → Connected apps'}</span></>}</span>
                {r.document_id ? <button type="button" className="btn ghost sm icon-only" onClick={() => put({ document_id: null }, 'Removed')} aria-label="Remove the document" title="Remove"><Icon name="x" size={14} /></button>
                  : <button type="button" className="btn sm" disabled={!docsReady} onClick={() => setModal('doc')}>Choose</button>}
              </div>
              <div className="list-row">
                <span className="list-ico"><Icon name="users" size={16} /></span>
                <span className="grow row-main">{r.contact_id ? <><span className="row-title">{r.contact_name}</span><span className="muted small">Who to call, in Pocket Contacts</span></>
                  : <><span className="row-title">Who to call</span><span className="muted small">{contactsReady ? 'The agent, the registrar, the office' : 'Connect Pocket Contacts in Settings → Connected apps'}</span></>}</span>
                {r.contact_id ? <button type="button" className="btn ghost sm icon-only" onClick={() => put({ contact_id: null }, 'Removed')} aria-label="Remove the contact" title="Remove"><Icon name="x" size={14} /></button>
                  : <button type="button" className="btn sm" disabled={!contactsReady} onClick={() => setModal('contact')}>Choose</button>}
              </div>
              <div className="list-row">
                <span className="list-ico"><Icon name="calendar" size={16} /></span>
                <span className="grow row-main"><span className="row-title">Pocket Calendar</span><span className="muted small">{r.cal_event_id ? 'On your calendar on the day it runs out' : settings?.calendar_sync ? 'Goes on your calendar within a few minutes' : 'Switch on “Show on my calendar” in Settings → Connected apps'}</span></span>
                {r.cal_event_id ? <Icon name="check" size={15} className="ok" /> : null}
              </div>
              {fromApp(r) && <div className="list-row">
                <span className="list-ico"><Icon name={SOURCE_ICON[r.source] || 'layers'} size={16} /></span>
                <span className="grow row-main"><span className="row-title">Found in {SOURCE_NAME[r.source]}</span><span className="muted small">{r.found_by}; the date moves on here when it changes there.</span></span>
                {r.source_url && <a href={r.source_url} target="_blank" rel="noreferrer" className="btn sm">Open<Icon name="external" size={12} /></a>}
              </div>}
            </div>
          </Card>
          <p className="muted small">Added {fmtDateTime(r.created_at)}{r.updated_at !== r.created_at ? ` · changed ${fmtDateTime(r.updated_at)}` : ''}</p>
        </div>
      </div>
      {modal === 'edit' && <RenewalModal renewal={r} onClose={() => setModal('')} onSaved={reload} />}
      {modal === 'renew' && <RenewModal renewal={r} next={data.next_expires} onClose={() => setModal('')} onDone={reload} />}
      {modal === 'doc' && <DocumentPickModal onClose={() => setModal('')} onPick={d => { setModal(''); put({ document_id: d.id, document_title: d.title || d.original_name }, 'Kept with it') }} />}
      {modal === 'contact' && <ContactPickModal onClose={() => setModal('')} onPick={c => { setModal(''); put({ contact_id: c.id, contact_name: c.fn || c.org }, 'Kept with it') }} />}
      {modal === 'delete' && <Confirm title={`Delete ${r.title}?`} message={fromApp(r) ? <>It goes with its history. It was found in {SOURCE_NAME[r.source]}; the next scan will not add it again. To keep it out of the way instead, <b>archive</b> it under Edit.</> : <>It goes with its history. To keep it out of the way instead, <b>archive</b> it under Edit.</>} confirmLabel="Delete"
        onCancel={() => setModal('')} onConfirm={async () => { setModal(''); try { await api.del(`${V1}/renewals/${r.id}`); toast('Deleted'); announceChange(); navigate('/all') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}

