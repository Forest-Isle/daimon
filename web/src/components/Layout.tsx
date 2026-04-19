import type { ComponentChildren } from 'preact'

export function Layout({ children, connected }: { children: ComponentChildren; connected: boolean }) {
  return (
    <div style={{ display: 'flex', minHeight: '100vh' }}>
      <nav style={{
        width: 200, padding: '20px 16px',
        background: 'var(--bg-secondary)', borderRight: '1px solid var(--border)',
      }}>
        <h2 style={{ fontSize: 16, marginBottom: 24 }}>IronClaw</h2>
        <a href="/" style={{ display: 'block', padding: '8px 12px', borderRadius: 6, background: 'var(--bg-tertiary)' }}>
          Overview
        </a>
      </nav>
      <main style={{ flex: 1, padding: 24 }}>
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
