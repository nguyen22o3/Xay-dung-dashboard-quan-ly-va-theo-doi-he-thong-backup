import { useState } from 'react'
import { apiErrorMessage, login } from '../api'

interface LoginProps {
  isDark: boolean
  onLogin: (token: string) => void
}

export default function Login({ isDark, onLogin }: LoginProps) {
  const bg = isDark ? '#1e1e1e' : '#f4f5f7'
  const cardBg = isDark ? '#252526' : '#ffffff'
  const cardBorder = isDark ? '#333333' : '#dcdcdc'
  const textPrimary = isDark ? '#cccccc' : '#333333'
  const textSecondary = isDark ? '#888888' : '#777777'
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const token = await login(username, password)
      localStorage.setItem('auth_token', token)
      onLogin(token)
    } catch (loginError: unknown) {
      setError(apiErrorMessage(loginError, 'Không thể kết nối đến máy chủ'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: bg,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
    }}>
      <div style={{
        background: cardBg,
        border: `1px solid ${cardBorder}`,
        borderRadius: 16,
        padding: '48px 40px',
        width: '100%',
        maxWidth: 400,
        boxShadow: '0 8px 32px rgba(0,0,0,0.2)',
      }}>
        {/* Logo / Title */}
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div style={{
            width: 56,
            height: 56,
            background: 'linear-gradient(135deg, #3b82f6, #8b5cf6)',
            borderRadius: 14,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '0 auto 16px',
            fontSize: 24,
          }}>
            🛡️
          </div>
          {/* <h1 style={{ color: textPrimary, fontSize: 22, fontWeight: 700, margin: 0 }}>
            aaPanel
          </h1> */}
          <p style={{ color: textSecondary, fontSize: 14, marginTop: 6 }}>
            Đăng nhập để tiếp tục
          </p>
        </div>

        <form onSubmit={handleSubmit}>
          {/* Username */}
          <div style={{ marginBottom: 16 }}>
            <label style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>
              Tên đăng nhập
            </label>
            <input
              type="text"
              value={username}
              onChange={e => setUsername(e.target.value)}
              placeholder="Nhập tên đăng nhập"
              required
              style={{
                width: '100%',
                padding: '10px 14px',
                background: bg,
                border: `1px solid ${cardBorder}`,
                borderRadius: 8,
                color: textPrimary,
                fontSize: 14,
                outline: 'none',
                boxSizing: 'border-box',
              }}
            />
          </div>

          {/* Password */}
          <div style={{ marginBottom: 24 }}>
            <label style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>
              Mật khẩu
            </label>
            <input
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              placeholder="Nhập mật khẩu"
              required
              style={{
                width: '100%',
                padding: '10px 14px',
                background: bg,
                border: `1px solid ${cardBorder}`,
                borderRadius: 8,
                color: textPrimary,
                fontSize: 14,
                outline: 'none',
                boxSizing: 'border-box',
              }}
            />
          </div>

          {/* Error */}
          {error && (
            <div style={{
              background: 'rgba(239,68,68,0.1)',
              border: '1px solid rgba(239,68,68,0.3)',
              borderRadius: 8,
              padding: '10px 14px',
              color: '#ef4444',
              fontSize: 13,
              marginBottom: 16,
            }}>
              {error}
            </div>
          )}

          {/* Submit */}
          <button
            type="submit"
            disabled={loading}
            style={{
              width: '100%',
              padding: '11px',
              background: loading ? '#6b7280' : 'linear-gradient(135deg, #3b82f6, #8b5cf6)',
              border: 'none',
              borderRadius: 8,
              color: '#fff',
              fontSize: 15,
              fontWeight: 600,
              cursor: loading ? 'not-allowed' : 'pointer',
            }}
          >
            {loading ? 'Đang đăng nhập...' : 'Đăng nhập'}
          </button>
        </form>
      </div>
    </div>
  )
}
