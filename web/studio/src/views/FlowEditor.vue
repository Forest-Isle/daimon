<script setup lang="ts">
import { ref, computed } from 'vue'
import { VueFlow, useVueFlow } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'

// Pipeline node types
const nodeTypes = {
  trigger: { label: '⚡ Trigger', color: '#7c3aed' },
  llm: { label: '🧠 LLM Call', color: '#3b82f6' },
  tool: { label: '🔧 Tool Exec', color: '#22c55e' },
  branch: { label: '🔀 Branch', color: '#f59e0b' },
  loop: { label: '🔄 Loop', color: '#ef4444' },
  output: { label: '📤 Output', color: '#8b5cf6' },
}

const elements = ref([
  { id: '1', type: 'input', position: { x: 250, y: 50 },  data: { label: 'On Message' } },
  { id: '2', position: { x: 250, y: 150 }, data: { label: 'Memory Search' } },
  { id: '3', position: { x: 250, y: 260 }, data: { label: 'LLM Plan' } },
  { id: '4', position: { x: 100, y: 380 }, data: { label: 'Tool: bash' } },
  { id: '5', position: { x: 400, y: 380 }, data: { label: 'Tool: http' } },
  { id: '6', position: { x: 250, y: 500 }, data: { label: 'LLM Reflect' } },
  { id: '7', type: 'output', position: { x: 250, y: 620 }, data: { label: 'Respond' } },
  { id: 'e1-2', source: '1', target: '2' },
  { id: 'e2-3', source: '2', target: '3' },
  { id: 'e3-4', source: '3', target: '4' },
  { id: 'e3-5', source: '3', target: '5' },
  { id: 'e4-6', source: '4', target: '6' },
  { id: 'e5-6', source: '5', target: '6' },
  { id: 'e6-7', source: '6', target: '7' },
])

const selectedNode = ref<string | null>(null)

const yamlPreview = computed(() => {
  return `# Pipeline YAML
name: cognitive-agent
version: 1.0.0

nodes:
  - id: perceive
    type: memory_search
  - id: plan
    type: llm
    config:
      model: claude-sonnet-4-20250514
  - id: act
    type: parallel
    tools: [bash, http]
  - id: reflect
    type: llm

edges:
  - from: perceive → plan
  - from: plan → act
  - from: act → reflect
  - from: reflect → respond`
})
</script>

<template>
  <div class="flow-editor">
    <div class="toolbar">
      <h1>Flow Editor</h1>
      <div class="toolbar-actions">
        <button class="btn" v-for="(t, k) in nodeTypes" :key="k"
          :style="{ borderColor: t.color, color: t.color }">
          {{ t.label }}
        </button>
        <button class="btn primary">▶ Run</button>
        <button class="btn">Export YAML</button>
      </div>
    </div>
    <div class="editor-area">
      <div class="flow-canvas">
        <VueFlow :elements="elements" :default-zoom="1.2" fit-view-on-init
          @pane-click="selectedNode = null">
          <Background :gap="20" />
        </VueFlow>
      </div>
      <div class="inspector">
        <h3>Inspector</h3>
        <div v-if="!selectedNode" class="empty">Click a node to inspect</div>
        <div class="yaml-preview">
          <h4>YAML Preview</h4>
          <pre>{{ yamlPreview }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.flow-editor { display: flex; flex-direction: column; height: calc(100vh - 48px); }
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.toolbar h1 { font-size: 1.3em; color: #c4b5fd; margin-right: 16px; }
.btn {
  background: transparent; border: 1px solid #333; color: #999;
  padding: 6px 14px; border-radius: 6px; font-size: 0.8em; cursor: pointer;
}
.btn.primary { background: #7c3aed; color: white; border-color: #7c3aed; }
.editor-area { display: flex; gap: 16px; flex: 1; min-height: 0; }
.flow-canvas { flex: 1; background: #0d0d14; border-radius: 12px; border: 1px solid #222; overflow: hidden; }
.inspector { width: 280px; background: #111118; border-radius: 12px; border: 1px solid #222; padding: 16px; overflow-y: auto; }
.inspector h3 { color: #888; font-size: 0.85em; margin-bottom: 12px; }
.yaml-preview { margin-top: 16px; }
.yaml-preview h4 { color: #666; font-size: 0.75em; margin-bottom: 8px; }
.yaml-preview pre { background: #0a0a0f; color: #a78bfa; padding: 12px; border-radius: 8px; font-size: 0.7em; overflow-x: auto; }
.empty { color: #444; text-align: center; padding: 24px; }
</style>
