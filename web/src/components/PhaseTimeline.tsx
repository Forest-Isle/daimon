import type { PhaseEvent } from '../lib/types'

const PHASE_ORDER = ['PERCEIVE', 'PLAN', 'ACT', 'OBSERVE', 'REFLECT']

export function PhaseTimeline({ phases }: { phases: PhaseEvent[] }) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Phase Timeline</h3>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        {PHASE_ORDER.map((name, i) => {
          const phase = phases.find(p => p.phase === name)
          const isActive = phase?.running
          const isDone = phase && !phase.running
          return (
            <span key={name}>
              {i > 0 && <span style={{ color: 'var(--text-secondary)', marginRight: 8 }}>{'\u2192'}</span>}
              <span style={{
                padding: '4px 12px', borderRadius: 6, fontSize: 13, fontFamily: 'var(--font-mono)',
                background: isActive ? 'rgba(88,166,255,0.15)' : isDone ? 'var(--bg-tertiary)' : 'transparent',
                color: isActive ? 'var(--accent)' : isDone ? 'var(--text-primary)' : 'var(--text-secondary)',
                border: isActive ? '1px solid var(--accent)' : '1px solid transparent',
              }}>
                {isActive && '\u25B8 '}{name}
                {isDone && phase.duration_ms != null && (
                  <span style={{ marginLeft: 6, fontSize: 11, color: 'var(--text-secondary)' }}>
                    {phase.duration_ms}ms
                  </span>
                )}
                {isActive && <span style={{ marginLeft: 6, fontSize: 11 }}>running\u2026</span>}
              </span>
            </span>
          )
        })}
      </div>
    </div>
  )
}
