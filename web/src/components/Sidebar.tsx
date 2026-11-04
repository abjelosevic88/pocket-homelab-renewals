// Left column: the add button, sections, the groups with how many each holds — the Pocket Calendar sidebar.
import { useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { GROUPS } from '../lib/renewals'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onCreate, onNavigate }: { onCreate: (k: CreateKind) => void; onNavigate: () => void }) {
  const { status, settings } = useApp()
  const { overview } = useData()
  const loc = useLocation()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Renewals'
  const c = overview?.counts
  const group = loc.pathname === '/all' ? new URLSearchParams(loc.search).get('group') : null
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: string; warn?: boolean }[] = [
    { to: '/', label: 'Overview', icon: 'hourglass', end: true, count: c?.expired ? String(c.expired) : c?.soon ? String(c.soon) : undefined, warn: !!c?.expired },
    { to: '/all', label: 'Everything', icon: 'list', end: true, count: c?.total ? String(c.total) : undefined },
    { to: '/find', label: 'Find in my apps', icon: 'scan', count: c?.check ? String(c.check) : undefined },
    { to: '/history', label: 'History', icon: 'note' },
  ]
  const groups = GROUPS.filter(g => (overview?.groups[g.value] || 0) > 0)
  return (
    <aside className="sidebar" aria-label="Pocket Renewals">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Renewals' ? title : <>Pocket <b>Renewals</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={15} strokeWidth={2.2} />Add<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate)} onClose={() => setMenu(null)} width={248} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive && !(n.to === '/all' && group) ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className={`sb-count ${n.warn ? 'warn' : ''}`} title={n.warn ? 'Expired' : undefined}>{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      {groups.length > 0 && <section className="sb-sec">
        <h4 className="sb-h">Kinds</h4>
        {groups.map(g => (
          <Link key={g.value} to={`/all?group=${g.value}`} onClick={onNavigate} className={`sb-group ${group === g.value ? 'active' : ''}`}>
            <Icon name={g.icon} size={15} className="sb-group-ico" />
            <span className="grow ellipsis">{g.label}</span>
            <span className="muted small">{overview?.groups[g.value]}</span>
          </Link>
        ))}
      </section>}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('prn:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
