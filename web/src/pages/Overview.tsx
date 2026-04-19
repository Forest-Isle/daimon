import { useAgentState } from '../hooks/useAgentState'
import { Layout } from '../components/Layout'
import { AgentStatus } from '../components/AgentStatus'
import { PhaseTimeline } from '../components/PhaseTimeline'
import { ToolCallFeed } from '../components/ToolCallFeed'
import { SessionList } from '../components/SessionList'

export function Overview() {
  const state = useAgentState()

  return (
    <Layout connected={state.connected}>
      <AgentStatus status={state.status} sessions={state.activeSessions} />
      <PhaseTimeline phases={state.phaseHistory} />
      <ToolCallFeed tools={state.recentTools} />
      <SessionList sessions={state.activeSessions} total={state.totalSessionsToday} />
      <div style={{
        marginTop: 24, padding: '12px 20px', background: 'var(--bg-secondary)',
        borderRadius: 8, fontSize: 13, color: 'var(--text-secondary)',
        display: 'flex', gap: 24,
      }}>
        <span>Uptime: {Math.floor(state.uptimeSeconds / 3600)}h {Math.floor((state.uptimeSeconds % 3600) / 60)}m</span>
      </div>
    </Layout>
  )
}
