import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

interface SessionState {
  id: string
  phase: string
  tool: string
  replans: number
}

interface MetricsState {
  sessions: SessionState[]
  activeSessions: number
  totalTokens: number
  cacheHits: number
}

export const useAgentStore = defineStore('agent', () => {
  const connected = ref(false)
  const metrics = ref<MetricsState>({
    sessions: [],
    activeSessions: 0,
    totalTokens: 0,
    cacheHits: 0
  })
  let ws: WebSocket | null = null

  function connect() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    ws = new WebSocket(`${protocol}//${location.host}/ws`)
    ws.onopen = () => { connected.value = true }
    ws.onclose = () => { connected.value = false; setTimeout(connect, 3000) }
    ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data)
        handleEvent(event)
      } catch {}
    }
  }

  function handleEvent(event: any) {
    switch (event.type) {
      case 'session_start':
        metrics.value.activeSessions++
        metrics.value.sessions.push({ id: event.session_id, phase: 'PERCEIVE', tool: '', replans: 0 })
        break
      case 'phase_start':
        const s = metrics.value.sessions.find(s => s.id === event.session_id)
        if (s) s.phase = event.phase
        break
      case 'tool_start':
        const t = metrics.value.sessions.find(s => s.id === event.session_id)
        if (t) t.tool = event.tool_name
        break
      case 'session_end':
        metrics.value.activeSessions = Math.max(0, metrics.value.activeSessions - 1)
        break
      case 'metrics_update':
        metrics.value.totalTokens = (event.input_tokens || 0) + (event.output_tokens || 0)
        metrics.value.cacheHits = event.cache_read || 0
        break
    }
  }

  return { connected, metrics, connect }
})
