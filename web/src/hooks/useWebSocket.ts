import { useEffect, useRef, useState, useCallback } from 'preact/hooks'
import type { DashboardEvent } from '../lib/types'

type ConnectionStatus = 'connected' | 'reconnecting' | 'disconnected'

export function useWebSocket(onEvent: (ev: DashboardEvent) => void) {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected')
  const wsRef = useRef<WebSocket | null>(null)
  const retriesRef = useRef(0)

  const connect = useCallback(() => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${location.host}/ws`
    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      setStatus('connected')
      retriesRef.current = 0
    }

    ws.onmessage = (msg) => {
      try {
        const ev: DashboardEvent = JSON.parse(msg.data)
        onEvent(ev)
      } catch { /* ignore malformed */ }
    }

    ws.onclose = () => {
      setStatus('reconnecting')
      const delay = Math.min(1000 * Math.pow(2, retriesRef.current), 30000)
      retriesRef.current++
      setTimeout(connect, delay)
    }

    ws.onerror = () => ws.close()
  }, [onEvent])

  useEffect(() => {
    connect()
    return () => { wsRef.current?.close() }
  }, [connect])

  return status
}
