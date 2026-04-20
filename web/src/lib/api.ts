import { authHeaders } from './auth'
import type { StateSnapshot, SessionInfo, MessageInfo, ToolLogEntry, HealthReport } from './types'

const BASE = ''

async function jsonFetch<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: authHeaders() })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }
  const ct = res.headers.get('Content-Type') ?? ''
  if (!ct.includes('application/json')) {
    throw new Error('Expected JSON response but got ' + (ct || 'unknown content type'))
  }
  return res.json()
}

export function fetchAgentState(): Promise<StateSnapshot> {
  return jsonFetch(`${BASE}/api/agent/state`)
}

export function fetchSessions(): Promise<SessionInfo[]> {
  return jsonFetch(`${BASE}/api/sessions`)
}

export function fetchSessionMessages(id: string): Promise<MessageInfo[]> {
  return jsonFetch(`${BASE}/api/sessions/${id}/messages`)
}

export function fetchSessionTools(id: string): Promise<ToolLogEntry[]> {
  return jsonFetch(`${BASE}/api/sessions/${id}/tools`)
}

export function fetchMetricsHealth(): Promise<HealthReport> {
  return jsonFetch(`${BASE}/api/metrics/health`)
}
