export type EventType =
  | 'phase.start' | 'phase.end'
  | 'tool.start' | 'tool.end'
  | 'plan.generated' | 'replan.start'
  | 'task.update'
  | 'session.start' | 'session.end'
  | 'agent.idle'

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
  total_sessions_today: number
}

export interface ToolEvent {
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
