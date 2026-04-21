import { useReducer, useEffect, useCallback } from 'preact/hooks'
import type { DashboardEvent, StateSnapshot, ToolEvent, PhaseEvent, SubAgentEvent } from '../lib/types'
import { fetchAgentState } from '../lib/api'
import { useWebSocket } from './useWebSocket'

export interface MetricsState {
  iteration: number
  maxIter: number
  utilization: number
  inputTokens: number
  outputTokens: number
  cacheCreate: number
  cacheRead: number
  model: string
  provider: string
}

interface AgentState {
  status: 'idle' | 'busy'
  activeSessions: StateSnapshot['active_sessions']
  recentTools: ToolEvent[]
  phaseHistory: PhaseEvent[]
  subAgents: SubAgentEvent[]
  compressionCount: number
  connected: boolean
  totalSessions: number
  uptimeSeconds: number
  replanCount: number
  metrics: MetricsState | null
  planInfo: { taskCount: number; complexity: string } | null
  observationResult: { passed: number; failed: number; total: number; progress: number } | null
  error: string | null
}

type Action =
  | { type: 'snapshot'; data: StateSnapshot }
  | { type: 'event'; data: DashboardEvent }
  | { type: 'connection'; connected: boolean }
  | { type: 'error'; message: string }

const MAX_TOOLS = 100
let toolSeq = 0

function reducer(state: AgentState, action: Action): AgentState {
  switch (action.type) {
    case 'snapshot':
      return {
        ...state,
        status: action.data.status,
        activeSessions: action.data.active_sessions || [],
        totalSessions: action.data.total_sessions,
        uptimeSeconds: action.data.uptime_seconds,
        error: null,
      }
    case 'connection':
      return { ...state, connected: action.connected }
    case 'error':
      return { ...state, error: action.message }
    case 'event': {
      const ev = action.data
      let { activeSessions, recentTools, phaseHistory, subAgents, compressionCount, status, totalSessions, replanCount, planInfo, observationResult } = state

      switch (ev.type) {
        case 'phase.start':
          phaseHistory = [...phaseHistory, {
            phase: ev.data.phase as string,
            started_at: ev.timestamp,
            running: true,
          }]
          status = 'busy'
          break
        case 'phase.end':
          phaseHistory = phaseHistory.map(p =>
            p.phase === ev.data.phase && p.running
              ? { ...p, running: false, duration_ms: ev.data.duration_ms as number }
              : p
          )
          break
        case 'tool.start':
          recentTools = [{
            id: ++toolSeq,
            timestamp: ev.timestamp,
            tool_name: ev.data.tool_name as string,
            running: true,
          }, ...recentTools].slice(0, MAX_TOOLS)
          break
        case 'tool.end': {
          const toolName = ev.data.tool_name as string
          let matched = false
          recentTools = recentTools.map(t => {
            if (!matched && t.tool_name === toolName && t.running) {
              matched = true
              return { ...t, running: false, succeeded: ev.data.succeeded as boolean, duration_ms: ev.data.duration_ms as number }
            }
            return t
          })
          break
        }
        case 'replan.start':
          replanCount++
          planInfo = null
          observationResult = null
          status = 'busy'
          break
        case 'plan.generated':
          planInfo = {
            taskCount: ev.data.task_count as number,
            complexity: ev.data.complexity as string,
          }
          status = 'busy'
          break
        case 'observation.result':
          observationResult = {
            passed: ev.data.passed as number,
            failed: ev.data.failed as number,
            total: ev.data.total as number,
            progress: ev.data.overall_progress as number,
          }
          break
        case 'subagent.spawn':
          subAgents = [...subAgents, {
            session_id: ev.session_id || '',
            parent_session_id: ev.data.parent_session_id as string,
            agent_name: ev.data.agent_name as string,
            task: ev.data.task as string | undefined,
            started_at: ev.timestamp,
            running: true,
          }]
          break
        case 'subagent.complete': {
          const sid = ev.session_id || ''
          let matched = false
          subAgents = subAgents.map(sa => {
            if (!matched && sa.session_id === sid && sa.running) {
              matched = true
              return { ...sa, running: false, succeeded: ev.data.succeeded as boolean, duration_ms: ev.data.duration_ms as number }
            }
            return sa
          })
          break
        }
        case 'context.compress':
          compressionCount++
          break
        case 'session.start':
          status = 'busy'
          phaseHistory = []
          subAgents = []
          compressionCount = 0
          replanCount = 0
          planInfo = null
          observationResult = null
          break
        case 'metrics.update':
          return { ...state, metrics: {
            iteration: ev.data.iteration as number,
            maxIter: ev.data.max_iterations as number,
            utilization: ev.data.utilization as number,
            inputTokens: ev.data.input_tokens as number,
            outputTokens: ev.data.output_tokens as number,
            cacheCreate: ev.data.cache_create as number,
            cacheRead: ev.data.cache_read as number,
            model: ev.data.model as string,
            provider: ev.data.provider as string,
          }, status: 'busy' }
        case 'session.end':
          if (ev.data.source === 'evolution') break
          status = 'idle'
          phaseHistory = []
          subAgents = []
          compressionCount = 0
          replanCount = 0
          planInfo = null
          observationResult = null
          totalSessions++
          break
      }
      return { ...state, activeSessions, recentTools, phaseHistory, subAgents, compressionCount, status, totalSessions, replanCount, planInfo, observationResult }
    }
    default:
      return state
  }
}

const initialState: AgentState = {
  status: 'idle',
  activeSessions: [],
  recentTools: [],
  phaseHistory: [],
  subAgents: [],
  compressionCount: 0,
  connected: false,
  totalSessions: 0,
  uptimeSeconds: 0,
  replanCount: 0,
  metrics: null,
  planInfo: null,
  observationResult: null,
  error: null,
}

export function useAgentState() {
  const [state, dispatch] = useReducer(reducer, initialState)

  const onEvent = useCallback((ev: DashboardEvent) => {
    dispatch({ type: 'event', data: ev })
  }, [])

  const wsStatus = useWebSocket(onEvent)

  useEffect(() => {
    dispatch({ type: 'connection', connected: wsStatus === 'connected' })
  }, [wsStatus])

  useEffect(() => {
    fetchAgentState()
      .then(data => dispatch({ type: 'snapshot', data }))
      .catch(err => dispatch({ type: 'error', message: err.message }))
  }, [])

  return { ...state, wsStatus }
}
