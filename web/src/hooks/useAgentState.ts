import { useReducer, useEffect, useCallback } from 'preact/hooks'
import type { DashboardEvent, StateSnapshot, ToolEvent, PhaseEvent } from '../lib/types'
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
  connected: boolean
  totalSessions: number
  uptimeSeconds: number
  replanCount: number
  metrics: MetricsState | null
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
      let { activeSessions, recentTools, phaseHistory, status, totalSessions, replanCount } = state

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
          status = 'busy'
          break
        case 'plan.generated':
          status = 'busy'
          break
        case 'session.start':
          status = 'busy'
          phaseHistory = []
          replanCount = 0
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
          status = 'idle'
          phaseHistory = []
          replanCount = 0
          totalSessions++
          break
      }
      return { ...state, activeSessions, recentTools, phaseHistory, status, totalSessions, replanCount }
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
  connected: false,
  totalSessions: 0,
  uptimeSeconds: 0,
  replanCount: 0,
  metrics: null,
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
