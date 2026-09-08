import { useState, useEffect, useCallback, useMemo } from 'react'
import './App.css'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Backups from './pages/Backups'
import Settings from './pages/Settings'
import Sidebar from './components/Layout/Sidebar'
import Topbar from './components/Layout/Topbar'
import CreateBackupModal from './components/Modals/CreateBackupModal'
import SourceModal from './components/Modals/SourceModal'
import FolderPickerModal from './components/Modals/FolderPickerModal'


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
    custom_file_name: '',
  })
  const [uploadFiles, setUploadFiles] = useState([])
  const [sourceModalOpen, setSourceModalOpen] = useState(false)
  const [folderOpen, setFolderOpen] = useState(false)
  const [currentFolderPath, setCurrentFolderPath] = useState('/')
  const [currentFolders, setCurrentFolders] = useState([])
  const [folderLoading, setFolderLoading] = useState(false)
  const [findResults, setFindResults] = useState([])
  const [findName, setFindName] = useState('')
  const [nativePicking, setNativePicking] = useState(false)

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

  const handleDelete = async (item) => {
    if (!window.confirm(`Bạn có chắc chắn muốn xóa bản backup này trên tất cả các nơi cất giữ?`)) return
    try {
      const dests = item.dests ? Object.values(item.dests) : [item]
      // Thực hiện xóa song song (concurrent) để tăng tốc độ
      await Promise.all(dests.map(dest => 
        fetch(`/api/delete?id=${dest.id}`, {
          method: 'DELETE',
          headers: ah(),
        })
      ))
      showToast('Xóa bản backup thành công')
      await fetchBackups()
    } catch (e) {
      console.error('Lỗi khi xóa:', e)
      showToast('Xóa bản backup thất bại', 'error')
    }
  }

  const handleCreateField = (field, value) => {
    setCreateForm(prev => ({ ...prev, [field]: value }))
  }

  const createBackup = async (e) => {
    e.preventDefault()
    const hasUploadFiles = uploadFiles && uploadFiles.length > 0
    const hasSourcePath = createForm.source_path && createForm.source_path.trim()
    if (!hasUploadFiles && !hasSourcePath) {
      showToast('Vui lòng chọn nguồn dữ liệu (chọn bên nút + ở ô "Nguồn dữ liệu")', 'error')
      return
    }
    if (!createForm.destinations || createForm.destinations.length === 0) {
      showToast('Vui lòng chọn ít nhất một nơi cất giữ', 'error')
      return
    }
    if ((createForm.destinations || []).includes('server') && !createForm.dest_path?.trim()) {
      showToast('Vui lòng chọn chỗ lưu trên Server (bấm + để chọn tự do, không có mặc định)', 'error')
      return
    }
    setCreating(true)
    try {
      let res
      if (hasUploadFiles) {
        const fd = new FormData()
        for (const f of uploadFiles) fd.append('files', f, f.webkitRelativePath || f.name)
        fd.append('destinations', JSON.stringify(createForm.destinations))
        fd.append('dest_path', createForm.dest_path)
        fd.append('source_name', createForm.source_name)
        fd.append('custom_file_name', createForm.custom_file_name)
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
      setCreateForm({ source_path: '', destinations: ['server'], dest_path: '', source_name: '', custom_file_name: '' })
      setUploadFiles([])
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
    if (files.length === 0) return
    // Gộp file mới vào danh sách cũ để cho phép chọn file + thư mục nhiều lần
    setUploadFiles(prev => {
      const merged = [...prev, ...files]
      // Loại trùng theo name + size + lastModified + webkitRelativePath
      const seen = new Set()
      return merged.filter(f => {
        const key = `${f.webkitRelativePath || f.name}-${f.size}-${f.lastModified}`
        if (seen.has(key)) return false
        seen.add(key)
        return true
      })
    })
    // Reset input để chọn lại cùng file vẫn trigger onChange
    e.target.value = ''
  }

  const handleDrop = (e) => {
    e.preventDefault()
    const items = e.dataTransfer?.items
    const files = []
    if (items) {
      for (const item of items) {
        const file = item.getAsFile?.()
        if (file) files.push(file)
      }
    } else {
      files.push(...Array.from(e.dataTransfer?.files || []))
    }
    if (files.length > 0) {
      setUploadFiles(prev => {
        const merged = [...prev, ...files]
        const seen = new Set()
        return merged.filter(f => {
          const key = `${f.webkitRelativePath || f.name}-${f.size}-${f.lastModified}`
          if (seen.has(key)) return false
          seen.add(key)
          return true
        })
      })
    }
  }

  const closeCreateModal = () => {
    setCreateOpen(false)
    setSourceModalOpen(false)
    setFolderOpen(false)
  }

  const hasUpload = uploadFiles && uploadFiles.length > 0
  const hasSourcePath = !!createForm.source_path && createForm.source_path.trim() !== ''

  const sourceTags = []
  if (hasUpload) sourceTags.push({ key: 'upload', label: 'Từ máy', cls: 'dest-tag--server' })
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

const handleDestPickClick = async () => {
    setNativePicking(true)
    showToast('Đang mở cửa sổ chọn thư mục...', 'info')
    try {
      // ƯU TIÊN: cửa sổ chọn thư mục NATIVE của Ubuntu (zenity trên backend)
      // — mở đúng hộp thoại file của hệ điều hành + trả về đường dẫn tuyệt đối
      const res = await fetch(`/api/native-picker?start=${encodeURIComponent(createForm.dest_path || '/home/ddnguyen')}`, { headers: ah() })
      if (res.ok) {
        const data = await res.json()
        if (data.path) {
          handleCreateField('dest_path', data.path)
          showToast(`Đã chọn: ${data.path}`)
          setNativePicking(false)
          return
        }
        setNativePicking(false)
        return // user hủy cửa sổ native
      }
    } catch {}
    setNativePicking(false)

    // FALLBACK 1: browser file dialog (Chrome/Brave/Edge)
    if (window.showDirectoryPicker) {
      try {
        const handle = await window.showDirectoryPicker()
        const dirName = handle.name
        // Gửi tên thư mục lên server để tìm đường dẫn thực
        try {
          const res = await fetch(`/api/find-dir?name=${encodeURIComponent(dirName)}`, { headers: ah() })
          if (res.ok) {
            const data = await res.json()
            if (data.results && data.results.length === 1) {
              handleCreateField('dest_path', data.results[0].path)
              showToast(`Đã chọn: ${data.results[0].path}`)
              return
            } else if (data.results && data.results.length > 1) {
              setFindResults(data.results)
              setFindName(dirName)
              setFolderOpen(true)
              await loadFolder(data.results[0].path)
              return
            }
          }
        } catch {}
        showToast(`Không tìm thấy thư mục "${dirName}" trên server — vui lòng nhập đường dẫn`, 'error')
        setFolderOpen(true)
        await loadFolder('/')
        return
      } catch (err) {
        if (err?.name === 'AbortError') return
      }
    }
    // FALLBACK 2: popup duyệt thư mục server
    setFolderOpen(true)
    await loadFolder(createForm.dest_path || '/')
  }

  const handleSourcePickClick = async (kind = 'dir') => {
    setNativePicking(true)
    showToast(kind === 'dir' ? 'Đang mở cửa sổ chọn thư mục...' : 'Đang mở cửa sổ chọn file...', 'info')
    try {
      // Nút chọn của Nguồn dữ liệu: mở cửa sổ native Ubuntu.
      // kind=dir  → chọn THƯ MỤC (OK sáng khi chỉ vào thư mục)
      // kind=file → chọn FILE (GTK chặn OK đối với thư mục, nên tách riêng 2 nút)
      const res = await fetch(`/api/native-picker?start=${encodeURIComponent(createForm.source_path || '/home/ddnguyen')}&kind=${kind}`, { headers: ah() })
      if (res.ok) {
        const data = await res.json()
        if (data.path) {
          handleCreateField('source_path', data.path)
          showToast(`Đã chọn nguồn: ${data.path}`)
          setNativePicking(false)
          return
        }
        setNativePicking(false)
        return // user hủy
      }
    } catch {}
    setNativePicking(false)
    showToast('Không mở được cửa sổ chọn — hãy nhập đường dẫn hoặc chọn từ máy', 'error')
    setSourceModalOpen(true)
  }

  const sources = useMemo(() => {
    const set = new Set(backups.map(b => b.source))
    return ['all', ...Array.from(set)]
  }, [backups])

  const destinations = useMemo(() => {
    const set = new Set(backups.map(b => b.destination).filter(Boolean))
    return ['all', ...Array.from(set)]
  }, [backups])

  const allGroups = useMemo(() => {
    const map = new Map();
    backups.forEach(item => {
      const key = item.status === 'Missed' ? `missed_${item.id}` : `${item.source}_${item.file_name}`;
      if (!map.has(key)) {
         map.set(key, {
            ...item,
            dests: { [item.destination || '--']: item }
         });
      } else {
         const group = map.get(key);
         group.dests[item.destination || '--'] = item;
         // If any destination in the group succeeded, consider the whole backup run a success
         if (item.status === 'Success') group.status = 'Success';
      }
    });
    const groups = Array.from(map.values());
    
    // Assign stable displayIds (assuming backups are sorted newest first from backend)
    groups.forEach((g, idx) => {
      g.displayId = groups.length - idx;
    });
    return groups;
  }, [backups])

  const filtered = useMemo(() => {
    return allGroups.filter(item => {
      if (filterStatus !== 'all' && item.status !== filterStatus) return false
      if (filterSource !== 'all' && item.source !== filterSource) return false
      if (filterDestination !== 'all' && !item.dests[filterDestination]) return false
      if (search) {
        const q = search.toLowerCase()
        return (
          item.file_name.toLowerCase().includes(q) ||
          item.source.toLowerCase().includes(q) ||
          Object.keys(item.dests).some(d => d.toLowerCase().includes(q))
        )
      }
      return true
    })
  }, [allGroups, search, filterStatus, filterSource, filterDestination])

  const totalPages = Math.max(1, Math.ceil(filtered.length / perPage))
  const safePage = Math.min(pageNum, totalPages)
  const paginated = filtered.slice((safePage - 1) * perPage, safePage * perPage)

  const stats = useMemo(() => {
    const total = allGroups.length
    const success = allGroups.filter(b => b.status === 'Success').length
    const failed = total - success
    const totalSize = allGroups.reduce((sum, b) => sum + b.size_mb, 0)
    const successRate = total > 0 ? Math.round((success / total) * 100) : 0
    return { total, success, failed, totalSize, successRate }
  }, [allGroups])

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
    dashboard: { title: 'Tổng quan & Bản backup', desc: 'Theo dõi thống kê và quản lý danh sách lịch sử backup' },
    settings: { title: 'Cài đặt', desc: 'Cấu hình hệ thống và tùy chọn' },
  }

  const current = pageTitles[page] || pageTitles.dashboard

  if (!token) {
    return (
      <Login
        username={username}
        setUsername={setUsername}
        password={password}
        setPassword={setPassword}
        handleLogin={handleLogin}
        loginError={loginError}
      />
    )
  }

  return (
    <div className="layout">
      <Sidebar
        page={page}
        setPage={setPage}
        handleLogout={handleLogout}
        backendOnline={backendOnline}
      />

      <main className="main">
        <Topbar
          current={current}
          page={page}
          stats={stats}
          setCreateOpen={setCreateOpen}
          handleRefresh={handleRefresh}
          refreshing={refreshing}
        />

        {page === 'settings' ? (
          <Settings
            config={config}
            setConfigField={setConfigField}
            saveConfig={saveConfig}
            testNotify={testNotify}
            darkMode={darkMode}
            toggleDarkMode={toggleDarkMode}
            schedules={schedules}
            scheduleForm={scheduleForm}
            handleScheduleForm={handleScheduleForm}
            saveSchedule={saveSchedule}
            editSchedule={editSchedule}
            deleteSchedule={deleteSchedule}
            setScheduleForm={setScheduleForm}
            backendOnline={backendOnline}
            stats={stats}
            handleClearAll={handleClearAll}
          />
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
                  <>
                    <Dashboard stats={stats} />
                    <Backups
                      search={search}
                      setSearch={setSearch}
                      filterStatus={filterStatus}
                      setFilterStatus={setFilterStatus}
                      filterSource={filterSource}
                      setFilterSource={setFilterSource}
                      filterDestination={filterDestination}
                      setFilterDestination={setFilterDestination}
                      sources={sources}
                      destinations={destinations}
                      backups={backups}
                      paginated={paginated}
                      filtered={filtered}
                      safePage={safePage}
                      totalPages={totalPages}
                      setPageNum={setPageNum}
                      handleDelete={handleDelete}
                    />
                  </>
                )}
              </>
            )}
          </>
        )}
      </main>

      {createOpen && (
        <CreateBackupModal
          closeCreateModal={closeCreateModal}
          createBackup={createBackup}
          createForm={createForm}
          handleCreateField={handleCreateField}
          handleSourcePickClick={handleSourcePickClick}
          uploadFiles={uploadFiles}
          setUploadFiles={setUploadFiles}
          toggleDestination={toggleDestination}
          handleDestPickClick={handleDestPickClick}
          creating={creating}
        />
      )}

      {sourceModalOpen && (
        <SourceModal
          setSourceModalOpen={setSourceModalOpen}
          handleDrop={handleDrop}
          handleFileChange={handleFileChange}
          uploadFiles={uploadFiles}
          setUploadFiles={setUploadFiles}
        />
      )}

      {folderOpen && (
        <FolderPickerModal
          setFolderOpen={setFolderOpen}
          findResults={findResults}
          setFindResults={setFindResults}
          findName={findName}
          setFindName={setFindName}
          handleCreateField={handleCreateField}
          currentFolderPath={currentFolderPath}
          setCurrentFolderPath={setCurrentFolderPath}
          loadFolder={loadFolder}
          folderLoading={folderLoading}
          currentFolders={currentFolders}
        />
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
