import type { StateSnapshot } from '../lib/types'

export function SessionList({ sessions, total }: {
  sessions: StateSnapshot['active_sessions']
  total: number
}) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>
        Sessions ({total} today)
      </h3>
      {sessions.length === 0 && (
        <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>No active sessions</div>
      )}
      {sessions.map(s => (
        <div key={s.session_id} style={{
          display: 'flex', justifyContent: 'space-between', padding: '8px 0',
          borderBottom: '1px solid var(--border)', fontSize: 13,
        }}>
          <span><code>{s.session_id.slice(0, 8)}</code> ({s.channel || '?'})</span>
          <span style={{ color: 'var(--text-secondary)' }}>
            {s.tools_executed} tools {'\u00B7'} {s.replan_count} replans
          </span>
        </div>
      ))}
    </div>
  )
}
