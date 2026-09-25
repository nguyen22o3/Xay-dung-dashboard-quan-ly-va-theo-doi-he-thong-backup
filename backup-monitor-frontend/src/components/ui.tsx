import type { CSSProperties, ReactNode } from 'react'
import type { Theme } from '../theme'
import type { Lang } from '../language'
import { tr } from '../language'

export function Card({
  theme,
  style,
  children,
}: {
  theme: Theme
  style?: CSSProperties
  children: ReactNode
}) {
  const base: CSSProperties = {
    backgroundColor: theme.cardBg,
    border: `1px solid ${theme.cardBorder}`,
    boxShadow: theme.isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)',
    borderRadius: '6px',
  }
  return <div style={{ ...base, ...style }}>{children}</div>
}

export function StatusBadge({
  theme,
  tone,
  children,
}: {
  theme: Theme
  tone: 'success' | 'failed' | 'warning' | 'neutral'
  children: ReactNode
}) {
  const map = {
    success: {
      bg: theme.isDark ? '#1b4d3e' : '#e8f5e9',
      color: theme.successText,
    },
    failed: {
      bg: theme.isDark ? '#4a1111' : '#ffebee',
      color: theme.errorText,
    },
    warning: {
      bg: theme.isDark ? '#4a3f11' : '#fff8e1',
      color: theme.warningText,
    },
    neutral: {
      bg: theme.isDark ? '#2a2a2b' : '#f0f0f0',
      color: theme.textSecondary,
    },
  }[tone]

  return (
    <span
      style={{
        padding: '2px 10px',
        borderRadius: '10px',
        fontSize: '11px',
        fontWeight: 'bold',
        backgroundColor: map.bg,
        color: map.color,
        display: 'inline-block',
      }}
    >
      {children}
    </span>
  )
}

export function Button({
  theme,
  variant = 'secondary',
  style,
  children,
  ...rest
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  theme: Theme
  variant?: 'primary' | 'secondary' | 'success' | 'danger'
  children: ReactNode
}) {
  const bgMap: Record<string, string> = {
    primary: theme.headerBg,
    success: theme.successText,
    secondary: theme.isDark ? '#3a3a3c' : '#e2e2e2',
    danger: theme.errorText,
  }
  const colorMap: Record<string, string> = {
    primary: 'white',
    success: 'white',
    secondary: theme.textPrimary,
    danger: 'white',
  }
  return (
    <button
      {...rest}
      style={{
        padding: '8px 16px',
        backgroundColor: bgMap[variant],
        color: colorMap[variant],
        border: 'none',
        borderRadius: '4px',
        cursor: rest.disabled ? 'not-allowed' : 'pointer',
        opacity: rest.disabled ? 0.55 : 1,
        fontSize: '13px',
        display: 'inline-flex',
        alignItems: 'center',
        gap: '6px',
        ...style,
      }}
    >
      {children}
    </button>
  )
}

export function Spinner({ theme }: { theme: Theme }) {
  return <span style={{ display: 'inline-block', fontSize: '12px', color: theme.textSecondary }}>⟳</span>
}

export function ErrorNote({
  theme,
  lang,
  message,
  onRetry,
}: {
  theme: Theme
  lang: Lang
  message?: string
  onRetry?: () => void
}) {
  return (
    <div
      style={{
        backgroundColor: theme.isDark ? '#3a1616' : '#ffebee',
        border: `1px solid ${theme.errorText}`,
        color: theme.errorText,
        padding: '10px 14px',
        borderRadius: '6px',
        fontSize: '13px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: '10px',
        marginBottom: '15px',
      }}
    >
      <span>
        {message || tr(lang, 'connectionError')} — {tr(lang, 'lastUpdated')}:{' '}
        {new Date().toLocaleTimeString()}
      </span>
      {onRetry && (
        <button
          onClick={onRetry}
          style={{
            background: 'none',
            border: `1px solid ${theme.errorText}`,
            color: theme.errorText,
            borderRadius: '4px',
            padding: '4px 10px',
            cursor: 'pointer',
            fontSize: '12px',
            flexShrink: 0,
          }}
        >
          {tr(lang, 'reload')}
        </button>
      )}
    </div>
  )
}

export function Modal({
  theme,
  title,
  onClose,
  children,
  wide,
}: {
  theme: Theme
  title: string
  onClose: () => void
  children: ReactNode
  wide?: boolean
}) {
  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(0,0,0,0.55)',
        zIndex: 1000,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '20px',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          backgroundColor: theme.cardBg,
          border: `1px solid ${theme.cardBorder}`,
          borderRadius: '8px',
          width: wide ? '90%' : '560px',
          maxWidth: '1100px',
          maxHeight: '85vh',
          display: 'flex',
          flexDirection: 'column',
          boxShadow: '0 10px 40px rgba(0,0,0,0.35)',
        }}
      >
        <div
          style={{
            padding: '14px 18px',
            borderBottom: `1px solid ${theme.gridLine}`,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
          }}
        >
          <strong style={{ color: theme.textPrimary, fontSize: '14px' }}>{title}</strong>
          <button
            onClick={onClose}
            style={{
              background: 'none',
              border: 'none',
              cursor: 'pointer',
              fontSize: '18px',
              color: theme.textSecondary,
              lineHeight: 1,
            }}
          >
            ×
          </button>
        </div>
        <div style={{ padding: '18px', overflow: 'auto', color: theme.textPrimary }}>{children}</div>
      </div>
    </div>
  )
}