import { useEffect, useState } from 'react'
import type { Lang } from '../../shared/language'
import { taskProcess, type TaskProcess } from '../../api/operations'
import { ProgressView } from '../../shared/ProgressView'

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
