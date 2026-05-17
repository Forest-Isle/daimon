<script setup lang="ts">
import { useAgentStore } from '../stores/agent'
const store = useAgentStore()
</script>

<template>
  <div class="dashboard">
    <h1>Agent Dashboard</h1>
    <div class="grid">
      <div class="card">
        <div class="card-label">Active Sessions</div>
        <div class="card-value">{{ store.metrics.activeSessions }}</div>
      </div>
      <div class="card">
        <div class="card-label">Total Tokens</div>
        <div class="card-value">{{ (store.metrics.totalTokens / 1000).toFixed(1) }}k</div>
      </div>
      <div class="card">
        <div class="card-label">Cache Hits</div>
        <div class="card-value">{{ store.metrics.cacheHits }}</div>
      </div>
      <div class="card">
        <div class="card-label">Status</div>
        <div class="card-value status-text">{{ store.connected ? '🟢 Online' : '🔴 Offline' }}</div>
      </div>
    </div>
    <div class="card full-width">
      <div class="card-label">Active Session Phases</div>
      <div class="session-list" v-if="store.metrics.sessions.length">
        <div v-for="s in store.metrics.sessions" :key="s.id" class="session-row">
          <code>{{ s.id.slice(0, 12) }}...</code>
          <span class="phase-tag">{{ s.phase }}</span>
          <span v-if="s.tool" class="tool-tag">🔧 {{ s.tool }}</span>
        </div>
      </div>
      <div v-else class="empty">No active sessions</div>
    </div>
  </div>
</template>

<style scoped>
.dashboard { max-width: 900px; }
h1 { font-size: 1.5em; margin-bottom: 24px; color: #c4b5fd; }
.grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 24px; }
.card {
  background: #111118; border: 1px solid #222; border-radius: 12px; padding: 20px;
}
.card.full-width { grid-column: 1 / -1; }
.card-label { color: #666; font-size: 0.8em; text-transform: uppercase; letter-spacing: 0.5px; margin-bottom: 8px; }
.card-value { font-size: 1.8em; font-weight: 700; color: #e0e0e0; }
.status-text { font-size: 1.2em; }
.session-list { display: flex; flex-direction: column; gap: 8px; }
.session-row { display: flex; align-items: center; gap: 12px; padding: 8px 12px; background: #0a0a0f; border-radius: 6px; font-size: 0.85em; }
.phase-tag { background: #7c3aed22; color: #a78bfa; padding: 2px 8px; border-radius: 4px; }
.tool-tag { color: #666; }
.empty { color: #444; padding: 24px; text-align: center; }
</style>
