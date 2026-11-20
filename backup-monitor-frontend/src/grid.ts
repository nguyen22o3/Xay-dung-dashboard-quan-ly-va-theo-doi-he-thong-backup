import type { Layout, LayoutItem } from "react-grid-layout"
import { useCallback, useEffect, useState } from "react"

export const dashboardGridStorageKey = 'overviewGridV2'
export const dashboardBreakpoints = { lg: 1250, md: 1020, sm: 760, xs: 480, xxs: 0 }
export const dashboardColumns = { lg: 24, md: 12, sm: 8, xs: 4, xxs: 2 }
type DashboardBreakpoint = keyof typeof dashboardColumns
type DashboardLayouts = Record<DashboardBreakpoint, Layout>

const kpis = ['drive-files', 'drive-size', 'server-files', 'server-size', 'scheduled-jobs']
const panels = ['history', 'distribution', 'activity', 'health']

// Match the original overview's viewport-based responsive arrangement.
export function dashboardBreakpointForViewport(width: number): DashboardBreakpoint {
  return width > 1250 ? 'lg' : width > 1020 ? 'md' : width > 760 ? 'sm' : width > 480 ? 'xs' : 'xxs'
}

function defaultDashboardLayouts(kpiIds = kpis): DashboardLayouts {
  const layoutFor = (breakpoint: DashboardBreakpoint): Layout => {
    const cols = dashboardColumns[breakpoint]
    const perRow = breakpoint === 'lg' ? kpiIds.length : 2
    const layout: LayoutItem[] = kpiIds.map((i, index) => {
      const column = index % perRow
      const rowSize = Math.min(perRow, kpiIds.length - index + column)
      const width = Math.floor(cols / rowSize)
      const extra = cols % rowSize
      return {
        i, x: column * width + Math.min(column, extra), y: Math.floor(index / perRow) * 4,
        w: width + (column < extra ? 1 : 0), h: 4, minW: Math.min(3, Math.floor(cols / perRow)), minH: 4,
      }
    })
    const start = Math.ceil(kpiIds.length / perRow) * 4
    for (const [index, i] of panels.entries()) {
      const wide = breakpoint === 'lg' || breakpoint === 'md'
      const split = breakpoint === 'lg' ? (index < 2 ? 16 : 13) : 7
      layout.push({
        i, x: wide && index % 2 === 1 ? split : 0,
        y: start + (wide ? (index >= 2 ? 9 : 0) : index * 9),
        w: wide ? (index % 2 === 0 ? split : cols - split) : cols,
        h: index < 2 ? 9 : 7, minW: Math.min(cols, breakpoint === 'lg' ? 6 : 4), minH: index < 2 ? 7 : 6,
      })
    }
    return layout
  }
  return { lg: layoutFor('lg'), md: layoutFor('md'), sm: layoutFor('sm'), xs: layoutFor('xs'), xxs: layoutFor('xxs') }
}

// Persist only valid geometry, never arbitrary static/disabled flags from storage.
export function readDashboardLayouts(raw: string | null): DashboardLayouts {
  const defaults = defaultDashboardLayouts()
  if (!raw) return defaults
  try {
    const saved = JSON.parse(raw)
    const legacyDefaults = defaultDashboardLayouts(kpis.filter((id) => id !== 'server-size'))
    for (const breakpoint of Object.keys(dashboardColumns) as DashboardBreakpoint[]) {
      const stored: unknown = saved?.[breakpoint]
      if (!Array.isArray(stored)) continue
      const items = stored.filter(item => item?.i !== 'restore-check')
      const hasServerSize = items.some(item => item?.i === 'server-size')
      const legacyLayout = legacyDefaults[breakpoint]
      const expected = hasServerSize ? defaults[breakpoint] : legacyLayout
      if (items.length !== expected.length) continue
      const ids = new Set<string>()
      const normalized: LayoutItem[] = []
      for (const item of items) {
        const base = defaults[breakpoint].find((entry) => entry.i === item?.i)
        if (!base || ids.has(item.i)) break
        const { x, y, w, h } = item
        if (![x, y, w, h].every((value) => Number.isSafeInteger(value)) ||
          x < 0 || y < 0 || y > 10000 || w < (base.minW ?? 1) || h < (base.minH ?? 1) ||
          h > 100 || x + w > dashboardColumns[breakpoint]) break
        ids.add(item.i)
        normalized.push({ ...base, x, y, w, h })
      }
      if (normalized.length !== items.length) continue
      if (!hasServerSize) {
        // Upgrade the old default arrangement. Custom cards keep their positions.
        if (legacyLayout.every((base) => normalized.some((item) => item.i === base.i && item.x === base.x && item.y === base.y && item.w === base.w && item.h === base.h))) continue
        const files = normalized.find((item) => item.i === 'server-files')
        const size = defaults[breakpoint].find((item) => item.i === 'server-size')
        if (!files || !size) continue
        const cols = dashboardColumns[breakpoint]
        const fits = (x: number, y: number) => x + size.w <= cols && y <= 10000 && normalized.every((item) => (
          x + size.w <= item.x || item.x + item.w <= x || y + size.h <= item.y || item.y + item.h <= y
        ))
        const beside = files.x + files.w
        if (fits(beside, files.y)) {
          normalized.push({ ...size, x: beside, y: files.y })
        } else {
          const x = Math.min(files.x, cols - size.w)
          const freeRows = [0, ...normalized.map((item) => item.y + item.h)].filter((y) => fits(x, y)).sort((a, b) => a - b)
          const y = freeRows.find((row) => row >= files.y + files.h) ?? freeRows[0]
          if (y === undefined) continue
          normalized.push({ ...size, x, y })
        }
      }
      defaults[breakpoint] = normalized
    }
  } catch { /* Invalid or unavailable saved layout: retain responsive defaults. */ }
  return defaults
}

export const pageGridBreakpoints = { lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }
export const pageGridColumns = { lg: 24, md: 20, sm: 12, xs: 8, xxs: 4 }
type Breakpoint = keyof typeof pageGridColumns
export type PageGridLayouts = Record<Breakpoint, Layout>
export type GridPage = 'server' | 'drive'

// Reading order also applies when cards stack on smaller screens.
const serverIds = ['overview-status', 'cron-jobs', 'cpu', 'ram', 'disk', 'success-failure', 'backup-chart', 'growth-trend', 'duration-chart', 'local-snapshots', 'websites']
const driveIds = ['cloud', 'last-backup', 'daily', 'history', 'growth', 'success']

function defaultLayouts(page: GridPage): PageGridLayouts {
  const layoutFor = (breakpoint: Breakpoint): Layout => {
    const cols = pageGridColumns[breakpoint]
    const wide = breakpoint === 'lg' || breakpoint === 'md'
    const layout: LayoutItem[] = []
    let y = 0
    const row = (ids: string[], widths: number[], height: number) => {
      let x = 0
      ids.forEach((i, index) => {
        const w = widths[index]
        // Keep the original resize limits, independent of the default card size.
        // Mobile cards start full-width but can still be resized by the user.
        const minW = page === 'drive' ? 2 : i === 'overview-status' ? 6 : 3
        const minH = page === 'drive' ? 2 : i === 'overview-status' ? 3 : 4
        layout.push({ i, x, y, w, h: height, minW: Math.min(cols, minW), minH })
        x += w
      })
      y += height
    }
    if (!wide) {
      for (const i of page === 'server' ? serverIds : driveIds) {
        row([i], [cols], i === 'overview-status' ? 10 : ['cpu', 'ram', 'disk'].includes(i) ? 6 : 9)
      }
    } else if (page === 'server') {
      row(['overview-status'], [cols], 4)
      row(['cron-jobs', 'cpu', 'ram', 'disk'], [cols / 4, cols / 4, cols / 4, cols / 4], 4)
      row(['success-failure', 'backup-chart', 'growth-trend'], [cols / 4, cols / 4, cols / 2], 7)
      row(['duration-chart', 'local-snapshots', 'websites'], breakpoint === 'lg' ? [12, 7, 5] : [10, 6, 4], 6)
    } else {
      row(['cloud', 'last-backup', 'daily'], breakpoint === 'lg' ? [8, 8, 8] : [7, 6, 7], 4)
      row(['history', 'growth'], [cols / 2, cols / 2], 5)
      row(['success'], [cols], 5)
    }
    return layout
  }
  return { lg: layoutFor('lg'), md: layoutFor('md'), sm: layoutFor('sm'), xs: layoutFor('xs'), xxs: layoutFor('xxs') }
}

function overlaps(a: LayoutItem, b: LayoutItem): boolean {
  return a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y
}

// Keep valid user positions; repair missing/tiny widgets from older versions.
// Explicit defaults at every breakpoint prevent desktop layouts being squeezed on mobile.
export function readPageGridLayouts(page: GridPage, raw: string | null): PageGridLayouts {
  const defaults = defaultLayouts(page)
  if (!raw) return defaults
  try {
    const saved: unknown = JSON.parse(raw)
    if (!saved || typeof saved !== 'object') return defaults
    for (const breakpoint of Object.keys(pageGridColumns) as Breakpoint[]) {
      const items: unknown = (saved as Record<string, unknown>)[breakpoint]
      if (!Array.isArray(items)) continue
      const bases = defaults[breakpoint]
      const valid: LayoutItem[] = []
      for (const base of bases) {
        const matches = items.filter(item => item?.i === base.i)
        if (matches.length !== 1) continue
        const { x, y, w, h } = matches[0]
        if (![x, y, w, h].every(Number.isSafeInteger) || x < 0 || y < 0 ||
          w < (base.minW ?? 1) || h < (base.minH ?? 1) || x + w > pageGridColumns[breakpoint]) continue
        const item = { ...base, x, y, w, h }
        if (!valid.some(other => overlaps(item, other))) valid.push(item)
      }
      for (const base of bases) {
        if (valid.some(item => item.i === base.i)) continue
        const item = { ...base }
        if (valid.some(other => overlaps(item, other))) item.y = Math.max(0, ...valid.map(other => other.y + other.h))
        valid.push(item)
      }
      defaults[breakpoint] = bases.map(base => valid.find(item => item.i === base.i)!)
    }
  } catch { /* Corrupt storage must not prevent login or rendering. */ }
  return defaults
}

// Apply the screenshot-based defaults once. Subsequent edits still persist;
// the previous storage entries are retained rather than deleting browser data.
const storageKeys = { server: 'serverGridV21', drive: 'driveGridV6' }

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
