<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'

const generation = ref(3)
const bestScore = ref(0.847)
const episodes = ref(1247)
const population = ref(20)

const history = ref([
  { gen: 1, score: 0.72, sr: 0.68 },
  { gen: 2, score: 0.79, sr: 0.74 },
  { gen: 3, score: 0.85, sr: 0.81 },
])

const strategies = ref([
  { name: 'Replan Threshold', value: 0.52, prev: 0.60, dir: 'down', reason: 'Lower threshold increased replan efficiency' },
  { name: 'MCTS Depth', value: 45, prev: 35, dir: 'up', reason: 'Deeper search improved complex task planning' },
  { name: 'Tool: bash', value: 0.85, prev: 0.70, dir: 'up', reason: 'High success rate → increased priority' },
  { name: 'Tool: http', value: 0.45, prev: 0.55, dir: 'down', reason: 'Lower success rate → reduced priority' },
])

const components = ref([
  { name: 'speculative', status: 'enabled', contribution: '+3.2%' },
  { name: 'tool_cache', status: 'enabled', contribution: '+5.1%' },
  { name: 'reranker', status: 'enabled', contribution: '+1.8%' },
  { name: 'mcts_planner', status: 'enabled', contribution: '+7.3%' },
  { name: 'prompt_v3', status: 'enabled', contribution: '+2.4%' },
])

const chartWidth = 400; const chartHeight = 200
const chartPoints = computed(() => {
  return history.value.map((h, i) => {
    const x = (i / (history.value.length - 1)) * chartWidth
    const y = chartHeight - (h.score * chartHeight)
    return `${x},${y}`
  }).join(' ')
})
</script>

<template>
  <div class="evolution-monitor">
    <div class="toolbar">
      <h1>Evolution Monitor</h1>
      <button class="btn">🔄 Run Generation</button>
    </div>
    <div class="grid">
      <div class="card"><div class="label">Generation</div><div class="value">{{ generation }}</div></div>
      <div class="card"><div class="label">Best Score</div><div class="value accent">{{ (bestScore * 100).toFixed(1) }}%</div></div>
      <div class="card"><div class="label">Episodes</div><div class="value">{{ episodes }}</div></div>
      <div class="card"><div class="label">Population</div><div class="value">{{ population }}</div></div>
    </div>
    <div class="charts">
      <div class="card chart-card">
        <div class="label">Fitness Over Generations</div>
        <svg :width="chartWidth" :height="chartHeight" class="chart-svg">
          <polyline :points="chartPoints" fill="none" stroke="#7c3aed" stroke-width="2" />
          <circle v-for="(h, i) in history" :key="i"
            :cx="(i / (history.length - 1)) * chartWidth"
            :cy="chartHeight - (h.score * chartHeight)" r="4" fill="#a78bfa" />
        </svg>
      </div>
      <div class="card chart-card">
        <div class="label">Strategy Parameters</div>
        <div class="strategy-list">
          <div v-for="s in strategies" :key="s.name" class="strategy-row">
            <span class="strategy-name">{{ s.name }}</span>
            <span class="strategy-value">{{ typeof s.value === 'number' && s.value < 1 ? (s.value*100).toFixed(0)+'%' : s.value }}</span>
            <span :class="['strategy-dir', s.dir]">{{ s.dir === 'up' ? '↑' : '↓' }}</span>
            <span class="strategy-reason">{{ s.reason }}</span>
          </div>
        </div>
      </div>
    </div>
    <div class="card full-width">
      <div class="label">Ablation Study — Component Contributions</div>
      <div class="ablation-list">
        <div v-for="c in components" :key="c.name" class="ablation-row">
          <span class="comp-name">{{ c.name }}</span>
          <span class="comp-status" :class="c.status">{{ c.status }}</span>
          <span class="comp-contribution" :class="{ positive: c.contribution.startsWith('+') }">{{ c.contribution }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.evolution-monitor { max-width: 900px; }
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 24px; }
.toolbar h1 { font-size: 1.3em; color: #c4b5fd; }
.btn { background: #1a1a2e; border: 1px solid #333; color: #999; padding: 6px 14px; border-radius: 6px; cursor: pointer; font-size: 0.8em; }
.grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 24px; }
.card { background: #111118; border: 1px solid #222; border-radius: 12px; padding: 20px; }
.card.full-width { grid-column: 1 / -1; margin-top: 16px; }
.label { color: #666; font-size: 0.75em; text-transform: uppercase; letter-spacing: 0.5px; margin-bottom: 8px; }
.value { font-size: 1.5em; font-weight: 700; color: #e0e0e0; }
.value.accent { color: #a78bfa; }
.charts { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-bottom: 16px; }
.chart-card { min-height: 220px; }
.chart-svg { width: 100%; }
.strategy-list { display: flex; flex-direction: column; gap: 8px; font-size: 0.8em; }
.strategy-row { display: grid; grid-template-columns: 1fr auto auto; gap: 8px; align-items: center; padding: 6px 0; border-bottom: 1px solid #1a1a2e; }
.strategy-name { color: #ccc; }
.strategy-value { color: #a78bfa; font-family: monospace; }
.strategy-dir { font-weight: bold; }
.strategy-dir.up { color: #22c55e; }
.strategy-dir.down { color: #f59e0b; }
.strategy-reason { grid-column: 1 / -1; color: #555; font-size: 0.85em; }
.ablation-list { display: flex; flex-direction: column; gap: 6px; }
.ablation-row { display: flex; align-items: center; gap: 12px; padding: 8px 12px; background: #0a0a0f; border-radius: 6px; font-size: 0.85em; }
.comp-name { flex: 1; color: #ccc; }
.comp-status { padding: 2px 8px; border-radius: 4px; font-size: 0.8em; }
.comp-status.enabled { background: #22c55e22; color: #22c55e; }
.comp-contribution { font-family: monospace; }
.comp-contribution.positive { color: #22c55e; }
</style>
