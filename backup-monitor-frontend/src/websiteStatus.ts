import type { Lang } from './language'
import type { WebsiteStatus } from './types'

export function websiteStatusPresentation(site: WebsiteStatus, lang: Lang) {
  const vi = lang === 'vi'
  const definitions: Record<string, { label: string; tone: 'healthy' | 'warning' | 'error' | 'neutral'; description: string }> = {
    ONLINE: { label: 'ONLINE', tone: 'healthy', description: vi ? 'Website đang phản hồi.' : 'The website is responding.' },
    SLOW: { label: vi ? 'CHẬM' : 'SLOW', tone: 'warning', description: vi ? 'Website vẫn phản hồi nhưng mất từ 2 giây trở lên.' : 'The website responds, but takes at least 2 seconds.' },
    TIMEOUT: { label: vi ? 'HẾT GIỜ CHỜ' : 'TIMEOUT', tone: 'warning', description: vi ? 'Hết thời gian chờ kiểm tra (tối đa 15 giây); chưa kết luận website đã tắt.' : 'The probe timed out (up to 15 seconds); this does not prove an outage.' },
    OFFLINE: { label: 'OFFLINE', tone: 'error', description: vi ? 'Không thể kết nối đến website.' : 'Could not connect to the website.' },
    ERROR: { label: vi ? 'LỖI HTTP' : 'HTTP ERROR', tone: 'error', description: vi ? 'Máy chủ trả mã lỗi HTTP.' : 'The server returned an HTTP error.' },
    UNKNOWN: { label: vi ? 'CHƯA XÁC NHẬN' : 'UNKNOWN', tone: 'neutral', description: vi ? 'Chưa kiểm tra được trạng thái website.' : 'The website status could not be checked.' },
  }
  const presentation = definitions[site.status] ?? definitions.UNKNOWN
  const details = [site.code && site.code !== '000' ? `HTTP ${site.code}` : '', site.time].filter(Boolean).join(' · ')
  return { ...presentation, details, title: [presentation.description, details].filter(Boolean).join(' ') }
}
