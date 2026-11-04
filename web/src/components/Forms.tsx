// Every add/edit dialog: a renewal, "Renewed", and picking its paper from
// Pocket Documents or who to call from Pocket Contacts.
import React, { useEffect, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange, useData } from '../lib/data'
import { todayISO } from '../lib/format'
import { KINDS, UNITS, addInterval, kindOf, money, moneyInput, parseMoney, termText } from '../lib/renewals'
import type { ContactItem, DocumentItem, History, MoneyResult, Picked, Renewal, Unit } from '../lib/types'
import { CoreHint, Field, Loading, Modal, errMsg, useDebounce, useToast } from './ui'
import { Select } from './Select'
import { Icon } from './icons'

function Actions({ onClose, busy, label, danger }: { onClose: () => void; busy: boolean; label: string; danger?: React.ReactNode }) {
  return <div className="form-actions">{danger}<span className="grow" /><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{busy ? 'Saving…' : label}</button></div>
}

/** "Create a transaction in Pocket Money": greyed out with why when Money is not reachable. */
export function MoneyTick({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  const { settings } = useApp()
  if (!settings) return null
  const coreOn = settings.core_configured && settings.core_registered !== false
  if (settings.money_mode === 'off') return null
  const ready = settings.money_via !== ''
  return (
    <div className="stack tight">
      <label className={`check-row ${ready ? '' : 'disabled'}`} title={ready ? '' : settings.money_detail}>
        <input type="checkbox" checked={ready && checked} disabled={!ready} onChange={e => onChange(e.target.checked)} />
        <span><Icon name="wallet" size={14} /> Book what it cost in Pocket Money</span>
      </label>
      {!ready && <p className="muted small">{settings.money_detail ? `Pocket Money is not connected: ${settings.money_detail}.` : 'Connect Pocket Money in Settings → Connected apps.'}</p>}
      {!ready && !coreOn && settings.money_mode === 'core' && <CoreHint />}
    </div>
  )
}

function moneyToast(toast: ReturnType<typeof useToast>['toast'], m?: MoneyResult) {
  if (!m || m.skipped) return
  if (m.ok) toast(`Booked in Pocket Money${m.account ? ` on ${m.account}` : ''}`)
  else toast(`Saved, but Pocket Money said: ${m.error}`, 'err')
}

// ---------- a renewal ----------

export function RenewalModal({ renewal, preset, onClose, onSaved }: { renewal?: Renewal; preset?: Partial<Renewal>; onClose: () => void; onSaved?: (r: Renewal) => void }) {
  const { settings } = useApp()
  const { holders } = useData()
  const { toast } = useToast()
  const start = renewal || preset || {}
  const k0 = kindOf(start.kind || 'passport')
  const [f, setF] = useState({
    title: start.title || '', kind: start.kind || '', holder: start.holder || '', provider: start.provider || '', number: start.number || '',
    issued_on: start.issued_on || '', expires_on: start.expires_on || '', renew_n: String(renewal ? start.renew_n ?? 0 : k0.term_n), renew_unit: (start.renew_unit || k0.term_u) as Unit,
    auto_renews: !!start.auto_renews, remind_days: start.remind_days || '', lead_days: start.lead_days ? String(start.lead_days) : '',
    cost: moneyInput(start.cost), currency: start.currency || settings?.currency || 'EUR', renew_url: start.renew_url || '', notes: start.notes || '', archived: !!start.archived,
  })
  const [more, setMore] = useState(!!renewal)
  const [busy, setBusy] = useState(false)
  const kind = kindOf(f.kind || 'other')
  const pickKind = (v: string) => {
    const k = kindOf(v)
    setF(x => ({ ...x, kind: v, title: x.title || (v === 'other' ? '' : k.label.replace(/ \(.*\)$/, '').replace(/ or .*$/, '')), renew_n: renewal ? x.renew_n : String(k.term_n), renew_unit: renewal ? x.renew_unit : k.term_u,
      auto_renews: renewal ? x.auto_renews : v === 'subscription' || v === 'domain' }))
  }
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const cost = parseMoney(f.cost)
    if (f.cost.trim() && cost == null) { toast('The cost is a number like 55 or 41.20', 'err'); return }
    setBusy(true)
    const body = { ...f, kind: f.kind || 'other', renew_n: Number(f.renew_n) || 0, lead_days: Number(f.lead_days) || 0, cost: f.cost.trim() ? cost : null }
    try {
      const r = renewal ? await api.put<Renewal>(`${V1}/renewals/${renewal.id}`, body) : await api.post<Renewal>(`${V1}/renewals`, body)
      toast(renewal ? 'Saved' : `${r.title} added`); announceChange(); onSaved?.(r); onClose()
    } catch (err) { toast(errMsg(err), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={renewal ? `Edit ${renewal.title}` : 'Add something that runs out'} onClose={onClose}>
      <form onSubmit={save} className="grid form-grid">
        <Field group label="What is it"><Select ariaLabel="Kind" value={f.kind} onChange={pickKind} placeholder="Choose…" options={KINDS.map(k => ({ value: k.value, label: k.label }))} /></Field>
        <Field label="Name"><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} required maxLength={200} autoFocus={!!renewal} placeholder="Passport, Car insurance, Domain example.com" /></Field>
        <div className="two-col tight">
          <Field label="Runs out on" help={f.expires_on ? '' : 'Leave it empty when you do not know yet.'}><input type="date" value={f.expires_on} onChange={e => setF({ ...f, expires_on: e.target.value })} /></Field>
          <Field label="Whose (optional)"><input value={f.holder} onChange={e => setF({ ...f, holder: e.target.value })} list="prn-holders" placeholder="Me, Ana, the car" maxLength={120} />
            <datalist id="prn-holders">{holders.map(h => <option key={h} value={h} />)}</datalist></Field>
        </div>
        <div className="two-col tight">
          <Field label="Issued by or bought from"><input value={f.provider} onChange={e => setF({ ...f, provider: e.target.value })} placeholder="Allianz, Namecheap, the municipality" maxLength={160} /></Field>
          <Field label="Number"><input value={f.number} onChange={e => setF({ ...f, number: e.target.value })} placeholder="Document or policy number" maxLength={120} /></Field>
        </div>
        <label className="check-row"><input type="checkbox" checked={f.auto_renews} onChange={e => setF({ ...f, auto_renews: e.target.checked })} />It renews by itself (a subscription, a domain on auto-renew)</label>
        {!more && <button type="button" className="conn-advanced" onClick={() => setMore(true)}><Icon name="chevron-down" size={13} /> Term, reminders, cost and notes</button>}
        {more && <>
          <div className="two-col tight">
            <Field group label="Usual term" help={termText(Number(f.renew_n) || 0, f.renew_unit)}>
              <div className="row every-row"><input className="num-sm" inputMode="numeric" value={f.renew_n} onChange={e => setF({ ...f, renew_n: e.target.value.replace(/\D/g, '') })} aria-label="Term" />
                <Select ariaLabel="Unit" value={f.renew_unit} onChange={v => setF({ ...f, renew_unit: v as Unit })} options={UNITS} /></div>
            </Field>
            <Field label="Issued on"><input type="date" value={f.issued_on} onChange={e => setF({ ...f, issued_on: e.target.value })} /></Field>
          </div>
          <div className="two-col tight">
            <Field label="Remind me (days before)" help={`Empty: ${kind.remind.join(', ')} days before, as usual for ${kind.label.toLowerCase().replace(/ \(.*\)$/, '')}.`}><input value={f.remind_days} onChange={e => setF({ ...f, remind_days: e.target.value })} placeholder={kind.remind.join(', ')} /></Field>
            <Field label="Takes this long to renew (days)" help="A notice period, or how long a new one takes. You are told two weeks before the last day to act."><input inputMode="numeric" value={f.lead_days} onChange={e => setF({ ...f, lead_days: e.target.value.replace(/\D/g, '') })} placeholder={kind.lead ? String(kind.lead) : '0'} /></Field>
          </div>
          <div className="two-col tight">
            <Field label={`What a renewal costs (${f.currency})`}><input inputMode="decimal" value={f.cost} onChange={e => setF({ ...f, cost: e.target.value })} placeholder="0.00" /></Field>
            <Field label="Currency"><input value={f.currency} onChange={e => setF({ ...f, currency: e.target.value.toUpperCase() })} maxLength={3} /></Field>
          </div>
          <Field label="Where to renew it"><input value={f.renew_url} onChange={e => setF({ ...f, renew_url: e.target.value })} placeholder="https://…" /></Field>
          <Field label="Notes"><textarea value={f.notes} onChange={e => setF({ ...f, notes: e.target.value })} rows={3} placeholder="What to bring, who to call" /></Field>
          {renewal && <label className="check-row"><input type="checkbox" checked={f.archived} onChange={e => setF({ ...f, archived: e.target.checked })} />Archived (I do not have it any more)</label>}
        </>}
        <Actions onClose={onClose} busy={busy} label={renewal ? 'Save' : 'Add'} />
      </form>
    </Modal>
  )
}

// ---------- renewed ----------

export function RenewModal({ renewal, next, onClose, onDone }: { renewal: Renewal; next?: string; onClose: () => void; onDone?: () => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const guess = next || (renewal.renew_n ? addInterval(renewal.expires_on && renewal.days_left > -30 && renewal.group !== 'identity' ? renewal.expires_on : todayISO(), renewal.renew_n, renewal.renew_unit) : '')
  const [f, setF] = useState({ renewed_on: todayISO(), expires_on: guess, cost: moneyInput(renewal.cost), note: '', number: '' })
  const [book, setBook] = useState(settings?.money_book ?? false)
  const [busy, setBusy] = useState(false)
  const identity = renewal.group === 'identity'
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const cost = parseMoney(f.cost)
    if (f.cost.trim() && cost == null) { toast('The cost is a number like 55 or 41.20', 'err'); return }
    setBusy(true)
    try {
      const r = await api.post<{ renewal: Renewal; history: History; money?: MoneyResult }>(`${V1}/renewals/${renewal.id}/renew`, { ...f, cost: cost || 0, money: book, issued_on: identity ? f.renewed_on : '' })
      toast(`Renewed — now until ${r.renewal.expires_on}`); moneyToast(toast, r.money); announceChange(); onDone?.(); onClose()
    } catch (err) { toast(errMsg(err), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Renewed: ${renewal.title}`} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid form-grid">
        <div className="two-col tight">
          <Field label="Renewed on"><input type="date" value={f.renewed_on} onChange={e => setF({ ...f, renewed_on: e.target.value })} required /></Field>
          <Field label="Now runs out on"><input type="date" value={f.expires_on} onChange={e => setF({ ...f, expires_on: e.target.value })} required autoFocus /></Field>
        </div>
        {identity && <Field label="New number (optional)"><input value={f.number} onChange={e => setF({ ...f, number: e.target.value })} placeholder={renewal.number || 'The new document number'} /></Field>}
        <Field label={`It cost (${renewal.currency})`}><input inputMode="decimal" value={f.cost} onChange={e => setF({ ...f, cost: e.target.value })} placeholder="0.00" /></Field>
        <Field label="Note (optional)"><input value={f.note} onChange={e => setF({ ...f, note: e.target.value })} placeholder="Switched to the yearly plan" /></Field>
        {parseMoney(f.cost) ? <MoneyTick checked={book} onChange={setBook} /> : null}
        <Actions onClose={onClose} busy={busy} label="Save" />
      </form>
    </Modal>
  )
}

// ---------- papers and people from the other apps ----------

export function DocumentPickModal({ onClose, onPick }: { onClose: () => void; onPick: (d: DocumentItem) => void }) {
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 300)
  const [res, setRes] = useState<Picked<DocumentItem> | null>(null)
  useEffect(() => {
    let live = true
    api.get<Picked<DocumentItem>>(`${V1}/links/documents?q=${encodeURIComponent(dq)}`).then(r => { if (live) setRes(r) }).catch(e => live && setRes({ connected: false, detail: errMsg(e), items: [] }))
    return () => { live = false }
  }, [dq])
  return (
    <Modal title="Keep the paper from Pocket Documents" onClose={onClose} size="sm">
      <div className="grid" style={{ gap: 10 }}>
        <input value={q} onChange={e => setQ(e.target.value)} placeholder="Search your documents (passport, policy, contract…)" autoFocus />
        <div className="pick-list">
          {!res ? <Loading /> : res.detail ? <p className="bad small">{res.detail}</p> : res.items.length === 0 ? <p className="muted small">Nothing found.</p>
            : res.items.map(d => <button key={d.id} type="button" className="pick-row" onClick={() => onPick(d)}><Icon name="file" size={15} /><span className="grow"><b>{d.title || d.original_name}</b><span className="muted small block-line">{[d.document_type_name, d.correspondent_name, (d.created || d.added_at || '').slice(0, 10)].filter(Boolean).join(' · ')}</span></span></button>)}
        </div>
      </div>
    </Modal>
  )
}

export function ContactPickModal({ onClose, onPick }: { onClose: () => void; onPick: (c: ContactItem) => void }) {
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 300)
  const [res, setRes] = useState<Picked<ContactItem> | null>(null)
  useEffect(() => {
    let live = true
    api.get<Picked<ContactItem>>(`${V1}/links/contacts?q=${encodeURIComponent(dq)}`).then(r => { if (live) setRes(r) }).catch(e => live && setRes({ connected: false, detail: errMsg(e), items: [] }))
    return () => { live = false }
  }, [dq])
  return (
    <Modal title="Who to call, from Pocket Contacts" onClose={onClose} size="sm">
      <div className="grid" style={{ gap: 10 }}>
        <input value={q} onChange={e => setQ(e.target.value)} placeholder="Search your contacts (the agent, the registrar…)" autoFocus />
        <div className="pick-list">
          {!res ? <Loading /> : res.detail ? <p className="bad small">{res.detail}</p> : !dq ? <p className="muted small">Type a name.</p> : res.items.length === 0 ? <p className="muted small">Nobody found.</p>
            : res.items.map(c => <button key={c.id} type="button" className="pick-row" onClick={() => onPick(c)}><Icon name="user" size={15} /><span className="grow"><b>{c.fn || c.org}</b><span className="muted small block-line">{[c.org !== c.fn && c.org, c.phones?.[0], c.emails?.[0]].filter(Boolean).join(' · ')}</span></span></button>)}
        </div>
      </div>
    </Modal>
  )
}

export { money }
