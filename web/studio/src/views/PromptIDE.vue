<script setup lang="ts">
import { ref, computed } from 'vue'

const promptRole = ref('plan')
const promptText = ref(`You are an AI agent planner. Given the user's request and available context, create a structured execution plan.

## Available Tools
{{tools}}

## Context
{{context}}

## User Request
{{user_message}}

## Instructions
1. Break the task into sequential subtasks
2. For each subtask, specify the tool and input
3. Ensure dependencies are correct (no cycles)
4. Output valid JSON only

## Output Format
{
  "subtasks": [
    {
      "id": "step_1",
      "description": "...",
      "tool": "tool_name",
      "input": {...},
      "depends_on": []
    }
  ],
  "confidence": 0.0
}`)

const variables = computed(() => {
  const matches = promptText.value.match(/\{\{(\w+)\}\}/g) || []
  return [...new Set(matches.map(m => m.replace(/[{}]/g, '')))]
})

const previewInput = ref('List all files in the current directory')
const previewOutput = ref('')

function runPreview() {
  previewOutput.value = `Generated plan with ${Math.floor(Math.random() * 5) + 2} subtasks...\n\n` +
    `1. **read_dir** — List directory contents\n` +
    `2. **filter_results** — Filter by criteria\n` +
    `3. **format_output** — Format for display\n\n` +
    `Confidence: ${(Math.random() * 0.3 + 0.7).toFixed(2)}`
}

const versions = ref([
  { id: 'v3', label: 'v3 (current)', active: true, success: 0.87 },
  { id: 'v2', label: 'v2', active: false, success: 0.82 },
  { id: 'v1', label: 'v1', active: false, success: 0.74 },
])
</script>

<template>
  <div class="prompt-ide">
    <div class="toolbar">
      <h1>Prompt IDE</h1>
      <select v-model="promptRole" class="role-select">
        <option value="plan">Plan Prompt</option>
        <option value="reflect">Reflect Prompt</option>
        <option value="act">Act Prompt</option>
      </select>
      <button class="btn" @click="runPreview">▶ Test Run</button>
      <button class="btn">💾 Save</button>
    </div>
    <div class="editor-area">
      <div class="editor-pane">
        <div class="editor-header">
          <span>Editor</span>
          <span class="vars">Variables: <code v-for="v in variables" :key="v">{{ '{{' }}{{ v }}{{ '}}' }}</code></span>
        </div>
        <textarea v-model="promptText" class="prompt-editor" spellcheck="false"></textarea>
      </div>
      <div class="preview-pane">
        <div class="preview-header">Preview</div>
        <div class="preview-input">
          <input v-model="previewInput" placeholder="Test input..." class="test-input" />
        </div>
        <div class="preview-output" v-if="previewOutput">
          <div class="output-label">Output</div>
          <pre>{{ previewOutput }}</pre>
        </div>
        <div class="version-list">
          <div class="preview-header">Versions</div>
          <div v-for="v in versions" :key="v.id" class="version-row" :class="{ active: v.active }">
            <span>{{ v.label }}</span>
            <span class="success-rate">{{ (v.success * 100).toFixed(0) }}% success</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.prompt-ide { display: flex; flex-direction: column; height: calc(100vh - 48px); }
.toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
.toolbar h1 { font-size: 1.3em; color: #c4b5fd; margin-right: 16px; }
.role-select { background: #111; color: #e0e0e0; border: 1px solid #333; padding: 6px 12px; border-radius: 6px; }
.btn { background: #1a1a2e; border: 1px solid #333; color: #999; padding: 6px 14px; border-radius: 6px; cursor: pointer; font-size: 0.8em; }
.editor-area { display: flex; gap: 16px; flex: 1; min-height: 0; }
.editor-pane { flex: 1; display: flex; flex-direction: column; }
.editor-header { display: flex; justify-content: space-between; align-items: center; padding: 8px 0; color: #666; font-size: 0.8em; }
.vars code { background: #1a1a2e; color: #a78bfa; padding: 1px 6px; border-radius: 3px; margin: 0 2px; font-size: 0.9em; }
.prompt-editor {
  flex: 1; background: #0d0d14; color: #e0e0e0; border: 1px solid #222;
  border-radius: 12px; padding: 20px; font-family: 'JetBrains Mono', monospace;
  font-size: 0.85em; line-height: 1.6; resize: none; outline: none;
}
.preview-pane { width: 320px; background: #111118; border-radius: 12px; border: 1px solid #222; padding: 16px; overflow-y: auto; display: flex; flex-direction: column; gap: 12px; }
.preview-header { color: #888; font-size: 0.8em; }
.test-input { width: 100%; background: #0a0a0f; border: 1px solid #222; color: #e0e0e0; padding: 8px 12px; border-radius: 6px; font-size: 0.85em; }
.output-label { color: #666; font-size: 0.75em; margin-bottom: 4px; }
.preview-output pre { background: #0a0a0f; color: #a78bfa; padding: 12px; border-radius: 8px; font-size: 0.8em; }
.version-row { display: flex; justify-content: space-between; padding: 8px 12px; background: #0a0a0f; border-radius: 6px; font-size: 0.85em; margin-bottom: 4px; }
.version-row.active { border: 1px solid #7c3aed22; }
.success-rate { color: #22c55e; font-size: 0.8em; }
</style>
