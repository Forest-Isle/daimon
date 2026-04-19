import type { StateSnapshot } from '../lib/types'

export function AgentStatus({ status, sessions }: {
  status: 'idle' | 'busy'
  sessions: StateSnapshot['active_sessions']
}) {
  const session = sessions[0]
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Agent Status</h3>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span style={{
          padding: '4px 12px', borderRadius: 12, fontSize: 13, fontWeight: 600,
          background: status === 'busy' ? 'rgba(88,166,255,0.15)' : 'var(--bg-tertiary)',
          color: status === 'busy' ? 'var(--accent)' : 'var(--text-secondary)',
        }}>
          {status.toUpperCase()}
        </span>
        {session && (
          <span style={{ fontSize: 14 }}>
            Phase: <strong>{session.current_phase || '\u2014'}</strong>
            {session.current_tool && <> &#9656; tool: <code>{session.current_tool}</code></>}
          </span>
        )}
      </div>
      {session && (
        <div style={{ marginTop: 8, fontSize: 13, color: 'var(--text-secondary)' }}>
          Session: {session.session_id.slice(0, 8)} ({session.channel || 'unknown'})
        </div>
      )}
    </div>
  )
}
