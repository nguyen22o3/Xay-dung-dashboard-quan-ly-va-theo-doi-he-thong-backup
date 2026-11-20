import type { CSSProperties, ReactNode } from 'react'
import type { Theme } from './theme'
import type { Lang } from './language'

// All actions share the same semantic styles; forms opt into type="submit".
export function Button({
  theme,
  variant = 'secondary',
  size = 'default',
  iconOnly = false,
  className = '',
  style,
  type = 'button',
  children,
  ...rest
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  theme?: Theme
  variant?: 'primary' | 'secondary' | 'success' | 'danger' | 'soft' | 'danger-outline'
  size?: 'default' | 'small'
  iconOnly?: boolean
  children: ReactNode
}) {
  const colors = theme ? {
    '--app-card': theme.cardBg,
    '--app-border': theme.cardBorder,
    '--app-text': theme.textPrimary,
    '--app-muted': theme.textSecondary,
    '--app-accent': theme.accentText,
    '--app-primary': theme.primary,
    '--app-danger': theme.errorText,
    '--app-on-accent': theme.onPrimary,
  } as CSSProperties : undefined
  return (
    <button
      {...rest}
      type={type}
      className={`ui-button ${iconOnly ? 'ui-button--icon' : ''} ${className}`.trim()}
      data-variant={variant}
      data-size={size}
      style={{ ...colors, ...style }}
    >
      {children}
    </button>
  )
}

export function OperationStatus({ state, lang }: { state: string; lang: Lang }) {
  const labels: Record<string, [string, string, string]> = {
    success: ['Thành công', 'Success', 'success'],
    ready: ['Đã chuẩn bị', 'Ready', 'success'],
    failed: ['Thất bại', 'Failed', 'failed'],
    cancelled: ['Đã hủy', 'Cancelled', 'warning'],
    running: ['Đang chạy', 'Running', 'warning'],
    queued: ['Đang chờ', 'Queued', 'neutral'],
    preview: ['Xem trước', 'Preview', 'neutral'],
    unknown: ['Chưa xác nhận', 'Unconfirmed', 'neutral'],
  }
  const label = labels[state]
  return <span className="ui-status" data-tone={label?.[2] ?? 'neutral'}>{label ? label[lang === 'vi' ? 0 : 1] : state}</span>
}
