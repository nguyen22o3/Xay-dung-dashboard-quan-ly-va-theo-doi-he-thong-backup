export interface Theme {
  readonly isDark: boolean
  readonly bg: string
  readonly cardBg: string
  readonly cardBorder: string
  readonly textPrimary: string
  readonly textSecondary: string
  readonly gridLine: string
  readonly accentText: string
  readonly primary: string
  readonly onPrimary: string
  readonly successText: string
  readonly errorText: string
  readonly titleColor: string
}

export function makeTheme(isDark: boolean): Theme {
  return {
    isDark,
    bg: isDark ? '#0f141a' : '#f4f7fb',
    cardBg: isDark ? '#151c24' : '#ffffff',
    cardBorder: isDark ? '#2b3643' : '#dce4ee',
    textPrimary: isDark ? '#f1f5fb' : '#182433',
    textSecondary: isDark ? '#9baabd' : '#61738b',
    gridLine: isDark ? '#273341' : '#e5edf6',
    accentText: isDark ? '#60a5fa' : '#1d63ed',
    primary: '#1d63ed',
    onPrimary: '#ffffff',
    successText: isDark ? '#08c889' : '#008d62',
    errorText: isDark ? '#fb7185' : '#d5344e',
    titleColor: isDark ? '#f7faff' : '#14243b',
  }
}
