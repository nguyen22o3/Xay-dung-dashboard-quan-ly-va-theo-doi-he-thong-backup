// Hide only this obsolete rclone advisory in the Drive viewer. Never alter
// stored logs, machine-readable history, or other errors (including auth/I/O).
const advisory = /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)? ERROR\s*:\s*Using --filter is recommended instead of both --include and --exclude as the order they are( parsed(?: in)? is indeterminate)?\s*$/
const advisoryContinuation = /^parsed(?: in)? is indeterminate$/

export function cronLogForDisplay(jobId: string, content: string): { content: string; hiddenAdvisories: number } {
  // New rclone JSON stats are stored unchanged. Show their human message in
  // the existing raw-text viewer, including real error messages and timestamps.
  content = content.split('\n').map(line => {
    if (!line.startsWith('{')) return line
    try {const record: unknown = JSON.parse(line)
      if (record && typeof record === 'object' && 'msg' in record && 'time' in record && 'level' in record && typeof record.msg === 'string' && typeof record.time === 'string' && typeof record.level === 'string') return `[${record.time}] ${record.level.toUpperCase()}: ${record.msg.trimEnd()}`
    } catch { /* Retain non-JSON and partial lines exactly. */ }
    return line
  }).join('\n')
  if (jobId !== 'drive-sync') return { content, hiddenAdvisories: 0 }
  const lines = content.split('\n'), kept: string[] = []
  let hiddenAdvisories = 0
  for (let i = 0; i < lines.length; i++) {
    const match = advisory.exec(lines[i])
    if (match && (match[1] || advisoryContinuation.test(lines[i + 1]?.trim() ?? ''))) {
      hiddenAdvisories++
      if (!match[1]) i++
    } else kept.push(lines[i])
  }
  return { content: kept.join('\n'), hiddenAdvisories }
}

export function cronLogLineLevel(line: string): 'info' | 'error' | 'warning' {
  if (/\b(?:ERROR|CRITICAL|FATAL|FAILED|invalid_grant)\b|MONITOR_RUN\|[^|]+\|[^|]+\|[^|]+\|failed\||Không thể|Không đọc được/i.test(line)) return 'error'
  if (/\bWARN(?:ING)?\b|DRY_RUN|Would delete:/i.test(line)) return 'warning'
  return 'info'
}
export function filterCronLog(content: string, query: string, level: 'all' | 'error' | 'warning') {
  const needle = query.toLocaleLowerCase().trim()
  return content.split('\n').filter(line => (!needle || line.toLocaleLowerCase().includes(needle)) && (level === 'all' || cronLogLineLevel(line) === level))
}
