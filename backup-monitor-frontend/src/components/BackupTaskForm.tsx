import { useState } from 'react'
import { Save } from 'lucide-react'
import { apiErrorMessage, updateCronJobSchedule } from '../api'
import { backupTaskLabels, cronClock, cronHours, cronMinutes, withCronClock } from '../backupTasks'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import { formatCronSchedule } from '../utils'

type Props = {
  lang: Lang; jobs: CronJob[]; selectedId: string; disabled: boolean; loading: boolean; error: string | null
  onSelect: (id: string) => void
  onBusyChange: (busy: boolean) => void; onSaved: (job: CronJob, time: string) => void
}

function ScheduleFields({ job, lang, disabled, onBusyChange, onSaved }: Pick<Props, 'lang' | 'disabled' | 'onBusyChange' | 'onSaved'> & { job: CronJob }) {
  const vi = lang === 'vi'
  // Keep the schedule used to initialize this draft. Polling must not silently
  // replace a user's unsaved clock or the stale-write guard sent to the server.
  const [expectedSchedule, setExpectedSchedule] = useState(job.schedule)
  const [expectedEnabled, setExpectedEnabled] = useState(job.enabled)
  const initialClock = cronClock(expectedSchedule)
  const [hour, setHour] = useState(initialClock.hour), [minute, setMinute] = useState(initialClock.minute)
  const [busy, setBusy] = useState(false), [error, setError] = useState('')
  const proposedSchedule = withCronClock(expectedSchedule, hour, minute)
  const stale = job.schedule !== expectedSchedule || job.enabled !== expectedEnabled
  const changed = `${hour}:${minute}` !== `${initialClock.hour}:${initialClock.minute}`
  const locked = disabled || busy || job.status === 'running' || typeof job.enabled !== 'boolean'
  const daily = expectedSchedule.trim().split(/\s+/).slice(2).every(field => field === '*')
  const reset = () => {
    const current = cronClock(job.schedule)
    setExpectedSchedule(job.schedule); setExpectedEnabled(job.enabled); setHour(current.hour); setMinute(current.minute); setError('')
  }
  const save = async () => {
    if (locked || stale || !changed || !proposedSchedule || typeof expectedEnabled !== 'boolean') return
    setBusy(true); onBusyChange(true); setError('')
    try {
      await updateCronJobSchedule(job.id, `${hour}:${minute}`, expectedSchedule, expectedEnabled)
      setExpectedSchedule(proposedSchedule)
      onSaved(job, `${hour}:${minute}`)
    } catch (error: unknown) {
      setError(apiErrorMessage(error, vi ? 'Không thể lưu lịch chạy. Hãy tải lại lịch hiện tại rồi thử lại.' : 'Could not save. Reload the current schedule and try again.'))
    } finally { setBusy(false); onBusyChange(false) }
  }
  return <form onSubmit={event => { event.preventDefault(); void save() }}>
    <div className="backup-system-fields">
      <div className="apex-settings-field"><label htmlFor="backup-task-name">{vi ? 'Tên tác vụ' : 'Task name'}</label><input id="backup-task-name" readOnly value={backupTaskLabels[job.id]?.[lang] ?? job.name} /></div>
      <div className="apex-settings-field"><label htmlFor="backup-task-script">{vi ? 'Script thực hiện' : 'Execution script'}</label><input id="backup-task-script" readOnly value={job.script} spellCheck={false} /><small>{vi ? 'Script lấy từ cấu hình máy chủ; chọn loại tác vụ sẽ chọn đúng file, không nhập lệnh shell tùy ý.' : 'The script comes from server configuration. Task type selects its file; arbitrary shell commands are not accepted.'}</small></div>
      <div className="apex-settings-field"><label htmlFor="backup-task-cycle">{vi ? 'Chu kỳ thực hiện' : 'Execute cycle'}</label>
        <div className="backup-task-cycle">
          <select id="backup-task-cycle" disabled value="current"><option value="current">{daily ? (vi ? 'Hằng ngày' : 'Daily') : (vi ? 'Theo lịch hiện tại' : 'Current recurrence')}</option></select>
          <div className="backup-time-unit"><select aria-label={vi ? 'Giờ thực hiện, 00–23' : 'Execution hour, 00–23'} value={hour} disabled={locked} onChange={event => { setHour(event.target.value); setError('') }} required><option value="">--</option>{cronHours.map(value => <option key={value}>{value}</option>)}</select><span>{vi ? 'Giờ' : 'Hours'}</span></div>
          <div className="backup-time-unit"><select aria-label={vi ? 'Phút thực hiện, 00–59' : 'Execution minute, 00–59'} value={minute} disabled={locked} onChange={event => { setMinute(event.target.value); setError('') }} required><option value="">--</option>{cronMinutes.map(value => <option key={value}>{value}</option>)}</select><span>{vi ? 'Phút' : 'Minutes'}</span></div>
          <span className="backup-cycle-preview" role="status">{proposedSchedule ? formatCronSchedule(proposedSchedule, lang) : (vi ? 'Chọn giờ và phút' : 'Choose a time')}</span>
        </div>
        <small>{vi ? 'Giờ máy chủ · định dạng 24h. Chỉ đổi giờ/phút; giữ nguyên ngày chạy và tham số script.' : 'Server time · 24-hour format. Only hour/minute change; recurrence and script arguments stay unchanged.'}</small>
      </div>
    </div>
    <div className="backup-task-editor-notes">
      <p className="backup-system-note">{vi ? 'Lịch đã lưu: ' : 'Saved schedule: '}{formatCronSchedule(job.schedule, lang)}{job.enabled === false ? (vi ? ' · đang tạm dừng' : ' · paused') : ''}</p>
      {stale && <p className="backup-system-warning" role="alert">{vi ? 'Lịch hoặc trạng thái trên máy chủ đã thay đổi. Tải lịch hiện tại trước khi lưu để tránh ghi đè.' : 'The server schedule or state changed. Reload it before saving to avoid overwriting changes.'}</p>}
      {error && <p className="backup-system-error" role="alert">{error}</p>}
      <p className="backup-system-note">{vi ? 'Chọn loại tác vụ không chạy script. “Lưu lịch chạy” chỉ đổi giờ, giữ nguyên trạng thái bật/tạm dừng. Nên xếp sao lưu → đồng bộ Drive → kiểm tra.' : 'Selecting a type does not execute its script. Save schedule changes the time and preserves the enabled/paused state. Schedule backups before Drive uploads and checks.'}</p>
    </div>
    <div className="backup-system-actions">
      <button type="submit" className="apex-settings-save" disabled={locked || stale || !changed || !proposedSchedule}><Save size={15} />{busy ? (vi ? 'Đang lưu...' : 'Saving...') : (vi ? 'Lưu lịch chạy' : 'Save schedule')}</button>
      {(stale || changed || error) && <button type="button" className="backup-system-secondary" disabled={locked} onClick={reset}>{vi ? 'Tải lịch hiện tại' : 'Reload current schedule'}</button>}
    </div>
  </form>
}

export default function BackupTaskForm(props: Props) {
  const { jobs, lang, selectedId, disabled, loading, error, onSelect } = props
  const vi = lang === 'vi'
  const selected = jobs.find(job => job.id === selectedId) ?? jobs.find(job => job.id === 'backup-database') ?? jobs[0]
  return <div className="backup-task-embedded">
      {error && <p role="alert" className="backup-system-error">{error}</p>}
      {!selected ? <p className="backup-system-note">{loading ? (vi ? 'Đang tải tác vụ từ máy chủ...' : 'Loading server tasks...') : (vi ? 'Chưa có cron được quản lý. Kiểm tra crontab máy chủ.' : 'No managed cron jobs. Check the server crontab.')}</p> : <>
        <div className="backup-system-fields backup-task-type-field"><div className="apex-settings-field"><label htmlFor="backup-task-type">{vi ? 'Loại tác vụ (Task type)' : 'Task type'}</label><select id="backup-task-type" value={selected.id} disabled={disabled} onChange={event => onSelect(event.target.value)}>{jobs.map(job => <option key={job.id} value={job.id}>{backupTaskLabels[job.id]?.[lang] ?? job.name}</option>)}</select></div></div>
        <ScheduleFields key={selected.id} job={selected} lang={lang} disabled={disabled || !!error} onBusyChange={props.onBusyChange} onSaved={props.onSaved} />
      </>}
    </div>
}
