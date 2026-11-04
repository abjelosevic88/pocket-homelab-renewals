// Every renewal done: when, the new date, what it cost.
import { api, V1 } from '../lib/api'
import { useOnChange } from '../lib/data'
import { money } from '../lib/renewals'
import type { History as HistoryT } from '../lib/types'
import { Card, Empty, Loading, useAsync } from '../components/ui'
import { Icon } from '../components/icons'
import { HistoryRow } from '../components/Rows'

export default function History() {
  const { data, reload, error } = useAsync(() => api.get<HistoryT[]>(`${V1}/history?limit=500`), [])
  useOnChange(reload)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const byYear = new Map<string, HistoryT[]>()
  for (const h of data) byYear.set(h.renewed_on.slice(0, 4), [...(byYear.get(h.renewed_on.slice(0, 4)) || []), h])
  return (
    <div className="stack loose">
      <div className="page-head"><div className="grow"><h1 className="page-title">History</h1><p className="muted">Every renewal, newest first, with what it cost.</p></div></div>
      {data.length === 0 ? <Empty title="Nothing renewed yet" icon="note">Press “Renewed” on something and it shows here.</Empty>
        : [...byYear.entries()].map(([y, list]) => {
          const cur = list[0].currency
          const total = list.filter(h => h.currency === cur).reduce((s, h) => s + h.cost, 0)
          return (
            <Card key={y} title={<>{y} <span className="muted count">{list.length}</span></>} actions={total ? <span className="muted small tnum">{money(total, cur, true)}</span> : undefined} flush>
              <div className="list">{list.map(h => <HistoryRow key={h.id} h={h} showTitle />)}</div>
            </Card>
          )
        })}
    </div>
  )
}
