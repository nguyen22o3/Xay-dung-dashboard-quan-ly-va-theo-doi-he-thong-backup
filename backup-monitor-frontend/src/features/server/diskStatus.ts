import type { Lang } from '../../shared/language'

export const DISK_USAGE_WARNING_THRESHOLD = 80
// Preserve the existing red low-space alert at 85% and above.
export const DISK_USAGE_DANGER_THRESHOLD = 85

export function diskStatusPresentation(usage: number | null, lang: Lang) {
  if (usage === null || !Number.isFinite(usage) || usage < 0 || usage > 100) {
    return { label: lang === 'vi' ? 'Chưa xác nhận' : 'Unconfirmed', tone: 'neutral' as const }
  }
  if (usage >= DISK_USAGE_DANGER_THRESHOLD) {
    return { label: lang === 'vi' ? 'Sắp hết dung lượng' : 'Low disk space', tone: 'error' as const }
  }
  if (usage > DISK_USAGE_WARNING_THRESHOLD) {
    return { label: lang === 'vi' ? 'Sắp hết dung lượng' : 'Low disk space', tone: 'warning' as const }
  }
  return { label: lang === 'vi' ? 'Bình thường' : 'Normal', tone: 'healthy' as const }
}
