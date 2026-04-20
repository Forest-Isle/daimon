import { useState, useEffect } from 'preact/hooks'
import { Layout } from '../components/Layout'
import { useAgentState } from '../hooks/useAgentState'
import { fetchSessionMessages, fetchSessionTools } from '../lib/api'
import type { MessageInfo, ToolLogEntry } from '../lib/types'

export function SessionDetail({ id }: { id: string }) {
  const { connected } = useAgentState()
  const [tab, setTab] = useState<'messages' | 'tools'>('messages')
  const [messages, setMessages] = useState<MessageInfo[]>([])
  const [tools, setTools] = useState<ToolLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    setError(null)
    Promise.all([
      fetchSessionMessages(id).catch(() => [] as MessageInfo[]),
      fetchSessionTools(id).catch(() => [] as ToolLogEntry[]),
    ]).then(([msgs, tls]) => {
      setMessages(msgs ?? [])
      setTools(tls ?? [])
      setLoading(false)
    }).catch(err => {
      setError(err.message)
      setLoading(false)
    })
  }, [id])

  return (
    <Layout connected={connected}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
        <a href="/sessions" style={{ color: 'var(--text-secondary)', fontSize: 14 }}>{'\u2190'} Sessions</a>
        <h2 style={{ fontSize: 18 }}>
          Session <code style={{ fontSize: 14 }}>{id.slice(0, 12)}...</code>
        </h2>
      </div>

      {error && (
        <div style={{
          padding: '12px 20px', marginBottom: 16,
          background: 'rgba(248,81,73,0.1)', border: '1px solid var(--error)',
          borderRadius: 8, fontSize: 13, color: 'var(--error)',
        }}>
          {error}
        </div>
      )}

      <div style={{ display: 'flex', gap: 4, marginBottom: 16 }}>
        {(['messages', 'tools'] as const).map(t => (
          <button key={t} onClick={() => setTab(t)} style={{
            padding: '8px 16px', borderRadius: 6, border: 'none', cursor: 'pointer',
            fontSize: 13, fontWeight: 600, transition: 'background 0.15s',
            background: tab === t ? 'var(--bg-tertiary)' : 'transparent',
            color: tab === t ? 'var(--text-primary)' : 'var(--text-secondary)',
          }}>
            {t === 'messages' ? `Messages (${messages.length})` : `Tools (${tools.length})`}
          </button>
        ))}
      </div>

      {loading ? (
        <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>Loading...</div>
      ) : tab === 'messages' ? (
        <MessageList messages={messages} />
      ) : (
        <ToolLogList tools={tools} />
      )}
    </Layout>
  )
}

function MessageList({ messages }: { messages: MessageInfo[] }) {
  if (messages.length === 0) {
    return <Empty text="No messages in this session" />
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {messages.map(m => (
        <div key={m.id} style={{
          padding: '12px 16px', borderRadius: 8,
          background: m.role === 'user' ? 'rgba(88,166,255,0.06)' : m.role === 'assistant' ? 'var(--bg-secondary)' : 'rgba(139,148,158,0.06)',
          border: '1px solid var(--border)',
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
            <span style={{
              fontSize: 12, fontWeight: 600, textTransform: 'uppercase',
              color: m.role === 'user' ? 'var(--accent)' : m.role === 'assistant' ? 'var(--success)' : 'var(--text-secondary)',
            }}>
              {m.role}
              {m.tool_name && <span style={{ fontWeight: 400, marginLeft: 8, fontFamily: 'var(--font-mono)' }}>{m.tool_name}</span>}
            </span>
            <span style={{ fontSize: 11, color: 'var(--text-secondary)' }}>
              {formatTime(m.created_at)}
            </span>
          </div>
          <pre style={{
            fontSize: 13, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            lineHeight: 1.5, margin: 0, maxHeight: 300, overflow: 'auto',
            color: 'var(--text-primary)',
          }}>
            {m.content}
          </pre>
        </div>
      ))}
    </div>
  )
}

function ToolLogList({ tools }: { tools: ToolLogEntry[] }) {
  const [expanded, setExpanded] = useState<string | null>(null)

  if (tools.length === 0) {
    return <Empty text="No tool calls in this session" />
  }

  return (
    <div style={{
      background: 'var(--bg-secondary)', borderRadius: 8,
      border: '1px solid var(--border)', overflow: 'hidden',
    }}>
      {tools.map(t => {
        const isExpanded = expanded === t.id
        const isSuccess = t.status === 'success'
        return (
          <div key={t.id} style={{ borderBottom: '1px solid var(--border)' }}>
            <div
              onClick={() => setExpanded(isExpanded ? null : t.id)}
              style={{
                display: 'flex', alignItems: 'center', gap: 12,
                padding: '10px 16px', cursor: 'pointer', fontSize: 13,
              }}
            >
              <span style={{ color: isSuccess ? 'var(--success)' : 'var(--error)', fontWeight: 600 }}>
                {isSuccess ? '\u2713' : '\u2717'}
              </span>
              <code style={{ minWidth: 120 }}>{t.tool_name}</code>
              <span style={{ color: 'var(--text-secondary)' }}>{t.duration_ms}ms</span>
              <span style={{ color: 'var(--text-secondary)', marginLeft: 'auto', fontSize: 11 }}>
                {formatTime(t.created_at)}
              </span>
              <span style={{ color: 'var(--text-secondary)', fontSize: 11 }}>
                {isExpanded ? '\u25B2' : '\u25BC'}
              </span>
            </div>
            {isExpanded && (
              <div style={{ padding: '0 16px 12px', fontSize: 12 }}>
                <div style={{ marginBottom: 8 }}>
                  <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 4 }}>Input</div>
                  <pre style={{
                    background: 'var(--bg-primary)', padding: 10, borderRadius: 6,
                    whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 200, overflow: 'auto',
                    margin: 0,
                  }}>{t.input || '\u2014'}</pre>
                </div>
                <div>
                  <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 4 }}>Output</div>
                  <pre style={{
                    background: 'var(--bg-primary)', padding: 10, borderRadius: 6,
                    whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: 200, overflow: 'auto',
                    margin: 0,
                  }}>{t.output || '\u2014'}</pre>
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function Empty({ text }: { text: string }) {
  return (
    <div style={{
      padding: 40, textAlign: 'center', color: 'var(--text-secondary)',
      background: 'var(--bg-secondary)', borderRadius: 8, border: '1px solid var(--border)',
    }}>
      {text}
    </div>
  )
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString()
  } catch {
    return iso
  }
}
