import { backupTaskLabels, cronScheduleIsStale, cycleLabels, proposedCronSchedule } from './backupTasks'
import type { CronScheduleDraft } from './backupTasks'
import type { Lang } from '../../shared/language'
import type { CronJob } from '../../shared/types'
import { formatCronSchedule } from '../../shared/utils'
import type { ManagedScriptOption } from '../../api/backupSystem'
import { Folder } from 'lucide-react'

type Props = {
  lang: Lang; jobs: CronJob[]; selected?: CronJob; draft: CronScheduleDraft | null
  disabled: boolean; loading: boolean; error: string | null; scriptPath: string
  idPrefix?: string; selectionLocked?: boolean
  scriptChoices?: { options: ManagedScriptOption[]; value: string; loading: boolean; error: string; onOpen: () => void }
  onSelect: (id: string) => void; onDraftChange: (change: Partial<CronScheduleDraft>) => void; onReset: () => void
}

// Controlled fields only: schedule and storage share the parent's preview/apply.
export default function BackupTaskForm({ lang, jobs, selected, draft, disabled, loading, error, scriptPath, scriptChoices, idPrefix = '', selectionLocked = false, onSelect, onDraftChange, onReset }: Props) {
  const vi = lang === 'vi'
  if (!selected || !draft) return loading ? <p className="backup-system-note">{vi ? 'Đang tải tác vụ...' : 'Loading tasks...'}</p> : null
  const stale = cronScheduleIsStale(draft, selected)
  const locked = disabled || !!error || stale || selected.status === 'running' || typeof selected.enabled !== 'boolean'
  const proposed = proposedCronSchedule(draft)
  const showHour = !['hourly', 'hours', 'minutes'].includes(draft.cycle)
  const weekLabels = vi ? ['Chủ nhật', 'Thứ hai', 'Thứ ba', 'Thứ tư', 'Thứ năm', 'Thứ sáu', 'Thứ bảy'] : ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
  const choices = selectionLocked ? [selected] : jobs
  return <div className="backup-task-embedded">
    <div className="backup-system-fields">
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-type`}>{vi ? 'Loại tác vụ (Task type)' : 'Task type'}</label><select id={`${idPrefix}backup-task-type`} value={selected.id} disabled={disabled || selectionLocked} onChange={event => onSelect(event.target.value)}>{choices.map(job => <option key={job.id} value={job.id}>{backupTaskLabels[job.id]?.[lang] ?? job.name}</option>)}</select></div>
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-script`}>{vi ? 'Script thực hiện' : 'Execution script'}</label>{scriptChoices ? <>
        <div className="backup-system-path"><input id={`${idPrefix}backup-task-script`} readOnly value={scriptChoices.value === '__stale__' ? (vi ? 'Script đã thay đổi — chọn lại' : 'Script changed — select again') : scriptChoices.options.find(option => option.selector === scriptChoices.value)?.path ?? scriptPath} spellCheck={false} /><button className="backup-system-secondary" type="button" disabled={disabled} onClick={scriptChoices.onOpen} aria-haspopup="dialog" aria-label={vi ? 'Chọn script trên AlmaLinux' : 'Choose an AlmaLinux script'} title={vi ? 'Chọn script' : 'Choose script'}><Folder size={16} aria-hidden="true" /></button></div>
        {scriptChoices.error && <p className="backup-system-error" role="alert" style={{ gridColumn: '1 / -1', margin: 0 }}>{scriptChoices.error}</p>}
      </> : <input id={`${idPrefix}backup-task-script`} readOnly value={scriptPath} spellCheck={false} />}</div>
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-cycle`}>{vi ? 'Chu kỳ thực hiện' : 'Execute cycle'}</label>
        <div className="backup-task-cycle">
          <select id={`${idPrefix}backup-task-cycle`} disabled={locked} value={draft.cycle} onChange={event => onDraftChange({ cycle: event.target.value as CronScheduleDraft['cycle'] })}>{Object.entries(cycleLabels).filter(([key]) => key !== 'current' || draft.cycle === 'current').map(([key, label]) => <option key={key} value={key}>{label[lang]}</option>)}</select>
          {['days', 'hours', 'minutes'].includes(draft.cycle) && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="[0-9]{1,2}" aria-label={vi ? 'Khoảng cách chu kỳ' : 'Cycle interval'} value={draft.every} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ every: event.target.value }) }} required /><span>{draft.cycle === 'days' ? (vi ? 'Ngày' : 'Days') : draft.cycle === 'hours' ? (vi ? 'Giờ' : 'Hours') : (vi ? 'Phút' : 'Minutes')}</span></div>}
          {draft.cycle === 'weekly' && <select aria-label={vi ? 'Ngày trong tuần' : 'Weekday'} value={draft.weekday} disabled={locked} onChange={event => onDraftChange({ weekday: event.target.value })}>{weekLabels.map((label, day) => <option key={day} value={day}>{label}</option>)}</select>}
          {draft.cycle === 'monthly' && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="(?:[1-9]|[12][0-9]|3[01])" aria-label={vi ? 'Ngày trong tháng, 1–31' : 'Day of month, 1–31'} value={draft.day} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ day: event.target.value }) }} required /><span>{vi ? 'Ngày' : 'Day'}</span></div>}
          {draft.cycle !== 'minutes' && <div className="backup-task-clock">
            {showHour && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="(?:[01]?[0-9]|2[0-3])" aria-label={vi ? 'Giờ thực hiện, 00–23' : 'Execution hour, 00–23'} value={draft.hour} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ hour: event.target.value }) }} required /><span>{vi ? 'Giờ' : 'Hours'}</span></div>}
            <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="[0-5]?[0-9]" aria-label={vi ? 'Phút thực hiện, 00–59' : 'Execution minute, 00–59'} value={draft.minute} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ minute: event.target.value }) }} required /><span>{vi ? 'Phút' : 'Minutes'}</span></div>
          </div>}
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
