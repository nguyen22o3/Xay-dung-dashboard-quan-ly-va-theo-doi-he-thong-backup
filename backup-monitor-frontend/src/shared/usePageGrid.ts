import { useCallback, useEffect, useState } from 'react'
import type { Layout } from 'react-grid-layout'
import { readPageGridLayouts, type GridPage } from './pageGrid'

// Reset to the requested original defaults once. Subsequent edits still persist;
// the previous storage entries are retained rather than deleting browser data.
const storageKeys = { server: 'serverGridV20', drive: 'driveGridV5' }

export function usePageGrid(page: GridPage) {
  const key = storageKeys[page]
  const [layouts, setLayouts] = useState(() => {
    try { return readPageGridLayouts(page, localStorage.getItem(key)) }
    catch { return readPageGridLayouts(page, null) }
  })
  useEffect(() => {
    try { localStorage.setItem(key, JSON.stringify(layouts)) }
    catch { /* Layout editing also works when browser storage is blocked. */ }
  }, [key, layouts])
  const onLayoutChange = useCallback((_current: Layout, incoming: Partial<Record<string, Layout>>) => {
    const next = readPageGridLayouts(page, JSON.stringify({ ...layouts, ...incoming }))
    if (JSON.stringify(next) !== JSON.stringify(layouts)) setLayouts(next)
  }, [layouts, page])
  return { layouts, onLayoutChange }
}
