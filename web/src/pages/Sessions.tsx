import { useState, useEffect } from 'preact/hooks'
import { Layout } from '../components/Layout'
import { useAgentState } from '../hooks/useAgentState'
import { fetchSessions } from '../lib/api'
import type { SessionInfo } from '../lib/types'

export function Sessions() {
  const { connected } = useAgentState()
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchSessions()
      .then(data => { setSessions(data ?? []); setLoading(false) })
      .catch(err => { setError(err.message); setLoading(false) })
  }, [])

  return (
    <Layout connected={connected}>
      <h2 style={{ fontSize: 18, marginBottom: 16 }}>Sessions</h2>
      {error && (
        <div style={{
          padding: '12px 20px', marginBottom: 16,
          background: 'rgba(248,81,73,0.1)', border: '1px solid var(--error)',
          borderRadius: 8, fontSize: 13, color: 'var(--error)',
        }}>
          Failed to load sessions: {error}
        </div>
      )}
      {loading ? (
        <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>Loading...</div>
      ) : sessions.length === 0 ? (
        <div style={{
          padding: 40, textAlign: 'center', color: 'var(--text-secondary)',
          background: 'var(--bg-secondary)', borderRadius: 8, border: '1px solid var(--border)',
        }}>
          No sessions found
        </div>
      ) : (
        <div style={{
          background: 'var(--bg-secondary)', borderRadius: 8,
          border: '1px solid var(--border)', overflow: 'hidden',
        }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)' }}>
                {['Session ID', 'Channel', 'Created', 'Updated'].map(h => (
                  <th key={h} style={{
                    padding: '10px 16px', textAlign: 'left', fontWeight: 600,
                    color: 'var(--text-secondary)', fontSize: 12, textTransform: 'uppercase',
                    letterSpacing: '0.05em',
                  }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {sessions.map(s => (
                <tr key={s.id} style={{ borderBottom: '1px solid var(--border)' }}>
                  <td style={{ padding: '10px 16px' }}>
                    <a href={`/sessions/${s.id}`} style={{ fontFamily: 'var(--font-mono)' }}>
                      {s.id.slice(0, 12)}...
                    </a>
                  </td>
                  <td style={{ padding: '10px 16px', color: 'var(--text-secondary)' }}>
                    {s.channel || '\u2014'}
                  </td>
                  <td style={{ padding: '10px 16px', color: 'var(--text-secondary)' }}>
                    {formatTime(s.created_at)}
                  </td>
                  <td style={{ padding: '10px 16px', color: 'var(--text-secondary)' }}>
                    {formatTime(s.updated_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Layout>
  )
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toLocaleString()
  } catch {
    return iso
  }
}
