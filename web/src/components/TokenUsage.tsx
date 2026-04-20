import type { MetricsState } from '../hooks/useAgentState'

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
  return String(n)
}

function utilizationColor(pct: number): string {
  if (pct >= 80) return 'var(--error)'
  if (pct >= 60) return 'var(--warning)'
  return 'var(--success)'
}

export function TokenUsage({ metrics }: { metrics: MetricsState | null }) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>Token Usage</h3>

      {!metrics ? (
        <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>Waiting for metrics...</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {/* Model + Provider */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--text-primary)' }}>{metrics.model}</span>
            <span style={{
              padding: '2px 8px', borderRadius: 10, fontSize: 11, fontWeight: 600,
              background: 'rgba(88,166,255,0.15)', color: 'var(--accent)',
            }}>
              {metrics.provider}
            </span>
          </div>

          {/* Iteration */}
          <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>
            Iteration <strong style={{ color: 'var(--text-primary)' }}>{metrics.iteration}</strong> / {metrics.maxIter}
          </div>

          {/* Utilization bar */}
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, marginBottom: 4 }}>
              <span style={{ color: 'var(--text-secondary)' }}>Context Utilization</span>
              <span style={{ color: utilizationColor(metrics.utilization * 100), fontWeight: 600 }}>
                {(metrics.utilization * 100).toFixed(1)}%
              </span>
            </div>
            <div style={{ height: 6, borderRadius: 3, background: 'var(--bg-tertiary)', overflow: 'hidden' }}>
              <div style={{
                height: '100%', borderRadius: 3, transition: 'width 0.3s',
                width: `${Math.min(metrics.utilization * 100, 100)}%`,
                background: utilizationColor(metrics.utilization * 100),
              }} />
            </div>
          </div>

          {/* Token counts */}
          <div style={{ display: 'flex', gap: 20, fontSize: 13 }}>
            <div>
              <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 2 }}>Input</div>
              <div style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{fmtTokens(metrics.inputTokens)}</div>
            </div>
            <div>
              <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 2 }}>Output</div>
              <div style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{fmtTokens(metrics.outputTokens)}</div>
            </div>
            <div>
              <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 2 }}>Total</div>
              <div style={{ fontWeight: 600, color: 'var(--text-primary)' }}>
                {fmtTokens(metrics.inputTokens + metrics.outputTokens)}
              </div>
            </div>
          </div>

          {/* Cache stats */}
          {(metrics.cacheCreate > 0 || metrics.cacheRead > 0) && (
            <div style={{ display: 'flex', gap: 20, fontSize: 13 }}>
              <div>
                <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 2 }}>Cache Created</div>
                <div style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{fmtTokens(metrics.cacheCreate)}</div>
              </div>
              <div>
                <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 2 }}>Cache Read</div>
                <div style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{fmtTokens(metrics.cacheRead)}</div>
              </div>
              <div>
                <div style={{ color: 'var(--text-secondary)', fontSize: 11, marginBottom: 2 }}>Hit Rate</div>
                <div style={{ fontWeight: 600, color: 'var(--success)' }}>
                  {(metrics.cacheCreate + metrics.cacheRead) > 0
                    ? ((metrics.cacheRead / (metrics.cacheCreate + metrics.cacheRead)) * 100).toFixed(1)
                    : '0.0'}%
                </div>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
