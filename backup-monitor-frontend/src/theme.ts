export interface Theme {
  readonly isDark: boolean
  readonly bg: string
  readonly cardBg: string
  readonly cardBorder: string
  readonly textPrimary: string
  readonly textSecondary: string
  readonly gridLine: string
  readonly successText: string
  readonly errorText: string
  readonly warningText: string
  readonly titleColor: string
  readonly headerBg: string
  readonly sidebarBg: string
  readonly sidebarItem: string
}

export function makeTheme(isDark: boolean): Theme {
  return {
    isDark,
    bg: isDark ? '#090b0b' : '#f4f7f6',
    cardBg: isDark ? '#151818' : '#ffffff',
    cardBorder: isDark ? '#292e2d' : '#dde6e2',
    textPrimary: isDark ? '#f1f5f3' : '#17211d',
    textSecondary: isDark ? '#9aa6a1' : '#64746c',
    gridLine: isDark ? '#262c29' : '#e8efeb',
    successText: isDark ? '#08c889' : '#008d62',
    errorText: isDark ? '#fb7185' : '#d5344e',
    warningText: isDark ? '#fbbf24' : '#a56800',
    titleColor: isDark ? '#f7faf8' : '#15251d',
    headerBg: isDark ? '#0d100f' : '#ffffff',
    sidebarBg: isDark ? '#0b0d0c' : '#ffffff',
    sidebarItem: isDark ? '#aeb9b3' : '#51645a',
  }
}
