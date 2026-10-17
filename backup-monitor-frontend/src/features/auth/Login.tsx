import { Button } from '../../shared/ui'
import { Database } from 'lucide-react'
import { makeTheme } from '../../shared/theme'
import { useEffect, useState } from 'react'
import { apiErrorMessage, createAdministrator, fetchAuthStatus, login } from '../../api/dashboard'
import { tr, type Lang } from '../../shared/language'
import { localizeMessage } from '../../shared/messages'

interface LoginProps {
  isDark: boolean
  lang: Lang
  onToggleLang: () => void
  onLogin: (token: string) => void
}

export default function Login({ isDark, lang, onToggleLang, onLogin }: LoginProps) {
  const theme = makeTheme(isDark)
  const { bg, cardBg, cardBorder, textPrimary, textSecondary } = theme
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null)
  const [checking, setChecking] = useState(true)
  const [statusAttempt, setStatusAttempt] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    let mounted = true
    fetchAuthStatus().then(status => {
      if (mounted) setSetupRequired(status.setupRequired)
    }).catch(statusError => {
      if (mounted) setError(apiErrorMessage(statusError, tr('vi', 'accountStatusError')))
    }).finally(() => {
      if (mounted) setChecking(false)
    })
    return () => { mounted = false }
  }, [statusAttempt])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (setupRequired === null || checking || loading) return
    setError('')
    if (setupRequired && (password !== confirmation || new TextEncoder().encode(password).length < 12 || new TextEncoder().encode(password).length > 72)) {
      setError(tr(lang, password !== confirmation ? 'passwordMismatch' : 'passwordLengthError'))
      return
    }
    setLoading(true)
    try {
      if (setupRequired) {
        await createAdministrator(username, password)
        setSetupRequired(false)
        setConfirmation('')
      }
      const token = await login(username, password)
      localStorage.setItem('auth_token', token)
      onLogin(token)
    } catch (loginError: unknown) {
      setError(apiErrorMessage(loginError, tr(lang, 'loginConnectionError'), lang))
      // Another tab may already have completed setup, or its response was lost.
      if (setupRequired) {
        try { setSetupRequired((await fetchAuthStatus()).setupRequired) } catch { /* Keep the current form; retry remains explicit. */ }
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-page" data-theme={isDark ? 'dark' : 'light'} style={{
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
            background: theme.primary,
            color: theme.onPrimary,
            borderRadius: 14,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '0 auto 16px',
            fontSize: 24,
          }}>
            <Database size={28} />
          </div>
          <h1 style={{ color: textPrimary, fontSize: 22, fontWeight: 700, margin: 0 }}>
            {tr(lang, setupRequired ? 'setupTitle' : 'loginTitle')}
          </h1>
          <p style={{ color: textSecondary, fontSize: 14, marginTop: 6 }}>
            {tr(lang, checking ? 'checkingAccount' : setupRequired ? 'setupDescription' : 'loginDescription')}
          </p>
          <div role="group" aria-label={lang === 'vi' ? 'Ngôn ngữ' : 'Language'} style={{ display: 'flex', justifyContent: 'center', gap: 8 }}>
            <Button type="button" theme={theme} aria-pressed={lang === 'vi'} onClick={() => { if (lang !== 'vi') onToggleLang() }}>Tiếng Việt</Button>
            <Button type="button" theme={theme} aria-pressed={lang === 'en'} onClick={() => { if (lang !== 'en') onToggleLang() }}>English</Button>
          </div>
        </div>

        <form onSubmit={handleSubmit}>
          {/* Username */}
          <div style={{ marginBottom: 16 }}>
            <label htmlFor="admin-username" style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>
              {tr(lang, 'username')}
            </label>
            <input
              id="admin-username"
              type="text"
              autoComplete="username"
              disabled={checking || setupRequired === null || loading}
              pattern={setupRequired ? '[a-zA-Z0-9][a-zA-Z0-9_.\\-]{2,63}' : undefined}
              title={setupRequired ? tr(lang, 'usernameHint') : undefined}
              value={username}
              onChange={e => setUsername(e.target.value)}
              placeholder={tr(lang, 'usernamePlaceholder')}
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
            <label htmlFor="admin-password" style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>
              {tr(lang, 'password')}
            </label>
            <input
              id="admin-password"
              type="password"
              autoComplete={setupRequired ? 'new-password' : 'current-password'}
              disabled={checking || setupRequired === null || loading}
              value={password}
              onChange={e => setPassword(e.target.value)}
              placeholder={tr(lang, 'passwordPlaceholder')}
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

          {setupRequired && <div style={{ marginBottom: 24 }}>
            <label htmlFor="admin-confirmation" style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>{tr(lang, 'confirmPassword')}</label>
            <input id="admin-confirmation" type="password" autoComplete="new-password" required
              value={confirmation} onChange={e => setConfirmation(e.target.value)} disabled={loading}
              style={{ width: '100%', padding: '10px 14px', background: bg, border: `1px solid ${cardBorder}`, borderRadius: 8, color: textPrimary, fontSize: 14, boxSizing: 'border-box' }} />
            <p style={{ color: textSecondary, fontSize: 12, lineHeight: 1.6 }}>{tr(lang, 'passwordHint')}</p>
          </div>}

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
              <span role="alert">{localizeMessage(error, lang)}</span>
            </div>
          )}

          {!checking && setupRequired === null && <Button theme={theme} type="button" onClick={() => {
            setError('')
            setChecking(true)
            setStatusAttempt(value => value + 1)
          }} style={{ width: '100%', marginBottom: 12 }}>{tr(lang, 'retryConnection')}</Button>}

          {/* Submit */}
          <Button theme={theme} variant="primary"
            type="submit"
            disabled={loading || checking || setupRequired === null}
            style={{ width: '100%' }}
          >
            {tr(lang, loading ? (setupRequired ? 'creatingAccount' : 'signingIn') : setupRequired ? 'createAndSignIn' : 'signIn')}
          </Button>
        </form>
      </div>
    </div>
  )
}
