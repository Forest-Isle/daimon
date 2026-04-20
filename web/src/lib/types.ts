export type EventType =
  | 'phase.start' | 'phase.end'
  | 'tool.start' | 'tool.end'
  | 'plan.generated' | 'replan.start'
  | 'task.update'
  | 'session.start' | 'session.end'
  | 'agent.idle'
  | 'subagent.spawn' | 'subagent.complete'
  | 'context.compress'

export interface DashboardEvent {
  type: EventType
  timestamp: string
  session_id?: string
  data: Record<string, unknown>
}

export interface SessionState {
  session_id: string
  channel?: string
  current_phase: string
  current_tool?: string
  phase_started_at?: string
  tools_executed: number
  replan_count: number
}

export interface StateSnapshot {
  status: 'idle' | 'busy'
  active_sessions: SessionState[]
  uptime_seconds: number
  total_sessions: number
  active_subagents?: SubAgentEvent[]
  compression_events?: number
}

export interface SubAgentEvent {
  session_id: string
  parent_session_id: string
  agent_name: string
  task?: string
  started_at: string
  running: boolean
  succeeded?: boolean
  duration_ms?: number
}

export interface ToolEvent {
  id: number
  timestamp: string
  tool_name: string
  succeeded?: boolean
  duration_ms?: number
  running: boolean
}

export interface PhaseEvent {
  phase: string
  started_at: string
  duration_ms?: number
  running: boolean
}

// --- REST API response types (from SQLite) ---

export interface SessionInfo {
  id: string
  channel: string
  channel_id: string
  created_at: string
  updated_at: string
}

export interface MessageInfo {
  id: string
  role: string
  content: string
  tool_name?: string
  created_at: string
}

export interface ToolLogEntry {
  id: string
  tool_name: string
  input: string
  output: string
  status: string
  duration_ms: number
  created_at: string
}

export interface MetricValue {
  value: number
  samples: number
}

export interface ReplanEfficiency {
  with_replan: MetricValue
  without_replan: MetricValue
}

export interface HealthReport {
  timestamp: string
  uptime_ms: number
  total_episodes: number
  total_reflections: number
  strategy_version: number
  assertion_pass_rate: MetricValue
  replan_rate: MetricValue
  replan_efficiency: ReplanEfficiency
  avg_confidence: MetricValue
  tool_reliability: Record<string, MetricValue>
  complexity_success: Record<string, MetricValue>
}
