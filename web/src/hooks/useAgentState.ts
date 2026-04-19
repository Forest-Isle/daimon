import { useReducer, useEffect, useCallback } from 'preact/hooks'
import type { DashboardEvent, StateSnapshot, ToolEvent, PhaseEvent } from '../lib/types'
import { fetchAgentState } from '../lib/api'
import { useWebSocket } from './useWebSocket'

interface AgentState {
  status: 'idle' | 'busy'
  activeSessions: StateSnapshot['active_sessions']
  recentTools: ToolEvent[]
  phaseHistory: PhaseEvent[]
  connected: boolean
  totalSessionsToday: number
  uptimeSeconds: number
}

type Action =
  | { type: 'snapshot'; data: StateSnapshot }
  | { type: 'event'; data: DashboardEvent }
  | { type: 'connection'; connected: boolean }

const MAX_TOOLS = 100

function reducer(state: AgentState, action: Action): AgentState {
  switch (action.type) {
    case 'snapshot':
      return {
        ...state,
        status: action.data.status,
        activeSessions: action.data.active_sessions || [],
        totalSessionsToday: action.data.total_sessions_today,
        uptimeSeconds: action.data.uptime_seconds,
      }
    case 'connection':
      return { ...state, connected: action.connected }
    case 'event': {
      const ev = action.data
      let { activeSessions, recentTools, phaseHistory, status, totalSessionsToday } = state

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
            timestamp: ev.timestamp,
            tool_name: ev.data.tool_name as string,
            running: true,
          }, ...recentTools].slice(0, MAX_TOOLS)
          break
        case 'tool.end':
          recentTools = recentTools.map(t =>
            t.tool_name === ev.data.tool_name && t.running
              ? { ...t, running: false, succeeded: ev.data.succeeded as boolean, duration_ms: ev.data.duration_ms as number }
              : t
          )
          break
        case 'session.end':
          status = 'idle'
          phaseHistory = []
          totalSessionsToday++
          break
      }
      return { ...state, activeSessions, recentTools, phaseHistory, status, totalSessionsToday }
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
  totalSessionsToday: 0,
  uptimeSeconds: 0,
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
      .catch(() => {})
  }, [])

  return { ...state, wsStatus }
}
