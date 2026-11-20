import type { TaskProgress as Progress } from '../api/operations'
import type { Lang } from './language'
import { formatBytes } from './utils'

export function ProgressView({ progress, lang }: {progress: Progress; lang: Lang}) {
  const vi = lang === 'vi'
  return <div className="task-progress">
    <span>{progress.phase.startsWith('day:') ? `${vi ? 'Ngày' : 'Day'} ${progress.phase.slice(4)}` : ({download: vi ? 'Tải backup' : 'Downloading', extract: vi ? 'Giải nén' : 'Extracting', transfer: vi ? 'Truyền dữ liệu' : 'Transfer', cleanup: vi ? 'Dọn dẹp' : 'Cleanup'}[progress.phase] ?? progress.phase)}{progress.percent != null ? ` · ${progress.percent.toFixed(1)}%` : ''}</span>
    {progress.percent != null && <progress max={100} value={progress.percent} aria-label={vi ? 'Tiến độ giai đoạn hiện tại' : 'Current phase progress'} />}
    <small>{formatBytes(progress.bytes)}{progress.totalBytes > 0 ? ` / ${formatBytes(progress.totalBytes)}` : ''} · {progress.files}{progress.totalFiles > 0 ? ` / ${progress.totalFiles}` : ''} {vi ? 'tệp' : 'files'}{progress.speed > 0 ? ` · ${formatBytes(progress.speed)}/s` : ''}{progress.eta != null ? ` · ETA ${Math.ceil(progress.eta)}s` : ''}</small>
    {progress.stale && <small>{vi ? 'Số liệu chưa cập nhật; tác vụ vẫn đang chạy.' : 'Stats are stale; the process is still running.'}</small>}
  </div>
}
