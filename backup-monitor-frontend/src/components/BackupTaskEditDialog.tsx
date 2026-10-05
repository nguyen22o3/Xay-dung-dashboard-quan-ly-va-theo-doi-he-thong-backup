import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import BackupSystemSettings from './BackupSystemSettings'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import type { ConfigurationPreview } from '../backupSystemApi'

type Props = {
  lang: Lang; job: CronJob; jobs: CronJob[]; loading: boolean; error: string | null
  busy: boolean; locked: boolean; onBusyChange: (busy: boolean) => void
  onClose: () => void; onReload: () => void; onSaved: (schedule?: ConfigurationPreview['schedule']) => void
}

export default function BackupTaskEditDialog({ lang, job, jobs, loading, error, busy, locked, onBusyChange, onClose, onReload, onSaved }: Props) {
  const dialog = useRef<HTMLDivElement>(null)
  const closeState = useRef({ busy, onClose })
  useEffect(() => { closeState.current = { busy, onClose } }, [busy, onClose])
  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    const containers = [document.body, document.querySelector<HTMLElement>('.app-content')].filter((element): element is HTMLElement => !!element)
    const overflow = containers.map(element => element.style.overflow)
    containers.forEach(element => { element.style.overflow = 'hidden' })
    const selector = 'button:not(:disabled), select:not(:disabled), input:not(:disabled)'
    dialog.current?.querySelector<HTMLElement>(selector)?.focus({ preventScroll: true })
    const keydown = (event: KeyboardEvent) => {
      // The nested folder picker owns focus and Escape while it is open.
      if (document.querySelector('.backup-folder-dialog')) return
      if (event.key === 'Escape' && !closeState.current.busy) closeState.current.onClose()
      if (event.key !== 'Tab') return
      const items = Array.from(dialog.current?.querySelectorAll<HTMLElement>(selector) ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => {
      document.removeEventListener('keydown', keydown)
      containers.forEach((element, index) => { element.style.overflow = overflow[index] })
      if (restore?.isConnected) restore.focus({ preventScroll: true })
    }
  }, [])
  return createPortal(<div className="backup-task-edit-overlay backup-management" onMouseDown={event => { if (event.target === event.currentTarget && !busy) onClose() }}>
    <div className="backup-task-edit-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="edit-backup-storage-title">
      <BackupSystemSettings lang={lang} mode="edit" expanded locked={locked} onClose={onClose} onReload={onReload} onBusyChange={onBusyChange}
        task={{ jobs, selectedId: job.id, revision: 0, loading, error, initialJob: job, onSelect: () => {}, onApplied: onSaved }} />
    </div>
  </div>, document.querySelector('.app-shell') ?? document.body)
}
