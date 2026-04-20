import type { ToolEvent } from '../lib/types'

export function ToolCallFeed({ tools }: { tools: ToolEvent[] }) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
      maxHeight: 300, overflowY: 'auto',
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Tool Calls</h3>
      {tools.length === 0 && (
        <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>No tool calls yet</div>
      )}
      {tools.map(t => (
        <div key={t.id} style={{
          display: 'flex', gap: 12, padding: '6px 0',
          borderBottom: '1px solid var(--border)', fontSize: 13,
        }}>
          <span style={{ color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)', minWidth: 70 }}>
            {new Date(t.timestamp).toLocaleTimeString()}
          </span>
          <code style={{ minWidth: 100 }}>{t.tool_name}</code>
          {t.running ? (
            <span style={{ color: 'var(--warning)' }}>{'\u23F3'} running\u2026</span>
          ) : (
            <span style={{ color: t.succeeded ? 'var(--success)' : 'var(--error)' }}>
              {t.succeeded ? '\u2713' : '\u2717'} {t.duration_ms}ms
            </span>
          )}
        </div>
      ))}
    </div>
  )
}
