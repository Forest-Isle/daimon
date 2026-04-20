import { useAgentState } from '../hooks/useAgentState'
import { Layout } from '../components/Layout'
import { AgentStatus } from '../components/AgentStatus'
import { TokenUsage } from '../components/TokenUsage'
import { PhaseTimeline } from '../components/PhaseTimeline'
import { ToolCallFeed } from '../components/ToolCallFeed'
import { SessionList } from '../components/SessionList'

export function Overview() {
  const state = useAgentState()

  return (
    <Layout connected={state.connected}>
      {state.error && (
        <div style={{
          padding: '12px 20px', marginBottom: 16,
          background: 'rgba(248,81,73,0.1)', border: '1px solid var(--error)',
          borderRadius: 8, fontSize: 13, color: 'var(--error)',
        }}>
          Failed to load agent state: {state.error}
        </div>
      )}
      <AgentStatus status={state.status} sessions={state.activeSessions} replanCount={state.replanCount} />
      <TokenUsage metrics={state.metrics} />
      <PhaseTimeline phases={state.phaseHistory} />
      <ToolCallFeed tools={state.recentTools} />
      <SessionList sessions={state.activeSessions} total={state.totalSessions} />
      <div style={{
        marginTop: 24, padding: '12px 20px', background: 'var(--bg-secondary)',
        borderRadius: 8, fontSize: 13, color: 'var(--text-secondary)',
        display: 'flex', gap: 24,
      }}>
        <span>Uptime: {Math.floor(state.uptimeSeconds / 3600)}h {Math.floor((state.uptimeSeconds % 3600) / 60)}m</span>
        <span>WebSocket: {state.wsStatus}</span>
      </div>
    </Layout>
  )
}
