import { useState, useEffect, useCallback, useMemo } from 'react'
import './App.css'

function formatSize(mb) {
  if (mb >= 1024) return (mb / 1024).toFixed(1) + ' GB'
  return mb.toFixed(1) + ' MB'
}

function formatTime(dateStr) {
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

function getInitialDarkMode() {
  const saved = localStorage.getItem('darkMode')
  if (saved !== null) return saved === 'true'
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function App() {
  const [token, setToken] = useState(() => sessionStorage.getItem('token') || null)
  const [loginError, setLoginError] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [backups, setBackups] = useState([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [search, setSearch] = useState('')
  const [filterStatus, setFilterStatus] = useState('all')
  const [filterSource, setFilterSource] = useState('all')
  const [filterDestination, setFilterDestination] = useState('all')
  const [pageNum, setPageNum] = useState(1)
  const [perPage, setPerPage] = useState(10)
  const [darkMode, setDarkMode] = useState(getInitialDarkMode)
  const [backendOnline, setBackendOnline] = useState(null)
  const [page, setPage] = useState('dashboard')
  const [toast, setToast] = useState(null)
  const [config, setConfig] = useState({
    discord_webhook_url: '',
    discord_enabled: false,
    gmail_smtp_host: 'smtp.gmail.com',
    gmail_smtp_port: '587',
    gmail_email: '',
    gmail_app_password: '',
    gmail_to: '',
    gmail_enabled: false,
    telegram_bot_token: '',
    telegram_chat_id: '',
    telegram_enabled: false,
  })
  const [schedules, setSchedules] = useState([])
  const [scheduleForm, setScheduleForm] = useState({
    id: null,
    source: '',
    source_key: '',
    cron_expr: '0 2 * * *',
    enabled: true,
    grace_minutes: 30,
  })
  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createForm, setCreateForm] = useState({
    source_path: '',
    destinations: ['server'],
    dest_path: '',
    source_name: '',
    empty_folder: '',
  })
  const [uploadFiles, setUploadFiles] = useState([])
  const [emptyPick, setEmptyPick] = useState(false)
  const [sourceModalOpen, setSourceModalOpen] = useState(false)
  const [folderOpen, setFolderOpen] = useState(false)
  const [currentFolderPath, setCurrentFolderPath] = useState('/')
  const [currentFolders, setCurrentFolders] = useState([])
  const [folderLoading, setFolderLoading] = useState(false)

  const ah = useCallback(() => {
    const h = { 'Content-Type': 'application/json' }
    if (token) h['Authorization'] = 'Bearer ' + token
    return h
  }, [token])

  const handleLogin = async (e) => {
    e.preventDefault()
    setLoginError('')
    try {
      const res = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      const data = await res.json()
      if (!res.ok || data.status !== 'success') {
        setLoginError(data.message || 'Đăng nhập thất bại')
        return
      }
      sessionStorage.setItem('token', data.token)
      setToken(data.token)
    } catch {
      setLoginError('Không kết nối được tới backend')
    }
  }

  const handleLogout = () => {
    sessionStorage.removeItem('token')
    setToken(null)
  }

  useEffect(() => {
    document.documentElement.classList.toggle('dark', darkMode)
    localStorage.setItem('darkMode', darkMode)
  }, [darkMode])

  useEffect(() => {
    let cancelled = false

    const checkHealth = async () => {
      try {
        const res = await fetch('/api/health')
        if (!cancelled) setBackendOnline(res.ok)
      } catch {
        if (!cancelled) setBackendOnline(false)
      }
    }

    checkHealth()
    const id = setInterval(checkHealth, 5000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [])

  useEffect(() => {
    if (!token) return
    const loadConfig = async () => {
      try {
        const res = await fetch('/api/config', { headers: ah() })
        if (!res.ok) return
        const data = await res.json()
        setConfig(prev => ({ ...prev, ...data }))
      } catch (e) {
        console.error('Lỗi khi tải cấu hình:', e)
      }
    }
    loadConfig()
  }, [ah, token])

  const toggleDarkMode = () => setDarkMode(prev => !prev)

  const setConfigField = (field, value) => {
    setConfig(prev => ({ ...prev, [field]: value }))
  }

  const showToast = (message, type = 'success') => {
    setToast({ message, type })
    setTimeout(() => setToast(null), 3000)
  }

  const saveConfig = async (evt) => {
    evt.preventDefault()
    try {
      const res = await fetch('/api/config', {
        method: 'POST',
        headers: ah(),
        body: JSON.stringify(config),
      })
      if (res.ok) {
        showToast('Đã lưu cấu hình thông báo thành công')
      } else {
        showToast('Lưu cấu hình thất bại', 'error')
      }
    } catch (e) {
      console.error('Lỗi khi lưu cấu hình:', e)
      showToast('Lưu cấu hình thất bại', 'error')
    }
  }

  const testNotify = async () => {
    try {
      const res = await fetch('/api/test-notify', {
        method: 'POST',
        headers: ah(),
        body: JSON.stringify(config),
      })
      if (res.ok) {
        showToast('Đã gửi thông báo thử nghiệm. Kiểm tra các kênh đã bật!')
      } else {
        showToast('Gửi thông báo thử thất bại', 'error')
      }
    } catch {
      showToast('Gửi thông báo thử thất bại', 'error')
    }
  }

  const loadSchedules = useCallback(async () => {
    if (!token) return
    try {
      const res = await fetch('/api/schedules', { headers: ah() })
      if (!res.ok) {
        if (res.status === 401) handleLogout()
        return
      }
      const data = await res.json()
      setSchedules(data || [])
    } catch (e) {
      console.error('Lỗi khi tải lịch backup:', e)
    }
  }, [ah, token])

  const handleScheduleForm = (field, value) => {
    setScheduleForm(prev => ({ ...prev, [field]: value }))
  }

  const saveSchedule = async (e) => {
    e.preventDefault()
    try {
      const method = scheduleForm.id ? 'PUT' : 'POST'
      const payload = { ...scheduleForm }
      if (!payload.id) delete payload.id
      const res = await fetch('/api/schedules', {
        method,
        headers: ah(),
        body: JSON.stringify(payload),
      })
      if (!res.ok) {
        const errText = await res.text()
        showToast('Lưu lịch backup thất bại' + (errText ? `: ${errText}` : ''), 'error')
        return
      }
      showToast(scheduleForm.id ? 'Đã cập nhật lịch backup' : 'Đã thêm lịch backup')
      setScheduleForm({
        id: null,
        source: '',
        source_key: '',
        cron_expr: '0 2 * * *',
        enabled: true,
        grace_minutes: 30,
      })
      await loadSchedules()
    } catch {
      showToast('Lưu lịch backup thất bại', 'error')
    }
  }

  const editSchedule = (s) => {
    setScheduleForm({
      id: s.id,
      source: s.source,
      source_key: s.source_key,
      cron_expr: s.cron_expr,
      enabled: s.enabled,
      grace_minutes: s.grace_minutes,
    })
    setPage('settings')
  }

  const deleteSchedule = async (id) => {
    if (!window.confirm('Xóa lịch backup này?')) return
    try {
      const res = await fetch(`/api/schedules?id=${id}`, {
        method: 'DELETE',
        headers: ah(),
      })
      if (!res.ok) {
        showToast('Xóa lịch backup thất bại', 'error')
        return
      }
      showToast('Đã xóa lịch backup')
      await loadSchedules()
    } catch {
      showToast('Xóa lịch backup thất bại', 'error')
    }
  }

  useEffect(() => {
    if (!token) return
    let cancelled = false
    fetch('/api/schedules', { headers: ah() })
      .then(res => {
        if (res.status === 401) {
          handleLogout()
          throw new Error('Unauthorized')
        }
        if (!res.ok) throw new Error('Tải lịch backup thất bại')
        return res.json()
      })
      .then(data => {
        if (!cancelled) setSchedules(data || [])
      })
      .catch(err => {
        if (!cancelled) console.error('Lỗi khi tải lịch backup:', err)
      })
    return () => { cancelled = true }
  }, [ah, token])

  const fetchBackups = useCallback(async () => {
    if (!token) return
    try {
      const res = await fetch('/api/backups', { headers: ah() })
      if (!res.ok) {
        if (res.status === 401) handleLogout()
        return
      }
      const data = await res.json()
      setBackups(data || [])
    } catch (e) {
      console.error('Lỗi khi tải dữ liệu:', e)
    }
  }, [ah, token])

  const handleRefresh = useCallback(async () => {
    setRefreshing(true)
    await fetchBackups()
    setRefreshing(false)
  }, [fetchBackups])

  useEffect(() => {
    if (!autoRefresh) return
    const id = setInterval(() => {
      fetchBackups()
    }, 10000)
    return () => clearInterval(id)
  }, [autoRefresh, fetchBackups])

  useEffect(() => {
    if (!token) return
    let cancelled = false
    fetch('/api/backups', { headers: ah() })
      .then(res => {
        if (res.status === 401) {
          handleLogout()
          throw new Error('Unauthorized')
        }
        if (!res.ok) throw new Error('Tải dữ liệu thất bại')
        return res.json()
      })
      .then(data => {
        if (!cancelled) {
          setBackups(data || [])
          setLoading(false)
        }
      })
      .catch(err => {
        if (!cancelled) {
          console.error('Lỗi khi tải dữ liệu:', err)
          setLoading(false)
        }
      })
    return () => { cancelled = true }
  }, [ah, token])

  const resetIds = async () => {
    try {
      await fetch('/api/reset-ids', {
        method: 'POST',
        headers: ah(),
      })
    } catch (e) {
      console.error('Lỗi khi reset ID:', e)
    }
  }

  const handleDelete = async (id, destination) => {
    const where = destination ? ` tại ${destination}` : ''
    if (!window.confirm(`Bạn có chắc chắn muốn xóa bản sao lưu này${where}? File sẽ bị xóa khỏi nơi cất giữ tương ứng.`)) return
    try {
      const res = await fetch(`/api/delete?id=${id}`, {
        method: 'DELETE',
        headers: ah(),
      })
      if (!res.ok) {
        const errText = await res.text()
        showToast('Xóa bản sao lưu thất bại' + (errText ? `: ${errText}` : ''), 'error')
        return
      }
      await resetIds()
      await fetchBackups()
      showToast('Đã xóa bản sao lưu')
    } catch (e) {
      console.error('Lỗi khi xóa:', e)
      showToast('Xóa bản sao lưu thất bại', 'error')
    }
  }

  const handleCreateField = (field, value) => {
    setCreateForm(prev => ({ ...prev, [field]: value }))
  }

  const createBackup = async (e) => {
    e.preventDefault()
    const hasUploadFiles = uploadFiles && uploadFiles.length > 0
    const hasEmptyFolder = createForm.empty_folder && createForm.empty_folder.trim()
    const hasSourcePath = createForm.source_path && createForm.source_path.trim()
    if (!hasUploadFiles && !hasEmptyFolder && !hasSourcePath) {
      showToast('Vui lòng chọn nguồn dữ liệu (chọn bên nút + ở ô "Nguồn dữ liệu")', 'error')
      return
    }
    if (!createForm.destinations || createForm.destinations.length === 0) {
      showToast('Vui lòng chọn ít nhất một nơi cất giữ', 'error')
      return
    }
    setCreating(true)
    try {
      let res
      if (hasUploadFiles || hasEmptyFolder) {
        const fd = new FormData()
        for (const f of uploadFiles) fd.append('files', f)
        fd.append('destinations', JSON.stringify(createForm.destinations))
        fd.append('dest_path', createForm.dest_path)
        fd.append('source_name', createForm.source_name)
        fd.append('empty_folder', (createForm.empty_folder || '').trim())
        fd.append('source_path', (createForm.source_path || '').trim())
        res = await fetch('/api/upload', {
          method: 'POST',
          headers: { Authorization: 'Bearer ' + token },
          body: fd,
        })
      } else {
        res = await fetch('/api/backup', {
          method: 'POST',
          headers: ah(),
          body: JSON.stringify(createForm),
        })
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        showToast('Tạo bản backup thất bại' + (data.message ? `: ${data.message}` : ''), 'error')
        return
      }
      showToast(data.message || 'Đã tạo bản backup thành công')
      setCreateOpen(false)
      setCreateForm({ source_path: '', destinations: ['server'], dest_path: '', source_name: '', empty_folder: '' })
      setUploadFiles([])
      setEmptyPick(false)
      await fetchBackups()
    } catch (err) {
      console.error('Lỗi khi tạo backup:', err)
      showToast('Tạo bản backup thất bại', 'error')
    } finally {
      setCreating(false)
    }
  }

  const toggleDestination = (value) => {
    setCreateForm(prev => {
      const cur = prev.destinations || []
      const has = cur.includes(value)
      const next = has ? cur.filter(d => d !== value) : [...cur, value]
      return { ...prev, destinations: next }
    })
  }

  const handleFileChange = (e) => {
    const files = Array.from(e.target.files || [])
    setUploadFiles(files)
    if (files.length === 0) {
      setEmptyPick(true)
    }
  }

  const closeCreateModal = () => {
    setCreateOpen(false)
    setEmptyPick(false)
    setSourceModalOpen(false)
    setFolderOpen(false)
  }

  const hasUpload = uploadFiles && uploadFiles.length > 0
  const hasEmptyFolder = !!createForm.empty_folder && createForm.empty_folder.trim() !== ''
  const hasSourcePath = !!createForm.source_path && createForm.source_path.trim() !== ''

  const sourceTags = []
  if (hasUpload) sourceTags.push({ key: 'upload', label: 'Từ máy', cls: 'dest-tag--server' })
  if (hasEmptyFolder) sourceTags.push({ key: 'empty', label: `Thư mục rỗng: ${createForm.empty_folder.trim()}`, cls: 'dest-tag--nas' })
  if (hasSourcePath) sourceTags.push({ key: 'path', label: `Đường dẫn: ${createForm.source_path.trim()}`, cls: 'dest-tag--google-drive' })

  const loadFolder = async (path) => {
    setFolderLoading(true)
    try {
      const res = await fetch(`/api/folders?path=${encodeURIComponent(path)}`, { headers: ah() })
      if (!res.ok) {
        showToast('Không đọc được thư mục', 'error')
        return
      }
      const data = await res.json()
      setCurrentFolderPath(data.path)
      setCurrentFolders(data.folders || [])
    } catch {
      showToast('Không đọc được thư mục', 'error')
    } finally {
      setFolderLoading(false)
    }
  }

  const openFolderPicker = async (initialPath) => {
    setFolderOpen(true)
    await loadFolder(initialPath || '/')
  }

  const sources = useMemo(() => {
    const set = new Set(backups.map(b => b.source))
    return ['all', ...Array.from(set)]
  }, [backups])

  const destinations = useMemo(() => {
    const set = new Set(backups.map(b => b.destination).filter(Boolean))
    return ['all', ...Array.from(set)]
  }, [backups])

  const filtered = useMemo(() => {
    return backups.filter(item => {
      if (filterStatus !== 'all' && item.status !== filterStatus) return false
      if (filterSource !== 'all' && item.source !== filterSource) return false
      if (filterDestination !== 'all' && item.destination !== filterDestination) return false
      if (search) {
        const q = search.toLowerCase()
        return (
          item.file_name.toLowerCase().includes(q) ||
          item.source.toLowerCase().includes(q) ||
          (item.destination || '').toLowerCase().includes(q)
        )
      }
      return true
    })
  }, [backups, search, filterStatus, filterSource, filterDestination])

  const totalPages = Math.max(1, Math.ceil(filtered.length / perPage))
  const safePage = Math.min(pageNum, totalPages)
  const paginated = filtered.slice((safePage - 1) * perPage, safePage * perPage)

  const stats = useMemo(() => {
    const total = backups.length
    const success = backups.filter(b => b.status === 'Success').length
    const failed = total - success
    const totalSize = backups.reduce((sum, b) => sum + b.size_mb, 0)
    const lastBackup = backups.length > 0 ? backups[0].created_at : null
    const successRate = total > 0 ? Math.round((success / total) * 100) : 0
    return { total, success, failed, totalSize, lastBackup, successRate }
  }, [backups])

  const handleClearAll = async () => {
    if (!window.confirm('Bạn có chắc chắn muốn xóa TOÀN BỘ dữ liệu backup? Hành động này không thể hoàn tác!')) return
    try {
      const res = await fetch('/api/clear-all', {
        method: 'POST',
        headers: ah(),
      })
      if (res.ok) {
        await fetchBackups()
        showToast('Đã xóa toàn bộ dữ liệu thành công')
      }
    } catch (e) {
      console.error('Lỗi khi xóa dữ liệu:', e)
      showToast('Xóa dữ liệu thất bại', 'error')
    }
  }

  const pageTitles = {
    dashboard: { title: 'Tổng quan giám sát', desc: 'Theo dõi và quản lý tất cả hoạt động sao lưu' },
    backups: { title: 'Bản sao lưu', desc: 'Danh sách và lịch sử sao lưu' },
    settings: { title: 'Cài đặt', desc: 'Cấu hình hệ thống và tùy chọn' },
  }

  const current = pageTitles[page] || pageTitles.dashboard

  if (!token) {
    return (
      <div className="login-page">
        <form className="login-card" onSubmit={handleLogin}>
          <h1>Backup Dashboard</h1>
          <p className="login-sub">Đăng nhập để tiếp tục</p>
          <input
            type="text"
            placeholder="Tên đăng nhập"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
          />
          <input
            type="password"
            placeholder="Mật khẩu"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {loginError && <div className="login-error">{loginError}</div>}
          <button type="submit" className="btn btn-primary login-btn">Đăng nhập</button>
        </form>
      </div>
    )
  }

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <div className="brand-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2L2 7l10 5 10-5-10-5z" />
              <path d="M2 17l10 5 10-5" />
              <path d="M2 12l10 5 10-5" />
            </svg>
          </div>
          <span className="brand-name">BackupOps</span>
        </div>

        <nav className="sidebar-nav">
          <button className={`nav-item ${page === 'dashboard' ? 'active' : ''}`} onClick={() => setPage('dashboard')}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="7" height="7" rx="1" />
              <rect x="14" y="3" width="7" height="7" rx="1" />
              <rect x="3" y="14" width="7" height="7" rx="1" />
              <rect x="14" y="14" width="7" height="7" rx="1" />
            </svg>
            <span>Tổng quan</span>
          </button>
          <button className={`nav-item ${page === 'backups' ? 'active' : ''}`} onClick={() => setPage('backups')}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="17 8 12 3 7 8" />
              <line x1="12" y1="3" x2="12" y2="15" />
            </svg>
            <span>Bản sao lưu</span>
          </button>
          <button className={`nav-item ${page === 'settings' ? 'active' : ''}`} onClick={() => setPage('settings')}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
            </svg>
            <span>Cài đặt</span>
          </button>
        </nav>

        <div className="sidebar-footer">
          <button className="theme-toggle" onClick={toggleDarkMode} title={darkMode ? 'Chế độ sáng' : 'Chế độ tối'}>
            {darkMode ? (
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="5" />
                <line x1="12" y1="1" x2="12" y2="3" />
                <line x1="12" y1="21" x2="12" y2="23" />
                <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
                <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
                <line x1="1" y1="12" x2="3" y2="12" />
                <line x1="21" y1="12" x2="23" y2="12" />
                <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
                <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
              </svg>
            ) : (
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
              </svg>
            )}
            <span>{darkMode ? 'Chế độ sáng' : 'Chế độ tối'}</span>
          </button>
          <button className="theme-toggle" onClick={handleLogout} title="Đăng xuất">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <polyline points="16 17 21 12 16 7" />
              <line x1="21" y1="12" x2="9" y2="12" />
            </svg>
            <span>Đăng xuất</span>
          </button>
          <div className={`system-status ${backendOnline === false ? 'is-offline' : ''}`}>
            <span className="status-dot" />
            <span className="status-label">
              {backendOnline === null
                ? 'Đang kiểm tra...'
                : backendOnline
                  ? 'Hệ thống hoạt động'
                  : 'Máy chủ ngoại tuyến'}
            </span>
          </div>
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div className="topbar-left">
            <h1 className="page-title">{current.title}</h1>
            <p className="page-desc">{current.desc}</p>
          </div>
          <div className="topbar-right">
            {page !== 'settings' && (
              <>
                <div className="topbar-time">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <circle cx="12" cy="12" r="10" />
                    <polyline points="12 6 12 12 16 14" />
                  </svg>
                  <span>Cập nhật: {stats.lastBackup ? formatTime(stats.lastBackup) : 'Chưa có'}</span>
                </div>
                <button className="btn-create" onClick={() => setCreateOpen(true)} title="Tạo bản backup mới">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="12" y1="5" x2="12" y2="19" />
                    <line x1="5" y1="12" x2="19" y2="12" />
                  </svg>
                  <span>Tạo bản backup</span>
                </button>
                <button className={`btn-auto ${autoRefresh ? 'active' : ''}`} onClick={() => setAutoRefresh(prev => !prev)} title="Tự động làm mới mỗi 10 giây">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M21 12a9 9 0 1 1-2.636-6.364" />
                    <polyline points="21 3 21 9 15 9" />
                  </svg>
                  <span>{autoRefresh ? 'Tự động' : 'Thủ công'}</span>
                </button>
                <button className="btn-refresh" onClick={handleRefresh} disabled={refreshing} title="Làm mới dữ liệu">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={refreshing ? 'spin' : ''}>
                    <polyline points="23 4 23 10 17 10" />
                    <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
                  </svg>
                  <span>{refreshing ? 'Đang tải...' : 'Làm mới'}</span>
                </button>
              </>
            )}
          </div>
        </header>

        {page === 'settings' ? (
          <div className="settings-page">
            <section className="settings-section">
              <div className="settings-header">
                <h2>Cấu hình thông báo</h2>
                <p>Chọn nền tảng nhận thông báo khi backup thành công hoặc thất bại</p>
              </div>
              <form className="settings-card" onSubmit={saveConfig}>
                {/* Discord */}
                <div className="settings-platform">
                  <div className="settings-row settings-row--between">
                    <div className="settings-brand">
                      <div className="settings-brand-icon settings-brand-icon--discord">
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                          <path d="M20.317 4.37a19.79 19.79 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028c.462-.63.874-1.295 1.226-1.994a.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z" />
                        </svg>
                      </div>
                      <div>
                        <div className="settings-label">Discord</div>
                        <p className="settings-hint" style={{ margin: '2px 0 0' }}>Gửi qua webhook kênh Discord</p>
                      </div>
                    </div>
                    <button type="button" className={`switch ${config.discord_enabled ? 'switch--on' : ''}`} role="switch" aria-checked={config.discord_enabled} onClick={() => setConfigField('discord_enabled', !config.discord_enabled)}>
                      <span className="switch-thumb" />
                    </button>
                  </div>
                  {config.discord_enabled && (
                    <div className="settings-row">
                      <label className="settings-label">Webhook URL</label>
                      <input className="settings-input" type="password" placeholder={config.discord_webhook_url === '••••••••' ? '•••••••• (đã lưu, nhập để thay đổi)' : 'https://discord.com/api/webhooks/...'} value={config.discord_webhook_url === '••••••••' ? '' : config.discord_webhook_url} onChange={e => setConfigField('discord_webhook_url', e.target.value)} />
                      <p className="settings-hint">Webhook Discord để nhận thông báo. Giá trị lưu được che đi cho an toàn</p>
                    </div>
                  )}
                </div>

                <div className="settings-divider" />

                {/* Gmail */}
                <div className="settings-platform">
                  <div className="settings-row settings-row--between">
                    <div className="settings-brand">
                      <div className="settings-brand-icon settings-brand-icon--gmail">
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                          <path d="M20 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z" />
                        </svg>
                      </div>
                      <div>
                        <div className="settings-label">Gmail</div>
                        <p className="settings-hint" style={{ margin: '2px 0 0' }}>Gửi qua SMTP Gmail</p>
                      </div>
                    </div>
                    <button type="button" className={`switch ${config.gmail_enabled ? 'switch--on' : ''}`} role="switch" aria-checked={config.gmail_enabled} onClick={() => setConfigField('gmail_enabled', !config.gmail_enabled)}>
                      <span className="switch-thumb" />
                    </button>
                  </div>
                  {config.gmail_enabled && (
                    <>
                      <div className="settings-row">
                        <label className="settings-label">Email gửi (Gmail)</label>
                        <input className="settings-input" type="text" placeholder="you@gmail.com" value={config.gmail_email} onChange={e => setConfigField('gmail_email', e.target.value)} />
                      </div>
                      <div className="settings-row">
                        <label className="settings-label">Mật khẩu ứng dụng (App Password)</label>
                        <input className="settings-input" type="password" placeholder={config.gmail_app_password === '••••••••' ? '•••••••• (đã lưu, nhập để thay đổi)' : '16 ký tự app password'} value={config.gmail_app_password === '••••••••' ? '' : config.gmail_app_password} onChange={e => setConfigField('gmail_app_password', e.target.value)} />
                        <p className="settings-hint">Bật 2FA và tạo App Password trong tài khoản Google</p>
                      </div>
                      <div className="settings-row">
                        <label className="settings-label">Email nhận thông báo</label>
                        <input className="settings-input" type="text" placeholder="recipient@example.com" value={config.gmail_to} onChange={e => setConfigField('gmail_to', e.target.value)} />
                      </div>
                      <div className="settings-row settings-row--split">
                        <div className="settings-row">
                          <label className="settings-label">SMTP Host</label>
                          <input className="settings-input" type="text" value={config.gmail_smtp_host} onChange={e => setConfigField('gmail_smtp_host', e.target.value)} />
                        </div>
                        <div className="settings-row">
                          <label className="settings-label">SMTP Port</label>
                          <input className="settings-input" type="text" value={config.gmail_smtp_port} onChange={e => setConfigField('gmail_smtp_port', e.target.value)} />
                        </div>
                      </div>
                    </>
                  )}
                </div>

                <div className="settings-divider" />

                {/* Telegram */}
                <div className="settings-platform">
                  <div className="settings-row settings-row--between">
                    <div className="settings-brand">
                      <div className="settings-brand-icon settings-brand-icon--telegram">
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                          <path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z" />
                        </svg>
                      </div>
                      <div>
                        <div className="settings-label">Telegram</div>
                        <p className="settings-hint" style={{ margin: '2px 0 0' }}>Gửi qua Telegram Bot</p>
                      </div>
                    </div>
                    <button type="button" className={`switch ${config.telegram_enabled ? 'switch--on' : ''}`} role="switch" aria-checked={config.telegram_enabled} onClick={() => setConfigField('telegram_enabled', !config.telegram_enabled)}>
                      <span className="switch-thumb" />
                    </button>
                  </div>
                  {config.telegram_enabled && (
                    <>
                      <div className="settings-row">
                        <label className="settings-label">Bot Token</label>
                        <input className="settings-input" type="password" placeholder={config.telegram_bot_token === '••••••••' ? '•••••••• (đã lưu, nhập để thay đổi)' : '123456:ABC-DEF... từ @BotFather'} value={config.telegram_bot_token === '••••••••' ? '' : config.telegram_bot_token} onChange={e => setConfigField('telegram_bot_token', e.target.value)} />
                      </div>
                      <div className="settings-row">
                        <label className="settings-label">Chat / Channel ID</label>
                        <input className="settings-input" type="text" placeholder="Ví dụ: -1001234567890 hoặc 123456789" value={config.telegram_chat_id} onChange={e => setConfigField('telegram_chat_id', e.target.value)} />
                      </div>
                    </>
                  )}
                </div>

                <div className="settings-divider" />

                <div className="settings-actions">
                  <button className="btn-primary" type="submit">Lưu cấu hình</button>
                  <button className="btn-secondary" type="button" onClick={testNotify}>Gửi thông báo thử</button>
                </div>
              </form>
            </section>

            <section className="settings-section">
              <div className="settings-header">
                <h2>Cài đặt giao diện</h2>
                <p>Tùy chỉnh hiển thị dashboard</p>
              </div>
              <div className="settings-card">
                <div className="settings-row settings-row--between">
                  <div>
                    <div className="settings-label">Chế độ tối</div>
                    <p className="settings-hint" style={{ margin: '4px 0 0' }}>Sử dụng giao diện tối để giảm mỏi mắt</p>
                  </div>
                  <button className={`switch ${darkMode ? 'switch--on' : ''}`} onClick={toggleDarkMode} role="switch" aria-checked={darkMode}>
                    <span className="switch-thumb" />
                  </button>
                </div>
              </div>
            </section>

            <section className="settings-section">
              <div className="settings-header">
                <h2>Lịch backup kỳ vọng</h2>
                <p>Cấu hình lịch để cảnh báo khi backup quá hạn (không thấy file mới)</p>
              </div>
              <div className="settings-card">
                <form className="schedule-form" onSubmit={saveSchedule}>
                  <div className="settings-row settings-row--split">
                    <div className="settings-row">
                      <label className="settings-label">Nguồn (tên hiển thị)</label>
                      <input className="settings-input" type="text" placeholder="Ví dụ: Server Web LAMP" value={scheduleForm.source} onChange={e => handleScheduleForm('source', e.target.value)} required />
                    </div>
                    <div className="settings-row">
                      <label className="settings-label">Prefix tên file (source_key)</label>
                      <input className="settings-input" type="text" placeholder="Ví dụ: web_code_" value={scheduleForm.source_key} onChange={e => handleScheduleForm('source_key', e.target.value)} required />
                    </div>
                  </div>
                  <div className="settings-row settings-row--split">
                    <div className="settings-row">
                      <label className="settings-label">Biểu thức Cron</label>
                      <input className="settings-input" type="text" placeholder="0 2 * * *" value={scheduleForm.cron_expr} onChange={e => handleScheduleForm('cron_expr', e.target.value)} required />
                      <p className="settings-hint">Định dạng 5 trường: phút giờ ngày tháng tuần. VD: <code>0 2 * * *</code> = 2h sáng hằng ngày</p>
                    </div>
                    <div className="settings-row">
                      <label className="settings-label">Ân hạn (phút)</label>
                      <input className="settings-input" type="number" min="0" value={scheduleForm.grace_minutes} onChange={e => handleScheduleForm('grace_minutes', Number(e.target.value))} />
                    </div>
                  </div>
                  <div className="settings-row settings-row--between">
                    <div>
                      <div className="settings-label">Bật theo dõi</div>
                      <p className="settings-hint" style={{ margin: '4px 0 0' }}>Bật để backend cảnh báo khi quá hạn</p>
                    </div>
                    <button type="button" className={`switch ${scheduleForm.enabled ? 'switch--on' : ''}`} role="switch" aria-checked={scheduleForm.enabled} onClick={() => handleScheduleForm('enabled', !scheduleForm.enabled)}>
                      <span className="switch-thumb" />
                    </button>
                  </div>
                  <div className="settings-actions">
                    <button className="btn-primary" type="submit">{scheduleForm.id ? 'Cập nhật lịch' : 'Thêm lịch'}</button>
                    {scheduleForm.id && (
                      <button className="btn-secondary" type="button" onClick={() => setScheduleForm({ id: null, source: '', source_key: '', cron_expr: '0 2 * * *', enabled: true, grace_minutes: 30 })}>Hủy</button>
                    )}
                  </div>
                </form>

                {schedules.length > 0 ? (
                  <div className="schedule-list">
                    <table className="schedule-table">
                      <thead>
                        <tr>
                          <th>Nguồn</th>
                          <th>Prefix</th>
                          <th>Cron</th>
                          <th>Ân hạn</th>
                          <th>Trạng thái</th>
                          <th></th>
                        </tr>
                      </thead>
                      <tbody>
                        {schedules.map(s => (
                          <tr key={s.id}>
                            <td>{s.source}</td>
                            <td><code>{s.source_key}</code></td>
                            <td><code>{s.cron_expr}</code></td>
                            <td>{s.grace_minutes} phút</td>
                            <td>
                              <span className={`tag ${s.enabled ? 'tag--success' : ''}`}>
                                {s.enabled ? 'Bật' : 'Tắt'}
                              </span>
                            </td>
                            <td>
                              <div className="table-actions">
                                <button className="btn-secondary btn-sm" type="button" onClick={() => editSchedule(s)}>Sửa</button>
                                <button className="btn-danger btn-sm" type="button" onClick={() => deleteSchedule(s.id)}>Xóa</button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <p className="settings-hint" style={{ padding: '8px 2px 0' }}>Chưa có lịch backup nào. Thêm lịch để bắt đầu theo dõi.</p>
                )}
              </div>
            </section>

            <section className="settings-section">
              <div className="settings-header">
                <h2>Thông tin hệ thống</h2>
                <p>Trạng thái hoạt động của hệ thống</p>
              </div>
              <div className="settings-card">
                <div className="settings-row settings-row--between">
                  <div>
                    <div className="settings-label">Trạng thái backend</div>
                    <p className="settings-hint" style={{ margin: '4px 0 0' }}>Kết nối máy chủ</p>
                  </div>
                  <span className={`tag ${backendOnline === false ? 'tag--danger' : 'tag--success'}`}>
                    {backendOnline === null
                      ? 'Đang kiểm tra...'
                      : backendOnline
                        ? 'Hoạt động'
                        : 'Ngoại tuyến'}
                  </span>
                </div>
                <div className="settings-divider" />
                <div className="settings-row settings-row--between">
                  <div>
                    <div className="settings-label">Số bản sao lưu</div>
                    <p className="settings-hint" style={{ margin: '4px 0 0' }}>Tổng số bản ghi trong hệ thống</p>
                  </div>
                  <span className="cell-mono">{stats.total}</span>
                </div>
                <div className="settings-divider" />
                <div className="settings-row settings-row--between">
                  <div>
                    <div className="settings-label">Tổng dung lượng</div>
                    <p className="settings-hint" style={{ margin: '4px 0 0' }}>Dung lượng đã sao lưu</p>
                  </div>
                  <span className="cell-mono">{formatSize(stats.totalSize)}</span>
                </div>
                <div className="settings-divider" />
                <div className="settings-row settings-row--between">
                  <div>
                    <div className="settings-label">Tỷ lệ thành công</div>
                    <p className="settings-hint" style={{ margin: '4px 0 0' }}>Phần trăm backup thành công</p>
                  </div>
                  <span className="cell-mono">{stats.successRate}%</span>
                </div>
              </div>
            </section>

            <section className="settings-section">
              <div className="settings-header">
                <h2>Xóa dữ liệu</h2>
                <p>Quản lý toàn bộ dữ liệu backup</p>
              </div>
              <div className="settings-card settings-card--danger">
                <div className="settings-danger-content">
                  <div>
                    <div className="settings-label">Xóa toàn bộ bản sao lưu</div>
                    <p className="settings-hint" style={{ margin: '4px 0 0' }}>Xóa tất cả bản ghi và reset ID về 1. Không thể hoàn tác.</p>
                  </div>
                  <button className="btn-danger" onClick={handleClearAll}>
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="3 6 5 6 21 6" />
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                    </svg>
                    Xóa tất cả
                  </button>
                </div>
              </div>
            </section>
          </div>
        ) : (
          <>
            {loading ? (
              <div className="loading-wrap">
                <div className="spinner" />
                <p>Đang tải dữ liệu...</p>
              </div>
            ) : (
              <>
                {page === 'dashboard' && (
                  <section className="stats-grid">
                    <div className="stat-card">
                      <div className="stat-icon-wrap">        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                        <polyline points="17 8 12 3 7 8" />
                        <line x1="12" y1="3" x2="12" y2="15" />
                      </svg>
                      </div>
                      <div className="stat-body">
                        <span className="stat-number">{stats.total}</span>
                        <span className="stat-name">Tổng bản sao lưu</span>
                      </div>
                    </div>

                    <div className="stat-card">
                      <div className="stat-icon-wrap stat-icon--success">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
                          <polyline points="22 4 12 14.01 9 11.01" />
                        </svg>
                      </div>
                      <div className="stat-body">
                        <span className="stat-number">{stats.successRate}%</span>
                        <span className="stat-name">Tỷ lệ thành công</span>
                      </div>
                    </div>

                    <div className="stat-card">
                      <div className="stat-icon-wrap stat-icon--warning">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
                          <line x1="12" y1="9" x2="12" y2="13" />
                          <line x1="12" y1="17" x2="12.01" y2="17" />
                        </svg>
                      </div>
                      <div className="stat-body">
                        <span className="stat-number">{stats.failed}</span>
                        <span className="stat-name">Thất bại</span>
                      </div>
                    </div>

                    <div className="stat-card">
                      <div className="stat-icon-wrap stat-icon--accent">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M4 7V4a2 2 0 0 1 2-2h8.5L20 7.5V20a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-3" />
                          <polyline points="14 2 14 8 20 8" />
                        </svg>
                      </div>
                      <div className="stat-body">
                        <span className="stat-number">{formatSize(stats.totalSize)}</span>
                        <span className="stat-name">Tổng dung lượng</span>
                      </div>
                    </div>
                  </section>
                )}

                <section className="content-card">
                  <div className="card-toolbar">
                    <div className="search-box">
                      <svg className="search-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <circle cx="11" cy="11" r="8" />
                        <line x1="21" y1="21" x2="16.65" y2="16.65" />
                      </svg>
                      <input
                        type="text"
                        className="search-input"
                        placeholder="Tìm theo tên file hoặc nguồn..."
                        value={search}
                        onChange={e => { setSearch(e.target.value); setPageNum(1) }}
                      />
                    </div>
                    <div className="filter-row">
                      <select className="filter-select" value={filterStatus} onChange={e => { setFilterStatus(e.target.value); setPageNum(1) }}>
                        <option value="all">Tất cả trạng thái</option>
                        <option value="Success">Thành công</option>
                        <option value="Failed">Thất bại</option>
                      </select>
                      <select className="filter-select" value={filterSource} onChange={e => { setFilterSource(e.target.value); setPageNum(1) }}>
                        {sources.map(s => (
                          <option key={s} value={s}>{s === 'all' ? 'Tất cả nguồn' : s}</option>
                        ))}
                      </select>
                      <select className="filter-select" value={filterDestination} onChange={e => { setFilterDestination(e.target.value); setPageNum(1) }}>
                        {destinations.map(d => (
                          <option key={d} value={d}>{d === 'all' ? 'Tất cả nơi cất giữ' : d}</option>
                        ))}
                      </select>
                    </div>
                  </div>

                  <div className="table-wrap">
                    <table className="data-table">
                      <thead>
                        <tr>
                          <th style={{ width: 64 }}>ID</th>
                          <th>Nguồn lưu trữ</th>
                          <th>Nơi cất giữ</th>
                          <th>Tên file</th>
                          <th>Dung lượng</th>
                          <th>Trạng thái</th>
                          <th>Thời gian</th>
                          <th style={{ width: 90, textAlign: 'center' }}>Thao tác</th>
                        </tr>
                      </thead>
                      <tbody>
                        {paginated.map(item => (
                          <tr key={item.id}>
                            <td className="cell-id"># {item.id}</td>
                            <td className="cell-source">{item.source}</td>
                            <td>
                              <span className={`dest-tag dest-tag--${(item.destination || '').toLowerCase().replace(/\s+/g, '-')}`}>
                                {item.destination || '--'}
                              </span>
                            </td>
                            <td className="cell-filename" title={item.file_name}>{item.file_name}</td>
                            <td className="cell-mono">{formatSize(item.size_mb)}</td>
                            <td>
                              <span className={`tag tag--${item.status === 'Success' ? 'success' : 'danger'}`}>
                                {item.status === 'Success' ? 'Thành công' : 'Thất bại'}
                              </span>
                            </td>
                            <td className="cell-time">{formatTime(item.created_at)}</td>
                            <td style={{ textAlign: 'center' }}>
                              <button className="btn-icon btn-icon--danger" onClick={() => handleDelete(item.id, item.destination)} title="Xóa bản sao lưu tại nơi cất giữ">
                                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                  <polyline points="3 6 5 6 21 6" />
                                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                                </svg>
                              </button>
                            </td>
                          </tr>
                        ))}
                        {filtered.length === 0 && (
                          <tr>
                            <td colSpan="8">
                              <div className="empty">
                                <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.3 }}>
                                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                                  <polyline points="17 8 12 3 7 8" />
                                  <line x1="12" y1="3" x2="12" y2="15" />
                                </svg>
                                <p>{backups.length === 0 ? 'Chưa có bản sao lưu nào' : 'Không tìm thấy kết quả phù hợp'}</p>
                              </div>
                            </td>
                          </tr>
                        )}
                      </tbody>
                    </table>
                  </div>

                  <div className="card-footer">
                    <span>{filtered.length === 0 ? 'Không có bản ghi' : `Hiển thị ${(safePage - 1) * perPage + 1}–${Math.min(safePage * perPage, filtered.length)} / ${filtered.length} bản ghi`}</span>
                    <div className="pagination">
                      <select
                        className="filter-select per-page"
                        value={perPage}
                        onChange={e => { setPerPage(Number(e.target.value)); setPageNum(1) }}
                        aria-label="Số bản ghi mỗi trang"
                      >
                        <option value={10}>10 / trang</option>
                        <option value={25}>25 / trang</option>
                        <option value={50}>50 / trang</option>
                        <option value={100}>100 / trang</option>
                      </select>
                      <button
                        className="pagination-btn"
                        onClick={() => setPageNum(safePage - 1)}
                        disabled={safePage <= 1}
                        aria-label="Trang trước"
                      >‹</button>
                      <span className="pagination-info">{safePage} / {totalPages}</span>
                      <button
                        className="pagination-btn"
                        onClick={() => setPageNum(safePage + 1)}
                        disabled={safePage >= totalPages}
                        aria-label="Trang sau"
                      >›</button>
                    </div>
                  </div>
                </section>
              </>
            )}
          </>
        )}
      </main>

      {createOpen && (
        <div className="modal-overlay" onClick={closeCreateModal}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h2>Tạo bản backup</h2>
              <button className="modal-close" onClick={closeCreateModal} aria-label="Đóng">×</button>
            </div>
            <form onSubmit={createBackup}>
              <div className="modal-body">
                <div className="settings-row">
                  <label className="settings-label">Nguồn dữ liệu</label>
                  <div className="path-pick">
                    <input className="settings-input path-input" type="text" placeholder="VD: /var/www/html hoặc /home/user/file.txt" value={createForm.source_path} onChange={e => handleCreateField('source_path', e.target.value)} />
                    <button type="button" className="btn-secondary btn-sm path-btn" onClick={() => setSourceModalOpen(true)} title="Chọn file hoặc thư mục từ máy">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" /></svg>
                    </button>
                  </div>
                  <p className="settings-hint">Nhập đường dẫn trên server, hoặc bấm + để chọn file/thư mục từ máy.</p>
                  {uploadFiles.length > 0 && (
                    <div className="upload-list">
                      {uploadFiles.length === 1
                        ? `Đã chọn từ máy: ${uploadFiles[0].name}`
                        : `Đã chọn từ máy ${uploadFiles.length} file/folder`}
                      <ul>
                        {uploadFiles.slice(0, 5).map((f, i) => (
                          <li key={i}>{f.webkitRelativePath || f.name}</li>
                        ))}
                        {uploadFiles.length > 5 && <li>... và {uploadFiles.length - 5} mục khác</li>}
                      </ul>
                      <button type="button" className="btn-secondary btn-sm" style={{ marginTop: 6 }} onClick={() => { setUploadFiles([]); setEmptyPick(false) }}>Bỏ chọn</button>
                    </div>
                  )}
                  {emptyPick && (
                    <div className="settings-row">
                      <label className="settings-label">Thư mục rỗng (từ máy) — nhập tên để tạo</label>
                      <input className="settings-input" type="text" placeholder="VD: Test (trình duyệt không tự gửi tên thư mục rỗng)" value={createForm.empty_folder} onChange={e => handleCreateField('empty_folder', e.target.value)} />
                    </div>
                  )}
                </div>

                <div className="settings-row">
                  <label className="settings-label">Nguồn hiển thị (tùy chọn)</label>
                  <input className="settings-input" type="text" placeholder="VD: Server Web LAMP" value={createForm.source_name} onChange={e => handleCreateField('source_name', e.target.value)} />
                </div>

                <div className="settings-row">
                  <label className="settings-label">Nơi cất giữ (chọn 1 hoặc nhiều)</label>
                  <div className="dest-check-row">
                    <label className={`dest-check ${(createForm.destinations || []).includes('server') ? 'dest-check--active' : ''}`}>
                      <input type="checkbox" checked={(createForm.destinations || []).includes('server')} onChange={() => toggleDestination('server')} />
                      <span className="dest-check-box"><span className="dot dot--server" /></span>
                      <span>Server</span>
                    </label>
                    <label className={`dest-check ${(createForm.destinations || []).includes('drive') ? 'dest-check--active' : ''}`}>
                      <input type="checkbox" checked={(createForm.destinations || []).includes('drive')} onChange={() => toggleDestination('drive')} />
                      <span className="dest-check-box"><span className="dot dot--drive" /></span>
                      <span>Google Drive</span>
                    </label>
                    <label className={`dest-check ${(createForm.destinations || []).includes('nas') ? 'dest-check--active' : ''}`}>
                      <input type="checkbox" checked={(createForm.destinations || []).includes('nas')} onChange={() => toggleDestination('nas')} />
                      <span className="dest-check-box"><span className="dot dot--nas" /></span>
                      <span>NAS</span>
                    </label>
                  </div>
                </div>

                {(createForm.destinations || []).includes('server') && (
                  <div className="settings-row">
                    <label className="settings-label">Đường dẫn lưu trên Server</label>
                    <div className="path-pick">
                      <input className="settings-input path-input" type="text" placeholder="VD: /opt/backups (mặc định /var/backups)" value={createForm.dest_path} onChange={e => handleCreateField('dest_path', e.target.value)} />
                      <button type="button" className="btn-secondary btn-sm path-btn" onClick={() => openFolderPicker(createForm.dest_path)} title="Chọn thư mục lưu trên server">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" /></svg>
                      </button>
                    </div>
                    <p className="settings-hint">Nhập đường dẫn hoặc bấm + để chọn thư mục lưu trên server. Trống = mặc định /var/backups.</p>
                  </div>
                )}
              </div>
              <div className="modal-footer">
                <button type="button" className="btn-secondary" onClick={closeCreateModal}>Hủy</button>
                <button type="submit" className="btn-primary" disabled={creating}>
                  {creating ? 'Đang tạo...' : 'Tạo backup'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {sourceModalOpen && (
        <div className="modal-overlay" onClick={() => setSourceModalOpen(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h2>Chọn nguồn dữ liệu</h2>
              <button className="modal-close" onClick={() => setSourceModalOpen(false)} aria-label="Đóng">×</button>
            </div>
            <div className="modal-body">
                <div className="settings-row">
                  <label className="settings-label">Chọn nguồn từ máy</label>
                  <div className="source-btns file-picker-btns">
                    <div className="file-picker">
                      <input type="file" id="upload-files" className="file-input" multiple onChange={handleFileChange} style={{ display: 'none' }} />
                      <label htmlFor="upload-files" className="file-btn">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.6, marginRight: 8 }}>
                          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                          <polyline points="17 8 12 3 7 8" />
                          <line x1="12" y1="3" x2="12" y2="15" />
                        </svg>
                        <span><strong>Chọn file</strong><br /><small>Tất cả định dạng: hình, video, tài liệu...</small></span>
                      </label>
                    </div>
                    <div className="file-picker">
                      <input type="file" id="upload-dir" className="file-input" multiple webkitdirectory="" onChange={handleFileChange} style={{ display: 'none' }} />
                      <label htmlFor="upload-dir" className="file-btn">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.6, marginRight: 8 }}>
                          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                        </svg>
                        <span><strong>Chọn thư mục</strong><br /><small>Lấy toàn bộ file bên trong, kể cả thư mục rỗng</small></span>
                      </label>
                    </div>
                  </div>
                  {emptyPick && (
                    <div className="empty-pick-note">
                      <strong>Đã chọn thư mục rỗng.</strong> Trình duyệt không gửi được tên thư mục rỗng, hãy gõ tên vào ô <em>"Tạo thư mục rỗng"</em> bên dưới.
                    </div>
                  )}
                  {uploadFiles.length > 0 && (
                    <div className="upload-list">
                      {uploadFiles.length === 1
                        ? `Đã chọn: ${uploadFiles[0].name}`
                        : `Đã chọn ${uploadFiles.length} file/folder`}
                      <ul>
                        {uploadFiles.slice(0, 5).map((f, i) => (
                          <li key={i}>{f.webkitRelativePath || f.name}</li>
                        ))}
                        {uploadFiles.length > 5 && <li>... và {uploadFiles.length - 5} mục khác</li>}
                      </ul>
                      <button type="button" className="btn-secondary btn-sm" style={{ marginTop: 6 }} onClick={() => { setUploadFiles([]); setEmptyPick(false) }}>Bỏ chọn</button>
                    </div>
                  )}
                </div>
              <div className="settings-divider" />
              <div className="settings-row">
                <label className="settings-label">Tạo thư mục rỗng (tùy chọn)</label>
                <input className="settings-input" type="text" placeholder="VD: Test (trình duyệt không tự gửi thư mục rỗng)" value={createForm.empty_folder} onChange={e => handleCreateField('empty_folder', e.target.value)} />
              </div>
            </div>
            <div className="modal-footer">
              <button type="button" className="btn-secondary" onClick={() => setSourceModalOpen(false)}>Đóng</button>
              <button type="button" className="btn-primary" onClick={() => setSourceModalOpen(false)}>Xong</button>
            </div>
          </div>
        </div>
      )}

      {folderOpen && (
        <div className="modal-overlay" onClick={() => setFolderOpen(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h2>Chọn thư mục trên Server</h2>
              <button className="modal-close" onClick={() => setFolderOpen(false)} aria-label="Đóng">×</button>
            </div>
            <div className="modal-body">
              <div className="folder-crumb">
                <button type="button" className="crumb-btn" onClick={() => loadFolder('/')}>/</button>
                {currentFolderPath.split('/').filter(Boolean).map((seg, i) => {
                  const upTo = '/' + currentFolderPath.split('/').filter(Boolean).slice(0, i + 1).join('/')
                  return (
                    <span key={i}>
                      <span className="crumb-sep">/</span>
                      <button type="button" className="crumb-btn" onClick={() => loadFolder(upTo)}>{seg}</button>
                    </span>
                  )
                })}
              </div>
              {folderLoading ? (
                <p className="settings-hint">Đang tải...</p>
              ) : currentFolders.length === 0 ? (
                <p className="settings-hint">Thư mục trống hoặc không có thư mục con.</p>
              ) : (
                <div className="folder-list">
                  {currentFolders.map(f => (
                    <div key={f.path} className={`folder-item ${f.write ? '' : 'folder-item--noread'}`}>
                      <button type="button" className="folder-name" onClick={() => loadFolder(f.path)}>
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" style={{ opacity: 0.6 }}>
                          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                        </svg>
                        <span>{f.name}</span>
                      </button>
                      <div className="folder-actions">
                        {!f.write && <span className="folder-note">không ghi được</span>}
                        <button type="button" className="btn-secondary btn-sm" onClick={() => { handleCreateField('dest_path', f.path); setFolderOpen(false) }}>Chọn</button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
            <div className="modal-footer">
              <button
                type="button"
                className={currentFolderPath === '/' ? 'btn-primary' : 'btn-secondary'}
                onClick={() => { handleCreateField('dest_path', currentFolderPath); setFolderOpen(false) }}
              >
                Sử dụng thư mục hiện tại: {currentFolderPath}
              </button>
            </div>
          </div>
        </div>
      )}

      {toast && (
        <div className={`toast toast--${toast.type}`}>
          <span>{toast.message}</span>
          <button className="toast-close" onClick={() => setToast(null)}>×</button>
        </div>
      )}
    </div>
  )
}

export default App
