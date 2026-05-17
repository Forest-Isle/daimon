<script setup lang="ts">
import { ref, computed } from 'vue'

interface MemoryNode {
  id: string; type: string; content: string; strength: number
  connections: string[]; timestamp: string
}

const memories = ref<MemoryNode[]>([
  { id: 'm1', type: 'episodic', content: 'User prefers concise answers with code examples', strength: 0.92, connections: ['m3'], timestamp: '2026-05-15' },
  { id: 'm2', type: 'semantic', content: 'IronClaw uses SQLite with WAL mode for persistence', strength: 0.85, connections: ['m5'], timestamp: '2026-05-14' },
  { id: 'm3', type: 'episodic', content: 'Python is user\'s primary language for data analysis tasks', strength: 0.78, connections: ['m1'], timestamp: '2026-05-13' },
  { id: 'm4', type: 'procedural', content: 'Strategy: git operations → check status → stage → commit → push', strength: 0.95, connections: [], timestamp: '2026-05-15' },
  { id: 'm5', type: 'semantic', content: 'Docker sandbox isolates bash execution per session', strength: 0.71, connections: ['m2'], timestamp: '2026-05-12' },
])

const filter = ref('all')
const selected = ref<MemoryNode | null>(null)

const filtered = computed(() => {
  if (filter.value === 'all') return memories.value
  return memories.value.filter(m => m.type === filter.value)
})

const typeColors: Record<string, string> = {
  episodic: '#7c3aed',
  semantic: '#3b82f6',
  procedural: '#22c55e',
  profile: '#f59e0b',
}
</script>

<template>
  <div class="memory-explorer">
    <div class="toolbar">
      <h1>Memory Explorer</h1>
      <div class="filters">
        <button v-for="t in ['all','episodic','semantic','procedural']" :key="t"
          class="filter-btn" :class="{ active: filter === t }" @click="filter = t">
          {{ t }}
        </button>
      </div>
      <input class="search-input" placeholder="Search memories..." />
    </div>
    <div class="memory-layout">
      <div class="memory-graph">
        <svg viewBox="0 0 600 400" class="graph-svg">
          <line v-for="m in filtered" v-for="c in m.connections"
            :key="`${m.id}-${c}`" :x1="pos(m.id).x" :y1="pos(m.id).y"
            :x2="pos(c).x" :y2="pos(c).y" stroke="#333" stroke-width="1" />
          <g v-for="(m, i) in filtered" :key="m.id" @click="selected = m">
            <circle :cx="pos(m.id).x" :cy="pos(m.id).y" :r="12 + m.strength * 10"
              :fill="typeColors[m.type] + '44'" :stroke="typeColors[m.type]" stroke-width="2" />
            <text :x="pos(m.id).x" :y="pos(m.id).y + 4" text-anchor="middle" fill="#e0e0e0" font-size="9">{{ m.type[0].toUpperCase() }}</text>
          </g>
        </svg>
      </div>
      <div class="memory-list">
        <div v-for="m in filtered" :key="m.id" class="memory-card"
          :class="{ selected: selected?.id === m.id }" @click="selected = m">
          <div class="memory-header">
            <span class="memory-type" :style="{ color: typeColors[m.type] }">{{ m.type }}</span>
            <span class="memory-strength">{{ (m.strength * 100).toFixed(0) }}%</span>
          </div>
          <div class="memory-content">{{ m.content.slice(0, 120) }}{{ m.content.length > 120 ? '...' : '' }}</div>
          <div class="memory-meta">{{ m.timestamp }} · {{ m.connections.length }} links</div>
        </div>
      </div>
      <div class="memory-detail" v-if="selected">
        <h3>Memory Detail</h3>
        <div class="detail-type" :style="{ color: typeColors[selected.type] }">{{ selected.type }}</div>
        <p>{{ selected.content }}</p>
        <div class="detail-stats">
          <div>Strength: {{ (selected.strength * 100).toFixed(0) }}%</div>
          <div>Created: {{ selected.timestamp }}</div>
          <div>Connections: {{ selected.connections.length }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts">
const positions: Record<string, {x:number,y:number}> = {
  m1: {x:200,y:100}, m2: {x:400,y:150}, m3: {x:300,y:250},
  m4: {x:150,y:300}, m5: {x:450,y:300},
}
function pos(id: string) { return positions[id] || {x:300, y:200} }
</script>

<style scoped>
.memory-explorer { display: flex; flex-direction: column; height: calc(100vh - 48px); }
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.toolbar h1 { font-size: 1.3em; color: #c4b5fd; margin-right: 16px; }
.filters { display: flex; gap: 4px; }
.filter-btn { background: transparent; border: 1px solid #333; color: #666; padding: 4px 12px; border-radius: 4px; cursor: pointer; font-size: 0.8em; }
.filter-btn.active { background: #7c3aed22; color: #a78bfa; border-color: #7c3aed44; }
.search-input { background: #111; border: 1px solid #333; color: #e0e0e0; padding: 6px 12px; border-radius: 6px; font-size: 0.85em; width: 200px; }
.memory-layout { display: flex; gap: 16px; flex: 1; min-height: 0; }
.memory-graph { flex: 1; background: #0d0d14; border-radius: 12px; border: 1px solid #222; overflow: hidden; }
.graph-svg { width: 100%; height: 100%; }
.memory-list { width: 300px; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
.memory-card { background: #111118; border: 1px solid #222; border-radius: 8px; padding: 12px; cursor: pointer; transition: border-color 0.15s; }
.memory-card:hover, .memory-card.selected { border-color: #7c3aed44; }
.memory-header { display: flex; justify-content: space-between; margin-bottom: 6px; }
.memory-type { font-size: 0.75em; font-weight: 600; text-transform: uppercase; }
.memory-strength { font-size: 0.75em; color: #666; }
.memory-content { font-size: 0.82em; line-height: 1.4; color: #ccc; }
.memory-meta { font-size: 0.7em; color: #555; margin-top: 6px; }
.memory-detail { width: 260px; background: #111118; border-radius: 12px; border: 1px solid #222; padding: 16px; overflow-y: auto; }
.memory-detail h3 { color: #888; font-size: 0.85em; margin-bottom: 8px; }
.detail-type { font-size: 0.8em; font-weight: 600; text-transform: uppercase; margin-bottom: 8px; }
.memory-detail p { font-size: 0.85em; line-height: 1.5; color: #ccc; margin-bottom: 16px; }
.detail-stats { display: flex; flex-direction: column; gap: 4px; font-size: 0.8em; color: #666; }
</style>
