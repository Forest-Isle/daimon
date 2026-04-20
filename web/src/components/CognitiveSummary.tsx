interface Props {
  planInfo: { taskCount: number; complexity: string } | null
  observationResult: { passed: number; failed: number; total: number; progress: number } | null
  replanCount: number
}

export function CognitiveSummary({ planInfo, observationResult, replanCount }: Props) {
  const hasContent = planInfo || observationResult || replanCount > 0
  if (!hasContent) return null

  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8, marginBottom: 16,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', margin: 0 }}>
          Cognitive Summary
        </h3>
        {replanCount > 0 && (
          <span style={{
            padding: '2px 8px', borderRadius: 12, fontSize: 11, fontWeight: 600,
            background: 'rgba(210,153,34,0.15)', color: 'var(--warning)',
          }}>
            {replanCount} replan{replanCount > 1 ? 's' : ''}
          </span>
        )}
      </div>

      <div style={{ display: 'flex', gap: 24, flexWrap: 'wrap' }}>
        {/* Plan info */}
        <div style={{ flex: 1, minWidth: 180 }}>
          <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginBottom: 4 }}>Plan</div>
          {planInfo ? (
            <div style={{ fontSize: 14 }}>
              <strong>{planInfo.taskCount}</strong> subtask{planInfo.taskCount !== 1 ? 's' : ''}
              <span style={{
                marginLeft: 8, padding: '2px 8px', borderRadius: 8, fontSize: 11,
                background: 'var(--bg-tertiary)', color: 'var(--text-secondary)',
              }}>
                {planInfo.complexity}
              </span>
            </div>
          ) : (
            <div style={{ fontSize: 13, color: 'var(--text-secondary)', fontStyle: 'italic' }}>
              No plan yet
            </div>
          )}
        </div>

        {/* Observation results */}
        <div style={{ flex: 1, minWidth: 220 }}>
          <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginBottom: 4 }}>Observations</div>
          {observationResult ? (
            <div>
              <div style={{ fontSize: 14, marginBottom: 6 }}>
                <span style={{ color: 'var(--success)' }}>{observationResult.passed} passed</span>
                {observationResult.failed > 0 && (
                  <span style={{ color: 'var(--error)', marginLeft: 8 }}>
                    {observationResult.failed} failed
                  </span>
                )}
                <span style={{ color: 'var(--text-secondary)', marginLeft: 8, fontSize: 12 }}>
                  / {observationResult.total} total
                </span>
              </div>
              <div style={{
                height: 6, borderRadius: 3, background: 'var(--bg-tertiary)', overflow: 'hidden',
              }}>
                <div style={{
                  height: '100%', borderRadius: 3,
                  width: `${Math.round(observationResult.progress * 100)}%`,
                  background: observationResult.failed > 0 ? 'var(--warning)' : 'var(--success)',
                  transition: 'width 0.3s ease',
                }} />
              </div>
              <div style={{ fontSize: 11, color: 'var(--text-secondary)', marginTop: 2 }}>
                {Math.round(observationResult.progress * 100)}% progress
              </div>
            </div>
          ) : (
            <div style={{ fontSize: 13, color: 'var(--text-secondary)', fontStyle: 'italic' }}>
              Awaiting results
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
