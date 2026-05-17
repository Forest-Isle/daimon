<script setup lang="ts">
import { useAgentStore } from './stores/agent'

const store = useAgentStore()
store.connect()
</script>

<template>
  <div class="app-shell">
    <nav class="sidebar">
      <div class="logo">⚡ IronClaw</div>
      <router-link to="/" class="nav-item">📊 Dashboard</router-link>
      <router-link to="/flows" class="nav-item">🔀 Flow Editor</router-link>
      <router-link to="/prompts" class="nav-item">📝 Prompt IDE</router-link>
      <router-link to="/memory" class="nav-item">🧠 Memory</router-link>
      <router-link to="/evolution" class="nav-item">🧬 Evolution</router-link>
      <div class="status">
        <span :class="['dot', store.connected ? 'green' : 'red']"></span>
        {{ store.connected ? 'Connected' : 'Disconnected' }}
      </div>
    </nav>
    <main class="content">
      <router-view />
    </main>
  </div>
</template>

<style>
.app-shell { display: flex; height: 100vh; }
.sidebar {
  width: 220px; background: #111118; border-right: 1px solid #222;
  display: flex; flex-direction: column; padding: 16px 12px; gap: 4px;
}
.logo { font-size: 1.2em; font-weight: 700; color: #7c3aed; margin-bottom: 16px; }
.nav-item {
  color: #999; text-decoration: none; padding: 10px 12px;
  border-radius: 8px; font-size: 0.9em; transition: all 0.15s;
}
.nav-item:hover, .nav-item.router-link-active { background: #1a1a2e; color: #e0e0e0; }
.status { margin-top: auto; font-size: 0.8em; display: flex; align-items: center; gap: 8px; color: #666; }
.dot { width: 8px; height: 8px; border-radius: 50%; }
.dot.green { background: #22c55e; }
.dot.red { background: #ef4444; }
.content { flex: 1; overflow-y: auto; padding: 24px; }
</style>
