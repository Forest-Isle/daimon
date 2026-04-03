# IronClaw Subagent System - Complete Documentation

This directory contains comprehensive documentation of the IronClaw subagent architecture, including complete file analysis, architectural diagrams, and quick reference guides.

## 📚 Documentation Files

### 1. **SUBAGENT_ARCHITECTURE.md** (Main Comprehensive Document)
   - **Complete architectural overview** of the subagent system
   - Detailed component descriptions with code examples
   - Full source code listings for all key files
   - Data flow walkthrough with concrete examples
   - Integration points and configuration guide
   - Best practices and design patterns
   - Troubleshooting guide
   - Future enhancement notes (Phase 3)
   
   **Read this for:** Deep understanding of how everything works

### 2. **QUICK_REFERENCE.md** (Quick Lookup Guide)
   - TL;DR overview
   - Component summary table
   - Quick start guide for defining agents
   - 12-step execution flow
   - Configuration reference
   - Troubleshooting checklist
   - Files to know
   - Example agent definition
   
   **Read this for:** Quick lookups, common tasks, troubleshooting

### 3. **ARCHITECTURE_DIAGRAMS.md** (Visual Reference)
   - System architecture overview
   - AgentTool execution flow
   - Runtime system prompt building
   - Tool scoping visualization
   - Circuit breaker state machine
   - Multi-agent pipeline example
   - Gateway initialization sequence
   
   **Read this for:** Understanding relationships and data flow visually

## 🎯 Quick Navigation

### I want to...

| Goal | Start Here |
|------|-----------|
| **Understand the whole system** | SUBAGENT_ARCHITECTURE.md § Overview |
| **Create a new sub-agent** | QUICK_REFERENCE.md § Quick Start |
| **Debug a problem** | QUICK_REFERENCE.md § Troubleshooting |
| **See the flow diagrams** | ARCHITECTURE_DIAGRAMS.md |
| **Understand AgentTool** | SUBAGENT_ARCHITECTURE.md § Agent Tool |
| **Learn about tool scoping** | ARCHITECTURE_DIAGRAMS.md § Tool Scoping |
| **Configure an agent** | QUICK_REFERENCE.md § Configuration |
| **Set up multi-agent pipeline** | SUBAGENT_ARCHITECTURE.md § Task Context |
| **Understand circuit breaker** | QUICK_REFERENCE.md § Circuit Breaker |
| **See code examples** | SUBAGENT_ARCHITECTURE.md § Step-by-Step Example |

## 🔑 Key Concepts Summary

### Architecture Layers

1. **Orchestrator Runtime** - Main agent that decides when to delegate
2. **AgentTool** - Bridge that executes sub-agents as tools
3. **Sub-Agent Runtime** - Temporary instance with scoped tools
4. **Shared Resources** - Memory, sessions, database (shared across agents)

### Safety Mechanisms

| Mechanism | Purpose | Details |
|-----------|---------|---------|
| **Tool Scoping** | Prevent unintended tool usage | `agent_*` tools always excluded |
| **Circuit Breaker** | Prevent cascading failures | Open after 3 failures, half-open after 60s |
| **Timeout** | Prevent runaway execution | Default 120s, configurable per agent |
| **Whitelist** | Limit tools per agent | Specify in spec.Tools |

### Key Files

| File | Lines | Key Responsibility |
|------|-------|-------------------|
| **agent_tool.go** | 286 | Execute sub-agents as tools |
| **agent_manager.go** | 181 | Load & register agent specs |
| **spec.go** | 87 | Define agent configuration |
| **runtime.go** | 414 | Main agent loop (reused) |
| **circuit_breaker.go** | 108 | Failure protection |
| **task_context.go** | 105 | Multi-agent coordination |

## 🚀 Getting Started (5 Minutes)

### Step 1: Define an Agent Spec
```yaml
# ~/.IronClaw/agents/researcher.yaml
name: researcher
description: Searches the web for findings
system_prompt: |
  You are a research expert. Search and compile findings.
tools: [http, file_read]
max_iterations: 5
timeout: 180s
```

### Step 2: Start IronClaw
```bash
ironclaw start
```

### Step 3: Use It
```
User: "Research recent AI advances and summarize"
Orchestrator LLM: "I'll use agent_researcher for this"
Sub-agent: Searches web, returns findings
```

That's it! The sub-agent was automatically:
- Loaded from YAML
- Registered as `agent_researcher` tool
- Made available to LLM
- Given scoped tools (http, file_read only)
- Protected by circuit breaker

## 📋 Full Component List

### Core Components
- **AgentTool** - Tool wrapper for sub-agents (agent_tool.go)
- **AgentSpec** - Configuration definition (spec.go)
- **AgentManager** - Spec loader and tool registrar (agent_manager.go)
- **Runtime** - Agent execution engine (runtime.go)

### Safety & Coordination
- **CircuitBreaker** - Failure protection (circuit_breaker.go)
- **TaskContext** - Multi-agent pipeline support (task_context.go)
- **captureChannel** - Output collection (agent_tool.go)

### Integration Points
- **Gateway** - System initialization (gateway/gateway.go)
- **Tool Registry** - Central tool store (tool/tool.go)
- **Provider Interface** - LLM abstraction (provider.go)

## 🔧 Common Configuration

### Sub-Agent Configuration (YAML)
```yaml
name: agent_name
description: What this agent does
system_prompt: Custom instructions
model: claude-3-5-sonnet-20241022
max_tokens: 8000
max_iterations: 5
timeout: 180s
tools: [bash, file_read, file_write]
requires_approval: false
tags: [research, analysis]
```

### Base Agent Configuration (config.yaml)
```yaml
agent:
  mode: simple
  max_iterations: 10
  system_prompt: Default agent instructions
  personality: Agent personality traits
```

## 📊 Data Flow Summary

```
User Message
  ↓
Orchestrator (includes agent descriptions in prompt)
  ↓
LLM decides to call agent_researcher
  ↓
AgentTool.Execute()
  ├─ Create scoped registry (no agent_* tools)
  ├─ Merge spec config
  ├─ Create temporary Runtime
  ├─ Run sub-agent loop
  └─ Capture output
  ↓
Return result to orchestrator
  ↓
Orchestrator continues, generates final response
```

## 🧪 Testing & Validation

### Circuit Breaker
- Monitor via logs: "agent: completed" or "circuit breaker open"
- Default: 3 failures before opening

### Memory Sharing
- Check logs: "memory.md search failed" indicates no memStore
- Verify: `gateway.SetMemoryStore()` called

### Tool Scoping
- Verify sub-agent can't call `agent_*` tools
- Check whitelist in spec matches intended tools

### Timeouts
- Default 120s per execution
- Override with `spec.timeout`
- Check logs for "context deadline exceeded"

## 🚨 Troubleshooting Quick Links

| Issue | Location | Solution |
|-------|----------|----------|
| Sub-agent calls another agent | QUICK_REFERENCE § Tool Scoping | Auto-excluded, remove from whitelist |
| Circuit breaker open | QUICK_REFERENCE § Troubleshooting | Check timeout, errors, LLM API |
| Memory not shared | QUICK_REFERENCE § Troubleshooting | Verify memStore setup |
| Agent timing out | ARCHITECTURE_DIAGRAMS § AgentTool | Increase timeout or split task |
| Output truncated | ARCHITECTURE_DIAGRAMS § Capture Channel | Increase max_tokens |

## 📖 Reading Recommendations

### For Different Audiences

**First-time users:**
1. Read QUICK_REFERENCE.md § TL;DR
2. Read QUICK_REFERENCE.md § Quick Start
3. Define an agent in ~/.IronClaw/agents/
4. Test with orchestrator

**Developers:**
1. Read SUBAGENT_ARCHITECTURE.md § Overview
2. Read SUBAGENT_ARCHITECTURE.md § Core Architecture
3. Review ARCHITECTURE_DIAGRAMS.md § AgentTool Execution
4. Study source files (agent_tool.go, agent_manager.go)

**Operators:**
1. Read QUICK_REFERENCE.md (all sections)
2. Review QUICK_REFERENCE.md § Troubleshooting
3. Monitor circuit breaker states in logs
4. Check memory store health

**Architects:**
1. Read SUBAGENT_ARCHITECTURE.md (all sections)
2. Review ARCHITECTURE_DIAGRAMS.md (all diagrams)
3. Study design patterns section
4. Review Phase 3 enhancements

## 🔮 Future Enhancements (Phase 3)

- **Remote Agents** - A2A protocol for external agents
- **Cognitive Mode** - 5-phase loop for complex agents
- **Agent Learning** - Knowledge graph integration
- **Semantic Routing** - Tag-based agent selection
- **Nested Agents** - Safe hierarchical delegation

See SUBAGENT_ARCHITECTURE.md § Future Enhancements for details.

## 📞 Quick Reference Summary

```
Component           File              Purpose
─────────────────────────────────────────────────────
AgentTool          agent_tool.go      Execute sub-agents
AgentSpec          spec.go            Define agent config
AgentManager       agent_manager.go   Load & register
Runtime            runtime.go         Agent loop
CircuitBreaker     circuit_breaker.go Failure protection
TaskContext        task_context.go    Multi-agent coordination
captureChannel     agent_tool.go      Output collection
Registry           tool/tool.go       Tool storage
```

## ✅ Verification Checklist

- [ ] Understand AgentTool execution flow
- [ ] Know tool scoping prevents recursion
- [ ] Familiar with circuit breaker states
- [ ] Can create agent spec YAML
- [ ] Can troubleshoot common issues
- [ ] Know where to find code references
- [ ] Understand memory sharing mechanism
- [ ] Familiar with task context pattern

---

**Last Updated:** 2024-04-02  
**Version:** 1.0 - Complete Architecture Analysis  
**Source Files Analyzed:** 10 Go files, 1,200+ lines of code
