// Overview: what has run out, what is coming up, what was found and needs a look, trips a passport does not cover, what was renewed lately.
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useOnChange } from '../lib/data'
import { fmtDate } from '../lib/format'
import { money } from '../lib/renewals'
import type { Overview as OverviewT, TravelWarning } from '../lib/types'
import { Card, Loading, errMsg, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { HistoryRow, RenewalRow } from '../components/Rows'
import { openCreate } from '../components/Layout'
import { announceChange } from '../lib/data'

export default function Overview() {
  const { user } = useApp()
  const { toast } = useToast()
  const { data: o, reload, error } = useAsync(() => api.get<OverviewT>(`${V1}/overview`), [])
  useOnChange(reload)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!o) return <Loading />
  const first = (user?.name || '').split(' ')[0]
  if (o.counts.total === 0) return (
    <Card className="welcome">
      <div className="welcome-body">
        <img src="/favicon.svg" alt="" width={56} height={56} />
        <div className="grow">
          <h2>Welcome to Pocket Renewals</h2>
          <p className="muted">One list of everything that runs out: passports and ID cards, insurance, the car's registration, warranties, domains, subscriptions and contracts. You hear about each one well before its date, and renewing it moves the date on.</p>
          <p className="muted">Start by letting it look through your other Pocket apps (your documents, your appliances' warranties, the car's reminders), or add the first one yourself.</p>
          <div className="row wrap mt">
            <Link to="/find" className="btn primary"><Icon name="scan" size={15} />Find them in my apps</Link>
            <button type="button" className="btn" onClick={() => openCreate('renewal')}><Icon name="plus" size={15} />Add one</button>
          </div>
        </div>
      </div>
    </Card>
  )
  const cur = Object.keys(o.year_cost)[0] || 'EUR'
  const yearCost = o.year_cost[cur] || 0
  const checkAll = async () => { try { await api.post(`${V1}/renewals/checked`, { all: true }); toast('All marked as checked'); announceChange() } catch (e) { toast(errMsg(e), 'err') } }
  const lead = o.counts.expired > 0 ? `${o.counts.expired} ${o.counts.expired === 1 ? 'thing has' : 'things have'} run out${o.counts.soon ? `, ${o.counts.soon} more coming up` : ''}.`
    : o.counts.soon > 0 ? `${o.counts.soon} coming up in the next ${o.window} days; nothing has run out.` : 'Nothing runs out soon.'
  return (
    <div className="stack loose">
      <div className="page-head">
        <div className="grow">
          <h1 className="page-title">{first && first !== 'Admin' ? `Hello, ${first}` : 'What runs out'}</h1>
          <p className="muted">{lead}</p>
        </div>
      </div>

      <div className="tiles">
        <Link to="/all?status=expired" className={`tile ${o.counts.expired ? 'tile-bad' : ''}`}>
          <div className="tile-head"><span className="tile-ico"><Icon name="alert" size={14} /></span>Expired</div>
          <div className="tile-val">{o.counts.expired}</div>
        </Link>
        <Link to="/all?status=soon" className="tile">
          <div className="tile-head"><span className="tile-ico"><Icon name="clock" size={14} /></span>Coming up</div>
          <div className="tile-val">{o.counts.soon}</div>
          <div className="muted small">next {o.window} days or reminding</div>
        </Link>
        <Link to="/all?status=check" className="tile">
          <div className="tile-head"><span className="tile-ico"><Icon name="sparkles" size={14} /></span>Found, to check</div>
          <div className="tile-val">{o.counts.check}</div>
        </Link>
        <Link to="/all" className="tile">
          <div className="tile-head"><span className="tile-ico"><Icon name="wallet" size={14} /></span>A year of renewals</div>
          <div className="tile-val">{money(yearCost, cur, true)}</div>
          <div className="muted small">{o.counts.total} tracked{o.counts.auto ? `, ${o.counts.auto} renew by themselves` : ''}</div>
        </Link>
      </div>

      {o.travel.length > 0 && <Card title="Before you travel" sub="Trips in Pocket Travel that a passport or ID does not cover well" flush>
        <div className="list">{o.travel.map(w => <TravelRow key={`${w.trip_id}-${w.renewal_id}`} w={w} />)}</div>
      </Card>}

      <div className="two-grid">
        <div className="stack">
          {o.expired.length > 0 && <Card title="Expired" flush>
            <div className="list">{o.expired.map(r => <RenewalRow key={r.id} r={r} />)}</div>
          </Card>}
          <Card title="Coming up" sub={`The next ${o.window} days, and what is already reminding you`} flush actions={<Link to="/all" className="btn ghost sm">Everything</Link>}>
            {o.soon.length === 0 ? <p className="muted pad">Nothing runs out in the next {o.window} days.</p>
              : <div className="list">{o.soon.map(r => <RenewalRow key={r.id} r={r} />)}</div>}
          </Card>
        </div>
        <div className="stack">
          {o.check.length > 0 && <Card title="Found in your apps" sub="Added by themselves: check the date and the details" flush actions={<button type="button" className="btn ghost sm" onClick={checkAll}><Icon name="check" size={14} />All fine</button>}>
            <div className="list">{o.check.map(r => <RenewalRow key={r.id} r={r} />)}</div>
          </Card>}
          {o.later.length > 0 && <Card title="Later this year" flush>
            <div className="list">{o.later.map(r => <RenewalRow key={r.id} r={r} />)}</div>
          </Card>}
          <Card title="Recently renewed" flush actions={<Link to="/history" className="btn ghost sm">History</Link>}>
            {o.recent.length === 0 ? <p className="muted pad">Renew something and it shows here with what it cost.</p>
              : <div className="list">{o.recent.map(h => <HistoryRow key={h.id} h={h} showTitle />)}</div>}
          </Card>
        </div>
      </div>
    </div>
  )
}

export function TravelRow({ w }: { w: TravelWarning }) {
  return (
    <div className="list-row travel-row">
      <span className={`list-ico ${w.level === 'before' ? 'st-expired' : 'st-soon'}`}><Icon name="plane" size={16} /></span>
      <span className="grow row-main">
        <span className="row-title">{w.trip}{w.destination && w.destination !== w.trip ? <span className="muted"> · {w.destination}</span> : null}</span>
        <span className="muted small">{fmtDate(w.start)}{w.end !== w.start ? ` – ${fmtDate(w.end)}` : ''} · {w.message}</span>
      </span>
      <Link to={`/r/${w.renewal_id}`} className="btn sm">{w.holder || w.title}</Link>
      {w.trip_url && <a href={w.trip_url} target="_blank" rel="noreferrer" className="btn ghost sm icon-only hide-xs" title="Open the trip in Pocket Travel" aria-label="Open the trip in Pocket Travel"><Icon name="external" size={14} /></a>}
    </div>
  )
}
