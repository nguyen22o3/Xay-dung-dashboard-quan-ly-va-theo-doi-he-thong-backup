import { localizeMessage } from '../../shared/messages'
import { Button } from '../../shared/ui'
import { useMemo, useRef, useState } from 'react'
import { apiErrorMessage } from '../../api/dashboard'
import { cronLogForDisplay, cronLogLineLevel, filterCronLog } from './cronLogDisplay'
import { readLogPage } from '../../api/operations'
import type { Lang } from '../../shared/language'

export default function CronLogTools({ content, jobId, selector = jobId, lang }: { content: string; jobId: string; selector?: string; lang: Lang }) {
  const vi = lang === 'vi'
  const [pages, setPages] = useState<string | null>(null)
  const [cursor, setCursor] = useState<string | undefined>()
  const [query, setQuery] = useState('')
  const [level, setLevel] = useState<'all' | 'error' | 'warning'>('all')
  const [highlight, setHighlight] = useState(false)
  const [busy, setBusy] = useState(false)
  const guard = useRef(false)
  const [error, setError] = useState('')
  const [partial, setPartial] = useState(false)
  const raw = pages ?? content
  const display = useMemo(() => cronLogForDisplay(jobId, raw), [jobId, raw])
  const lines = useMemo(() => filterCronLog(display.content, query, level), [display.content, query, level])
  const capped = raw.length >= 1024 * 1024
  const older = async () => {
    if (guard.current) return
    guard.current = true; setBusy(true); setError('')
    try {
      const page = await readLogPage(selector, cursor)
      if (!page.exists) throw new Error(vi ? 'File log không còn tồn tại.' : 'Log file no longer exists.')
      setPages(previous => previous === null || capped ? page.content : page.content + previous)
      setCursor(page.olderCursor); setPartial('partialLine' in page && page.partialLine === true)
    } catch (e) { setError(apiErrorMessage(e, vi ? 'Không đọc được log cũ. Hãy làm mới.' : 'Could not read older log. Refresh and retry.')) }
    finally { guard.current = false; setBusy(false) }
  }
  return <>
    <div className="ops-log-tools">
      <input type="search" aria-label={vi ? 'Tìm trong log đã tải' : 'Search loaded log'} placeholder={vi ? 'Tìm trong log đã tải…' : 'Search loaded log…'} value={query} onChange={e => setQuery(e.target.value)} />
      <select aria-label={vi ? 'Lọc mức log' : 'Log level'} value={level} onChange={e => setLevel(e.target.value as typeof level)}><option value="all">{vi ? 'Tất cả dòng' : 'All lines'}</option><option value="error">ERROR</option><option value="warning">WARNING</option></select>
      <label><input type="checkbox" checked={highlight} onChange={e => setHighlight(e.target.checked)} />{vi ? 'Tô màu lỗi' : 'Highlight errors'}</label>
      <Button type="button" onClick={() => void older()} disabled={busy || (pages !== null && !cursor)}>{busy ? (vi ? 'Đang tải…' : 'Loading…') : pages === null ? (vi ? 'Xem log phân trang' : 'Paged log') : (vi ? 'Trang cũ hơn' : 'Older page')}</Button>
    </div>
    <div className="cron-log-body" role="status">
      {error && <p className="cron-log-error">{localizeMessage(error, lang)}</p>}
      {partial && <p className="cron-log-note">{vi ? 'Đầu trang có thể là một phần dòng; tải trang cũ hơn để xem phần trước.' : 'The first line may be partial; load the previous page for context.'}</p>}
      {capped && <p className="cron-log-note">{vi ? 'Đã tải khoảng 1 MB. Trang tiếp theo thay cửa sổ hiển thị; không xóa dữ liệu lưu trữ.' : 'About 1 MB loaded. The next page replaces the display window, not stored data.'}</p>}
      {(query || level !== 'all') && <p className="cron-log-note">{vi ? 'Chỉ tìm/lọc phần log đã tải; dòng ngữ cảnh có thể không nằm trong kết quả.' : 'Search/filter applies to loaded text only; context lines may be excluded.'}</p>}
      {lines.some(line => line !== '') ? <pre>{lines.map((line, index) => <span key={index} className={highlight ? `ops-log-${cronLogLineLevel(line)}` : undefined}>{line}{index < lines.length - 1 ? '\n' : ''}</span>)}</pre> : <p>{vi ? 'Không có dòng log khớp.' : 'No matching log lines.'}</p>}
    </div>
  </>
}
