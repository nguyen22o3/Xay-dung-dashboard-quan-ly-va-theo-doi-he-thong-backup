import { Sun, Moon, Languages } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { tr } from '../language'

export default function Settings({
  isDark,
  onToggleDark,
  lang,
  onToggleLang
}: {
  isDark: boolean
  onToggleDark: () => void
  lang: Lang
  onToggleLang: () => void
}) {
  const t = makeTheme(isDark)
  const language = lang

  return (
    <div className="animate-fade-in" style={{ 
      padding: '40px 20px', 
      height: '100%', 
      display: 'flex', 
      flexDirection: 'column', 
      alignItems: 'center', 
      justifyContent: 'flex-start' 
    }}>
      
      <div style={{ width: '100%', maxWidth: '600px' }}>
        <h2 style={{ margin: '0 0 20px 0', fontSize: '22px', fontWeight: 'normal', color: t.titleColor }}>
          {tr(lang, 'settings')}
        </h2>

        <div style={{ display: 'grid', gap: '20px' }}>
          <div style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}`, borderRadius: '4px', padding: '20px', boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)' }}>
            <h3 style={{ margin: '0 0 15px 0', fontSize: '16px', color: t.titleColor, display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Languages size={18} /> {language === 'vi' ? 'Ngôn ngữ' : 'Language'}
            </h3>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ color: t.textSecondary, fontSize: '13px' }}>
                {language === 'vi' ? 'Chuyển đổi giao diện sang Tiếng Việt / Tiếng Anh' : 'Switch interface to Vietnamese / English'}
              </div>
              <button
                onClick={onToggleLang}
                style={{
                  background: '#2196f3',
                  border: 'none',
                  color: 'white',
                  borderRadius: '4px',
                  padding: '8px 16px',
                  cursor: 'pointer',
                  fontSize: '13px',
                  fontWeight: 'bold',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  transition: 'background 0.2s'
                }}
              >
                <Languages size={16} /> {language === 'vi' ? 'TIẾNG VIỆT' : 'ENGLISH'}
              </button>
            </div>
          </div>

          <div style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}`, borderRadius: '4px', padding: '20px', boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)' }}>
            <h3 style={{ margin: '0 0 15px 0', fontSize: '16px', color: t.titleColor, display: 'flex', alignItems: 'center', gap: '8px' }}>
              {isDark ? <Moon size={18} /> : <Sun size={18} />} {language === 'vi' ? 'Giao diện' : 'Appearance'}
            </h3>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ color: t.textSecondary, fontSize: '13px' }}>
                {language === 'vi' ? 'Chuyển đổi giữa chế độ Sáng / Tối' : 'Switch between Light / Dark mode'}
              </div>
              <button
                onClick={onToggleDark}
                style={{
                  background: isDark ? '#333' : '#e0e0e0',
                  border: 'none',
                  color: isDark ? 'white' : '#333',
                  borderRadius: '4px',
                  padding: '8px 16px',
                  cursor: 'pointer',
                  fontSize: '13px',
                  fontWeight: 'bold',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  transition: 'background 0.2s'
                }}
              >
                {isDark ? <Moon size={16} /> : <Sun size={16} />} {isDark ? (language === 'vi' ? 'TỐI' : 'DARK') : (language === 'vi' ? 'SÁNG' : 'LIGHT')}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
