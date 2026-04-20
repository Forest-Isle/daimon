import type { SubAgentEvent } from '../lib/types'

export function SubAgentList({ subAgents, compressionCount }: {
  subAgents: SubAgentEvent[]
  compressionCount: number
}) {
  if (subAgents.length === 0 && compressionCount === 0) {
    return null
  }

  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', margin: 0 }}>Sub-Agents &amp; Context</h3>
        {compressionCount > 0 && (
          <span style={{
            padding: '2px 8px', borderRadius: 12, fontSize: 11, fontWeight: 600,
            background: 'rgba(136,108,228,0.15)', color: '#886ce4',
          }}>
            {compressionCount} compression{compressionCount > 1 ? 's' : ''}
          </span>
        )}
      </div>
      {subAgents.length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>No sub-agent activity</div>
      ) : (
        subAgents.map(sa => (
          <div key={sa.session_id} style={{
            display: 'flex', alignItems: 'center', gap: 12, padding: '6px 0',
            borderBottom: '1px solid var(--border)', fontSize: 13,
          }}>
            <code style={{ minWidth: 100 }}>{sa.agent_name}</code>
            {sa.task && (
              <span style={{ color: 'var(--text-secondary)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {sa.task.length > 60 ? sa.task.slice(0, 60) + '...' : sa.task}
              </span>
            )}
            {sa.running ? (
              <span style={{
                padding: '2px 8px', borderRadius: 12, fontSize: 11, fontWeight: 600,
                background: 'rgba(88,166,255,0.15)', color: 'var(--accent)',
              }}>
                running
              </span>
            ) : (
              <span style={{
                padding: '2px 8px', borderRadius: 12, fontSize: 11, fontWeight: 600,
                background: sa.succeeded ? 'rgba(63,185,80,0.15)' : 'rgba(248,81,73,0.15)',
                color: sa.succeeded ? 'var(--success)' : 'var(--error)',
              }}>
                {sa.succeeded ? 'done' : 'failed'}
                {sa.duration_ms != null && ` ${sa.duration_ms}ms`}
              </span>
            )}
          </div>
        ))
      )}
    </div>
  )
}
