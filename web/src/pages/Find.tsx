// Find in my apps: the Pocket apps renewals are found in, each with its connection, a switch and "Scan now";
// new and changed items read as they are saved; the nightly scan; what was looked at lately.
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange } from '../lib/data'
import { ago, fmtDateTime } from '../lib/format'
import { SOURCE_ICON, SOURCE_NAME } from '../lib/renewals'
import type { Scan, Settings, Source, Sources } from '../lib/types'
import { Card, CoreHint, Loading, SettingRow, Spinner, Switch, errMsg, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'

function scanText(s: Scan): string {
  if (s.error) return `Did not work: ${s.error}`
  const bits = [`looked at ${s.looked}`, `${s.found} with a date`]
  if (s.added) bits.push(`${s.added} added`)
  if (s.updated) bits.push(`${s.updated} moved on`)
  return bits.join(', ')
}

export default function Find() {
  const { settings, setSettings } = useApp()
  const { toast } = useToast()
  const { data, reload, error } = useAsync(() => api.get<Sources>(`${V1}/sources`), [])
  const { data: scans, reload: reloadScans } = useAsync(() => api.get<Scan[]>(`${V1}/scans`), [])
  const { data: dismissed, reload: reloadDismissed } = useAsync(() => api.get<{ source_ref: string; title: string; created_at: string }[]>(`${V1}/dismissed`), [])
  const running = !!data?.sources.some(s => s.running)
  const was = useRef(false)
  useEffect(() => {
    if (!running) {
      if (was.current) { was.current = false; announceChange(); reloadScans() }
      return
    }
    was.current = true
    const t = setInterval(reload, 2000)
    return () => clearInterval(t)
  }, [running, reload, reloadScans])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data || !settings) return <Loading />
  const coreOn = settings.core_configured && settings.core_registered !== false
  const save = async (patch: Partial<Settings>) => {
    try { const s = await api.put<Settings>(`${V1}/settings`, patch); setSettings(s); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const scan = async (source: string) => {
    try { const r = await api.post<{ started: string[] }>(`${V1}/scan`, { source }); toast(source === 'all' ? `Looking through ${r.started.length} ${r.started.length === 1 ? 'app' : 'apps'}…` : 'Looking…'); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const restore = async (ref: string) => { try { await api.post(`${V1}/dismissed/restore`, ref ? { source_ref: ref } : { all: true }); reloadDismissed(); toast('The next scan may add it again') } catch (e) { toast(errMsg(e), 'err') } }
  const ready = data.sources.filter(s => s.connected && s.included)
  const docs = data.sources.find(s => s.name === 'documents')
  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow"><h1 className="page-title">Find in my apps</h1><p className="muted">Renewals look through your other Pocket apps for dates that run out and add each one, marked “Check”. Nothing is added twice, and what you delete stays deleted.</p></div>
        <button type="button" className="btn primary" disabled={running || ready.length === 0} onClick={() => scan('all')} title={ready.length === 0 ? 'Connect at least one app below' : ''}>
          {running ? <Spinner /> : <Icon name="scan" size={15} />}{running ? 'Looking…' : 'Scan everything'}</button>
      </div>
      {!coreOn && <div className="callout info"><Icon name="info" size={15} /><div>Without Pocket Core each app is connected with a URL and a token under Settings → Connected apps.<CoreHint /></div></div>}

      <Card title="As it happens" sub="No need to scan for these">
        <SettingRow title="As soon as you save something" help={<>A document filed in Pocket Documents is read within seconds; an appliance's warranty in Pocket Home, a reminder or paper in Pocket Garage, an item in Pocket Inventory, a subscription in Pocket Money or a renewed certificate in Pocket Monitoring shows up a few seconds after you save it. Each app needs its “events:read” link on in Pocket Core → Links.{' '}
          {!coreOn ? 'Needs Pocket Core.' : data.stream.connected ? (data.stream.last_event_at ? `Last change ${ago(data.stream.last_event_at)}.` : 'Listening.') : `Not listening yet${data.stream.error ? `: ${data.stream.error}` : ''}.`}</>}>
          <Switch checked={data.live && coreOn} disabled={!coreOn} onChange={v => save({ live: v })} label="As it happens" />
        </SettingRow>
        <SettingRow title="Read the text with AI" help={docs?.ai_ready ? 'When a document has no “Valid until” or other date field, AI reads its text for an end date (passports, policies, contracts only). What it finds is always marked “Check”.' : <>No AI is set up: only date fields are read. <Link to="/settings/ai">Settings → AI</Link></>}>
          <Switch checked={data.documents_ai && !!docs?.ai_ready} disabled={!docs?.ai_ready} onChange={v => save({ documents_ai: v })} label="AI for documents" />
        </SettingRow>
        <SettingRow title="Every night" help="Looks through every app switched on below at 4 in the morning, so anything the live updates missed is here by the time you wake up.">
          <Switch checked={data.scan_nightly} onChange={v => save({ scan_nightly: v })} label="Nightly scan" />
        </SettingRow>
      </Card>

      <Card title="Apps to look in" flush>
        <div className="list">{data.sources.map(s => <SourceRow key={s.name} s={s} coreOn={coreOn} onScan={() => scan(s.name)} onInclude={v => save({ scan_sources: { [s.name]: v } } as Partial<Settings>)} />)}</div>
      </Card>

      {scans && scans.length > 0 && <Card title="Lately" flush>
        <div className="list">{scans.slice(0, 12).map(sc => (
          <div key={sc.id} className="list-row">
            <span className={`list-ico ${sc.error ? 'st-soon' : sc.added ? 'st-ok' : ''}`}><Icon name={SOURCE_ICON[sc.source] || 'layers'} size={16} /></span>
            <span className="grow row-main"><span className="row-title">{SOURCE_NAME[sc.source] || sc.source}<span className="muted small"> · {sc.trigger === 'event' ? 'as it happened' : sc.trigger === 'nightly' ? 'at night' : sc.trigger === 'weekly' ? 'weekly' : 'by hand'}</span></span>
              <span className={`small ${sc.error ? 'warn-text' : 'muted'}`}>{scanText(sc)}</span></span>
            <span className="muted small hide-xs">{fmtDateTime(sc.started_at)}</span>
          </div>))}</div>
      </Card>}

      {dismissed && dismissed.length > 0 && <Card title="Deleted, not added again" sub="Found once and deleted by you" flush actions={<button type="button" className="btn ghost sm" onClick={() => restore('')}>Allow all again</button>}>
        <div className="list">{dismissed.map(d => (
          <div key={d.source_ref} className="list-row">
            <span className="list-ico"><Icon name="eye-off" size={16} /></span>
            <span className="grow row-main"><span className="row-title">{d.title || d.source_ref}</span><span className="muted small">{SOURCE_NAME[d.source_ref.split(':')[0]] || ''} · deleted {fmtDateTime(d.created_at)}</span></span>
            <button type="button" className="btn ghost sm" onClick={() => restore(d.source_ref)}>Allow again</button>
          </div>))}</div>
      </Card>}
    </div>
  )
}

function SourceRow({ s, coreOn, onScan, onInclude }: { s: Source; coreOn: boolean; onScan: () => void; onInclude: (v: boolean) => void }) {
  const needsAI = s.ai === 'required' && !s.ai_ready
  const usable = s.connected && !needsAI
  return (
    <div className="list-row source-row">
      <span className={`list-ico ${s.connected ? 'st-ok' : ''}`}><Icon name={SOURCE_ICON[s.name] || 'layers'} size={16} /></span>
      <span className="grow row-main">
        <span className="row-title">{s.label}{s.count > 0 && <Link to={`/all?source=${s.name}`} className="muted small" style={{ fontWeight: 500 }}>{s.count} here</Link>}</span>
        <span className="muted small">{s.what}</span>
        <span className="source-meta">
          {s.connected ? <span className="ok">Connected{s.mode === 'direct' ? ' directly' : ' through Pocket Core'}</span> : <span className="bad">{s.detail ? `Not connected: ${s.detail}` : 'Not connected'}</span>}
          {needsAI && <span className="bad">Needs AI (Settings → AI)</span>}
          {s.last && <span title={fmtDateTime(s.last.started_at)}>Last look {ago(s.last.started_at)}: {scanText(s.last)}</span>}
          {s.open_url && <a href={s.open_url} target="_blank" rel="noreferrer">Open {s.label} <Icon name="external" size={11} /></a>}
        </span>
        {!s.connected && !coreOn && s.mode === 'core' && <CoreHint />}
      </span>
      <span className="source-actions">
        <Switch size="sm" checked={s.included} onChange={onInclude} label={`Include ${s.label}`} />
        <button type="button" className="btn sm" disabled={!usable || s.running} onClick={onScan} title={usable ? '' : needsAI ? 'Set up AI first' : s.detail}>
          {s.running ? <Spinner size={12} /> : <Icon name="scan" size={14} />}{s.running ? 'Looking…' : 'Scan now'}</button>
      </span>
    </div>
  )
}
