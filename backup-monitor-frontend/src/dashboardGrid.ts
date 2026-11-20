import type { Layout, LayoutItem } from 'react-grid-layout'

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
