export function formatSize(mb) {
  if (mb == null || isNaN(mb)) return '--'
  if (mb >= 1024) return (mb / 1024).toFixed(1) + ' GB'
  if (mb >= 1) return mb.toFixed(1) + ' MB'
  if (mb > 0) return (mb * 1024).toFixed(1) + ' KB'
  return '0.0 MB'
}

export function formatTime(dateStr) {
  if (!dateStr) return '--'
  const d = new Date(dateStr)
  return d.toLocaleString('vi-VN', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
