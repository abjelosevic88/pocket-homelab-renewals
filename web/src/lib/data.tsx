// The person's counts per group and status and the holders, loaded once for
// the sidebar and pickers; any change in the app ("prn:changed") reloads them.
import React, { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { api, V1 } from './api'
import type { Overview } from './types'

interface Data { overview: Overview | null; holders: string[]; reload: () => void }
const Ctx = createContext<Data>({ overview: null, holders: [], reload: () => {} })
export const useData = () => useContext(Ctx)

/** Something was added or changed: lists and pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('prn:changed'))
export function useOnChange(cb: () => void) {
  const ref = React.useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('prn:changed', h)
    return () => window.removeEventListener('prn:changed', h)
  }, [])
}

export function DataProvider({ children }: { children: React.ReactNode }) {
  const [overview, setOverview] = useState<Overview | null>(null)
  const [holders, setHolders] = useState<string[]>([])
  const reload = useCallback(() => {
    api.get<Overview>(`${V1}/overview`).then(setOverview).catch(() => {})
    api.get<string[]>(`${V1}/holders`).then(setHolders).catch(() => {})
  }, [])
  useEffect(() => { reload() }, [reload])
  useOnChange(reload)
  return <Ctx.Provider value={{ overview, holders, reload }}>{children}</Ctx.Provider>
}
