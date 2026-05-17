import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import Dashboard from './views/Dashboard.vue'
import FlowEditor from './views/FlowEditor.vue'
import PromptIDE from './views/PromptIDE.vue'
import MemoryExplorer from './views/MemoryExplorer.vue'
import EvolutionMonitor from './views/EvolutionMonitor.vue'

const routes = [
  { path: '/', component: Dashboard },
  { path: '/flows', component: FlowEditor },
  { path: '/prompts', component: PromptIDE },
  { path: '/memory', component: MemoryExplorer },
  { path: '/evolution', component: EvolutionMonitor },
]

const router = createRouter({ history: createWebHashHistory(), routes })
const pinia = createPinia()
const app = createApp(App)

app.use(router).use(pinia).mount('#app')
