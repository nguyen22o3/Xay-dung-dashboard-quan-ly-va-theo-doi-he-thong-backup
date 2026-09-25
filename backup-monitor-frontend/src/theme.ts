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
    bg: isDark ? '#1e1e1e' : '#f4f5f7',
    cardBg: isDark ? '#252526' : '#ffffff',
    cardBorder: isDark ? '#333333' : '#dcdcdc',
    textPrimary: isDark ? '#cccccc' : '#333333',
    textSecondary: isDark ? '#888888' : '#777777',
    gridLine: isDark ? '#333333' : '#eeeeee',
    successText: '#4caf50',
    errorText: '#d50000',
    warningText: isDark ? '#ffeb3b' : '#f9a825',
    titleColor: isDark ? '#999999' : '#1a4175',
    headerBg: '#1a4175',
    sidebarBg: '#4a4e52',
    sidebarItem: isDark ? '#dddddd' : '#dddddd',
  }
}