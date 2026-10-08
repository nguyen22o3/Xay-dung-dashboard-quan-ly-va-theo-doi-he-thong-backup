import { useEffect, useState } from 'react'
import type { Lang } from './language'
import { taskProcess, type TaskProgress as Progress, type TaskProcess } from '../api/operations'
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
export default function TaskProgress({ id, lang }: {id: string; lang: Lang}) {
  const [state, setState] = useState<TaskProcess | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  useEffect(() => {
    let stopped = false, timer = 0
    const poll = async () => { try {const next = await taskProcess(id); if (!stopped) {setState(next); setUnavailable(false)}} catch {if (!stopped) setUnavailable(true)} finally {if (!stopped) timer = window.setTimeout(() => void poll(), 5000)} }
    void poll(); return () => {stopped = true; window.clearTimeout(timer)}
  }, [id])
  if (unavailable) return <small>{lang === 'vi' ? 'Chưa đọc được tiến độ' : 'Progress unavailable'}</small>
  if (!state?.running) return null
  return <>{state.progress ? <ProgressView progress={state.progress} lang={lang} /> : <small>{lang === 'vi' ? 'Đang chạy · chưa có số liệu để tính %' : 'Running · percentage unavailable'}</small>}<small>{lang === 'vi' ? 'Đã chạy' : 'Elapsed'}: {state.elapsedSeconds}s</small>{id === 'drive-sync' && <small>{lang === 'vi' ? '% và ETA tính cho lệnh tải/ngày hiện tại, không phải toàn tác vụ.' : '% and ETA refer to the current transfer/day, not the entire job.'}</small>}</>
}
