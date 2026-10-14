import type { Layout, LayoutItem } from 'react-grid-layout'

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
