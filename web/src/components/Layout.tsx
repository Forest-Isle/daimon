import type { ComponentChildren } from 'preact'
import { useLocation } from 'wouter'

const NAV_ITEMS = [
  { href: '/', label: 'Overview', icon: '\u25C9' },
  { href: '/sessions', label: 'Sessions', icon: '\u2630' },
  { href: '/metrics', label: 'Metrics', icon: '\u2261' },
]

export function Layout({ children, connected }: { children: ComponentChildren; connected: boolean }) {
  const [location] = useLocation()

  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      <nav style={{
        width: 200, padding: '20px 16px',
        background: 'var(--bg-secondary)', borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column',
      }}>
        <h2 style={{ fontSize: 16, marginBottom: 24 }}>IronClaw</h2>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1 }}>
          {NAV_ITEMS.map(item => {
            const active = item.href === '/' ? location === '/' : location.startsWith(item.href)
            return (
              <a key={item.href} href={item.href} style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: '8px 12px', borderRadius: 6, fontSize: 14,
                background: active ? 'var(--bg-tertiary)' : 'transparent',
                color: active ? 'var(--text-primary)' : 'var(--text-secondary)',
                transition: 'background 0.15s',
              }}>
                <span style={{ fontSize: 12 }}>{item.icon}</span>
                {item.label}
              </a>
            )
          })}
        </div>
        <div style={{
          fontSize: 11, color: 'var(--text-secondary)',
          paddingTop: 16, borderTop: '1px solid var(--border)',
        }}>
          IronClaw Dashboard
        </div>
      </nav>
      <main style={{ flex: 1, padding: 24, minWidth: 0 }}>
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}>
          <span style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            fontSize: 13, color: 'var(--text-secondary)',
          }}>
            <span style={{
              width: 8, height: 8, borderRadius: '50%',
              background: connected ? 'var(--success)' : 'var(--error)',
            }} />
            {connected ? 'Connected' : 'Disconnected'}
          </span>
        </div>
        {children}
      </main>
    </div>
  )
}
