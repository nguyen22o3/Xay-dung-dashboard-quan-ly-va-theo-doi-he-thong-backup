import { backupTaskLabels, cronHours, cronMinutes, cronScheduleIsStale, withCronClock } from '../backupTasks'
import type { CronScheduleDraft } from '../backupTasks'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import { formatCronSchedule } from '../utils'

type Props = {
  lang: Lang; jobs: CronJob[]; selected?: CronJob; draft: CronScheduleDraft | null
  disabled: boolean; loading: boolean; error: string | null; scriptsDir: string
  onSelect: (id: string) => void; onClockChange: (hour: string, minute: string) => void; onReset: () => void
}

// Controlled fields only: schedule and storage share the parent's preview/apply.
export default function BackupTaskForm({ lang, jobs, selected, draft, disabled, loading, error, scriptsDir, onSelect, onClockChange, onReset }: Props) {
  const vi = lang === 'vi'
  if (!selected || !draft) return <p className="backup-system-note">{loading ? (vi ? 'Đang tải tác vụ...' : 'Loading tasks...') : (vi ? 'Chưa có cron được quản lý.' : 'No managed cron jobs.')}</p>
  const stale = cronScheduleIsStale(draft, selected)
  const locked = disabled || !!error || stale || selected.status === 'running' || typeof selected.enabled !== 'boolean'
  const proposed = withCronClock(draft.expectedSchedule, draft.hour, draft.minute)
  const daily = draft.expectedSchedule.trim().split(/\s+/).slice(2).every(field => field === '*')
  const script = scriptsDir.replace(/\/$/, '') + '/' + selected.script.split('/').at(-1)
  return <div className="backup-task-embedded">
    <div className="backup-system-fields">
      <div className="apex-settings-field"><label htmlFor="backup-task-type">{vi ? 'Loại tác vụ (Task type)' : 'Task type'}</label><select id="backup-task-type" value={selected.id} disabled={disabled} onChange={event => onSelect(event.target.value)}>{jobs.map(job => <option key={job.id} value={job.id}>{backupTaskLabels[job.id]?.[lang] ?? job.name}</option>)}</select></div>
      <div className="apex-settings-field"><label htmlFor="backup-task-name">{vi ? 'Tên tác vụ' : 'Task name'}</label><input id="backup-task-name" readOnly value={backupTaskLabels[selected.id]?.[lang] ?? selected.name} /></div>
      <div className="apex-settings-field"><label htmlFor="backup-task-script">{vi ? 'Script thực hiện' : 'Execution script'}</label><input id="backup-task-script" readOnly value={script} spellCheck={false} /><small>{vi ? 'Tự chọn theo loại tác vụ và thư mục scripts bên dưới; không nhập lệnh shell tùy ý.' : 'Selected automatically from task type and the scripts directory below; arbitrary shell commands are not accepted.'}</small></div>
      <div className="apex-settings-field"><label htmlFor="backup-task-cycle">{vi ? 'Chu kỳ thực hiện' : 'Execute cycle'}</label>
        <div className="backup-task-cycle">
          <select id="backup-task-cycle" disabled value="current"><option value="current">{daily ? (vi ? 'Hằng ngày' : 'Daily') : (vi ? 'Theo lịch hiện tại' : 'Current recurrence')}</option></select>
          <div className="backup-time-unit"><select aria-label={vi ? 'Giờ thực hiện, 00–23' : 'Execution hour, 00–23'} value={draft.hour} disabled={locked} onChange={event => onClockChange(event.target.value, draft.minute)} required><option value="">--</option>{cronHours.map(value => <option key={value}>{value}</option>)}</select><span>{vi ? 'Giờ' : 'Hours'}</span></div>
          <div className="backup-time-unit"><select aria-label={vi ? 'Phút thực hiện, 00–59' : 'Execution minute, 00–59'} value={draft.minute} disabled={locked} onChange={event => onClockChange(draft.hour, event.target.value)} required><option value="">--</option>{cronMinutes.map(value => <option key={value}>{value}</option>)}</select><span>{vi ? 'Phút' : 'Minutes'}</span></div>
          <span className="backup-cycle-preview" role="status">{proposed ? formatCronSchedule(proposed, lang) : (vi ? 'Chọn giờ và phút' : 'Choose a time')}</span>
        </div>
        <small>{vi ? 'Giờ máy chủ · 24h. Giữ nguyên ngày chạy, tham số và trạng thái bật/tạm dừng.' : 'Server time · 24h. Recurrence, arguments and enabled/paused state stay unchanged.'} {vi ? 'Lịch đã lưu: ' : 'Saved: '}{formatCronSchedule(selected.schedule, lang)}{selected.enabled === false ? (vi ? ' · đang tạm dừng' : ' · paused') : ''}</small>
      </div>
    </div>
    {(stale || error) && <div className="backup-task-editor-notes">
      <p className="backup-system-error" role="alert">{error || (vi ? 'Lịch hoặc trạng thái trên máy chủ đã thay đổi. Tải lịch hiện tại trước khi xem trước.' : 'The server schedule or state changed. Reload the current schedule before previewing.')}</p>
      <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onReset}>{vi ? 'Tải lịch hiện tại' : 'Reload current schedule'}</button>
    </div>}
  </div>
}
