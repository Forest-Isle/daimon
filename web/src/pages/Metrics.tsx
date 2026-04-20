import { useState, useEffect } from 'preact/hooks'
import { Layout } from '../components/Layout'
import { useAgentState } from '../hooks/useAgentState'
import { fetchMetricsHealth } from '../lib/api'
import type { HealthReport, MetricValue } from '../lib/types'

export function Metrics() {
  const { connected } = useAgentState()
  const [report, setReport] = useState<HealthReport | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = () => {
    setLoading(true)
    fetchMetricsHealth()
      .then(data => { setReport(data); setError(null); setLoading(false) })
      .catch(err => { setError(err.message); setLoading(false) })
  }

  useEffect(load, [])

  return (
    <Layout connected={connected}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ fontSize: 18 }}>Cognitive Health</h2>
        <button onClick={load} style={{
          padding: '6px 14px', borderRadius: 6, border: '1px solid var(--border)',
          background: 'var(--bg-tertiary)', color: 'var(--text-primary)',
          cursor: 'pointer', fontSize: 13,
        }}>
          Refresh
        </button>
      </div>

      {error && (
        <div style={{
          padding: '12px 20px', marginBottom: 16,
          background: 'rgba(248,81,73,0.1)', border: '1px solid var(--error)',
          borderRadius: 8, fontSize: 13, color: 'var(--error)',
        }}>
          {error.includes('not enabled') || error.includes('503')
            ? 'Cognitive metrics collector is not enabled. Enable the evolution engine in config to collect metrics.'
            : error}
        </div>
      )}

      {loading ? (
        <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>Loading...</div>
      ) : report ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {/* Summary cards */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <StatCard label="Total Episodes" value={String(report.total_episodes)} />
            <StatCard label="Total Reflections" value={String(report.total_reflections)} />
            <StatCard label="Strategy Version" value={`v${report.strategy_version}`} />
            <StatCard label="Uptime" value={formatUptime(report.uptime_ms)} />
          </div>

          {/* Core metrics */}
          <Card title="Core Metrics">
            <MetricRow label="Assertion Pass Rate" metric={report.assertion_pass_rate} isPercent />
            <MetricRow label="Replan Rate" metric={report.replan_rate} isPercent />
            <MetricRow label="Avg Confidence" metric={report.avg_confidence} isPercent />
          </Card>

          {/* Replan efficiency */}
          <Card title="Replan Efficiency">
            <MetricRow label="With Replan" metric={report.replan_efficiency.with_replan} isPercent />
            <MetricRow label="Without Replan" metric={report.replan_efficiency.without_replan} isPercent />
          </Card>

          {/* Tool reliability */}
          {report.tool_reliability && Object.keys(report.tool_reliability).length > 0 && (
            <Card title="Tool Reliability">
              {Object.entries(report.tool_reliability)
                .sort(([, a], [, b]) => b.samples - a.samples)
                .map(([name, metric]) => (
                  <MetricRow key={name} label={name} metric={metric} isPercent />
                ))
              }
            </Card>
          )}

          {/* Complexity success */}
          {report.complexity_success && Object.keys(report.complexity_success).length > 0 && (
            <Card title="Complexity Success">
              {Object.entries(report.complexity_success).map(([level, metric]) => (
                <MetricRow key={level} label={level} metric={metric} isPercent />
              ))}
            </Card>
          )}
        </div>
      ) : null}
    </Layout>
  )
}

function Card({ title, children }: { title: string; children: preact.ComponentChildren }) {
  return (
    <div style={{
      padding: 20, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8,
    }}>
      <h3 style={{ fontSize: 14, color: 'var(--text-secondary)', marginBottom: 12 }}>{title}</h3>
      {children}
    </div>
  )
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div style={{
      padding: 16, background: 'var(--bg-secondary)',
      border: '1px solid var(--border)', borderRadius: 8,
    }}>
      <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginBottom: 4 }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600 }}>{value}</div>
    </div>
  )
}

function MetricRow({ label, metric, isPercent }: { label: string; metric: MetricValue; isPercent?: boolean }) {
  const val = isPercent ? `${(metric.value * 100).toFixed(1)}%` : metric.value.toFixed(2)
  const pct = isPercent ? metric.value * 100 : 0

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12,
      padding: '8px 0', borderBottom: '1px solid var(--border)', fontSize: 13,
    }}>
      <span style={{ minWidth: 140, color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)' }}>
        {label}
      </span>
      {isPercent && (
        <div style={{ flex: 1, height: 6, background: 'var(--bg-primary)', borderRadius: 3, overflow: 'hidden' }}>
          <div style={{
            height: '100%', borderRadius: 3, transition: 'width 0.3s',
            width: `${Math.min(pct, 100)}%`,
            background: pct >= 80 ? 'var(--success)' : pct >= 50 ? 'var(--warning)' : 'var(--error)',
          }} />
        </div>
      )}
      <span style={{ fontWeight: 600, minWidth: 60, textAlign: 'right' }}>{val}</span>
      <span style={{ color: 'var(--text-secondary)', fontSize: 11 }}>({metric.samples} samples)</span>
    </div>
  )
}

function formatUptime(ms: number): string {
  const s = Math.floor(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}
