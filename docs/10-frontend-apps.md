# 10. Frontend Apps

IronClaw has one frontend workspace: the standalone Vue Studio prototype.

## Studio: `web/studio/`

`web/studio/` is a Vue 3 + Vite application with:

- Vue Router.
- Pinia.
- Vue Flow.
- Monaco editor dependency.
- D3 dependency.
- Splitpanes dependency.

Routes:

| Route | View | Current behavior |
|---|---|---|
| `/` | `Dashboard.vue` | Reads WebSocket-derived store metrics. |
| `/flows` | `FlowEditor.vue` | Local visual flow mock and YAML preview. |
| `/prompts` | `PromptIDE.vue` | Local prompt editor; test preview is randomized simulation. |
| `/memory` | `MemoryExplorer.vue` | Local static memory graph/list demo. |
| `/evolution` | `EvolutionMonitor.vue` | Evolution monitoring UI surface. |

The Studio connects to `/ws` through `web/studio/src/stores/agent.ts`. Other views currently use local state. This means Studio should be described as a prototype or visual IDE surface unless backend APIs are added.

## Building Studio

```bash
cd web/studio
npm ci
npm run build
```

This runs `vue-tsc` and Vite. It creates `web/studio/dist/`. That directory is generated output and should usually be removed before committing.

## Frontend Change Checklist

Studio changes:

1. Decide whether the view is prototype-only or backend-connected.
2. If backend-connected, add API routes and document auth/state behavior.
3. Run `cd web/studio && npm run build`.
4. Remove accidental `web/studio/dist/` before commit unless release policy requires it.

## Current Frontend Boundary

Do not document Studio's Prompt IDE save/test, Flow Editor run/export, or Memory Explorer search as production-connected until backend APIs and persistence exist.
