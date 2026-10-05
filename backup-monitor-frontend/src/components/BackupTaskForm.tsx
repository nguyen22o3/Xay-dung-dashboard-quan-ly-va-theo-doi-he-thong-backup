import { backupTaskLabels, cronScheduleIsStale, cycleLabels, proposedCronSchedule } from '../backupTasks'
import type { CronScheduleDraft } from '../backupTasks'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import { formatCronSchedule } from '../utils'

type Props = {
  lang: Lang; jobs: CronJob[]; selected?: CronJob; draft: CronScheduleDraft | null
  disabled: boolean; loading: boolean; error: string | null; scriptsDir: string
  idPrefix?: string; selectionLocked?: boolean
  onSelect: (id: string) => void; onDraftChange: (change: Partial<CronScheduleDraft>) => void; onReset: () => void
}

// Controlled fields only: schedule and storage share the parent's preview/apply.
export default function BackupTaskForm({ lang, jobs, selected, draft, disabled, loading, error, scriptsDir, idPrefix = '', selectionLocked = false, onSelect, onDraftChange, onReset }: Props) {
  const vi = lang === 'vi'
  if (!selected || !draft) return <p className="backup-system-note">{loading ? (vi ? 'Đang tải tác vụ...' : 'Loading tasks...') : (vi ? 'Chưa có cron được quản lý.' : 'No managed cron jobs.')}</p>
  const stale = cronScheduleIsStale(draft, selected)
  const locked = disabled || !!error || stale || selected.status === 'running' || typeof selected.enabled !== 'boolean'
  const proposed = proposedCronSchedule(draft)
  const showHour = !['hourly', 'hours', 'minutes'].includes(draft.cycle)
  const weekLabels = vi ? ['Chủ nhật', 'Thứ hai', 'Thứ ba', 'Thứ tư', 'Thứ năm', 'Thứ sáu', 'Thứ bảy'] : ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
  const script = scriptsDir.replace(/\/$/, '') + '/' + selected.script.split('/').at(-1)
  return <div className="backup-task-embedded">
    <div className="backup-system-fields">
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-type`}>{vi ? 'Loại tác vụ (Task type)' : 'Task type'}</label><select id={`${idPrefix}backup-task-type`} value={selected.id} disabled={disabled || selectionLocked} onChange={event => onSelect(event.target.value)}>{jobs.map(job => <option key={job.id} value={job.id}>{backupTaskLabels[job.id]?.[lang] ?? job.name}</option>)}</select></div>
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-script`}>{vi ? 'Script thực hiện' : 'Execution script'}</label><input id={`${idPrefix}backup-task-script`} readOnly value={script} spellCheck={false} /></div>
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-cycle`}>{vi ? 'Chu kỳ thực hiện' : 'Execute cycle'}</label>
        <div className="backup-task-cycle">
          <select id={`${idPrefix}backup-task-cycle`} disabled={locked} value={draft.cycle} onChange={event => onDraftChange({ cycle: event.target.value as CronScheduleDraft['cycle'] })}>{Object.entries(cycleLabels).filter(([key]) => key !== 'current' || draft.cycle === 'current').map(([key, label]) => <option key={key} value={key}>{label[lang]}</option>)}</select>
          {['days', 'hours', 'minutes'].includes(draft.cycle) && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="[0-9]{1,2}" aria-label={vi ? 'Khoảng cách chu kỳ' : 'Cycle interval'} value={draft.every} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ every: event.target.value }) }} required /><span>{draft.cycle === 'days' ? (vi ? 'Ngày' : 'Days') : draft.cycle === 'hours' ? (vi ? 'Giờ' : 'Hours') : (vi ? 'Phút' : 'Minutes')}</span></div>}
          {draft.cycle === 'weekly' && <select aria-label={vi ? 'Ngày trong tuần' : 'Weekday'} value={draft.weekday} disabled={locked} onChange={event => onDraftChange({ weekday: event.target.value })}>{weekLabels.map((label, day) => <option key={day} value={day}>{label}</option>)}</select>}
          {draft.cycle === 'monthly' && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="(?:[1-9]|[12][0-9]|3[01])" aria-label={vi ? 'Ngày trong tháng, 1–31' : 'Day of month, 1–31'} value={draft.day} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ day: event.target.value }) }} required /><span>{vi ? 'Ngày' : 'Day'}</span></div>}
          {showHour && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="(?:[01]?[0-9]|2[0-3])" aria-label={vi ? 'Giờ thực hiện, 00–23' : 'Execution hour, 00–23'} value={draft.hour} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ hour: event.target.value }) }} required /><span>{vi ? 'Giờ' : 'Hours'}</span></div>}
          {draft.cycle !== 'minutes' && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="[0-5]?[0-9]" aria-label={vi ? 'Phút thực hiện, 00–59' : 'Execution minute, 00–59'} value={draft.minute} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ minute: event.target.value }) }} required /><span>{vi ? 'Phút' : 'Minutes'}</span></div>}
          <span className="backup-cycle-preview" role="status">{proposed ? formatCronSchedule(proposed, lang) : (vi ? 'Nhập giá trị chu kỳ hợp lệ' : 'Enter valid schedule values')}</span>
        </div>
      </div>
    </div>
    {(stale || error) && <div className="backup-task-editor-notes">
      <p className="backup-system-error" role="alert">{error || (vi ? 'Lịch hoặc trạng thái trên máy chủ đã thay đổi. Tải lịch hiện tại trước khi xem trước.' : 'The server schedule or state changed. Reload the current schedule before previewing.')}</p>
      <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onReset}>{vi ? 'Tải lịch hiện tại' : 'Reload current schedule'}</button>
    </div>}
  </div>
}
