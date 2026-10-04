import type { Layout, LayoutItem } from 'react-grid-layout'

export const dashboardGridStorageKey = 'overviewGridV2'
export const dashboardBreakpoints = { lg: 1250, md: 1020, sm: 760, xs: 480, xxs: 0 }
export const dashboardColumns = { lg: 24, md: 12, sm: 8, xs: 4, xxs: 2 }
export type DashboardBreakpoint = keyof typeof dashboardColumns
export type DashboardLayouts = Record<DashboardBreakpoint, Layout>

const kpis = ['drive-files', 'drive-size', 'server-files', 'scheduled-jobs']
const panels = ['history', 'distribution', 'activity', 'health']

// Match the original overview's viewport-based responsive arrangement.
export function dashboardBreakpointForViewport(width: number): DashboardBreakpoint {
  return width > 1250 ? 'lg' : width > 1020 ? 'md' : width > 760 ? 'sm' : width > 480 ? 'xs' : 'xxs'
}

export function defaultDashboardLayouts(): DashboardLayouts {
  const layoutFor = (breakpoint: DashboardBreakpoint): Layout => {
    const cols = dashboardColumns[breakpoint]
    const perRow = breakpoint === 'lg' ? 4 : 2
    const kpiWidth = cols / perRow
    const layout: LayoutItem[] = kpis.map((i, index) => ({
      i, x: (index % perRow) * kpiWidth, y: Math.floor(index / perRow) * 4,
      w: kpiWidth, h: 4, minW: Math.min(3, kpiWidth), minH: 4,
    }))
    const start = (4 / perRow) * 4
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
    for (const breakpoint of Object.keys(dashboardColumns) as DashboardBreakpoint[]) {
      const items: unknown = saved?.[breakpoint]
      if (!Array.isArray(items) || items.length !== 8) continue
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
      if (normalized.length === 8) defaults[breakpoint] = normalized
    }
  } catch { /* Invalid or unavailable saved layout: retain responsive defaults. */ }
  return defaults
}
