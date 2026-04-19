const BASE = ''

export async function fetchAgentState(): Promise<import('./types').StateSnapshot> {
  const res = await fetch(`${BASE}/api/agent/state`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchSessions() {
  const res = await fetch(`${BASE}/api/sessions`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}
