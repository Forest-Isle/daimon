# IronClaw - Incomplete Implementation Quick Reference

## 🔴 CRITICAL (Must Fix for Full Functionality)

| Issue | File | Line | Severity | Impact |
|-------|------|------|----------|--------|
| Token limit hard-coded | `internal/gateway/init_multiagent.go` | 64 | HIGH | May cause token overflow with different models |
| Profiler not wired to reflector | `internal/gateway/init_memory.go` | 94 | MEDIUM | Memory profiling not triggered automatically |

---

## 🟡 IMPORTANT (Test Coverage Gaps)

| Issue | File | Lines | Type | Details |
|-------|------|-------|------|---------|
| Lifecycle tests stub | `internal/tool/memory_manage_test.go` | 123-127 | TEST STUB | Needs LLM mocking: ADD/UPDATE/DELETE/NOOP decisions |
| Consolidation tests stub | `internal/memory/reflector_test.go` | 84-86 | TEST STUB | 3 missing test suites: consolidation, knowledge graph, privacy |
| Conditional skip | `internal/memory/forgetting_curve_test.go` | 85 | SKIP | Fade test skipped if strength above threshold |
| Env skip | `internal/hook/injector_test.go` | 18 | SKIP | Git repo test skipped outside git repos |

---

## 🟠 UNIMPLEMENTED (Intentional Future Work)

| Backend | File | Line | Status | Test Coverage |
|---------|------|------|--------|----------------|
| Subprocess | `internal/agent/backend.go` | 101-103 | Not Yet Implemented | ✅ Has test: `TestSubprocessBackend_NotImplemented` |
| Docker | `internal/agent/backend.go` | 130-132 | Not Yet Implemented | ✅ Has test: `TestDockerBackend_NotAvailable` |

---

## 🟢 INTENTIONAL DESIGN PATTERNS (Not Issues)

| Pattern | File | Type | Purpose |
|---------|------|------|---------|
| NoopEmbedding | `internal/memory/embedding.go:15-24` | Placeholder | Used when embedding disabled |
| Empty Cleanup methods | Multiple files | No-op | Types don't hold resources |
| Goroutine launchers | `internal/memory/` | Async init | Correct singleton pattern |

---

## 📊 Summary by Severity

```
CRITICAL FIXES:    2 items
IMPORTANT TESTS:   4 items  
INTENTIONAL STUBS: 2 items
DESIGN PATTERNS:   3 items
                  ─────────
TOTAL ISSUES:      11 items
```

---

## 🎯 Action Items Priority

### Phase 1 (This Sprint)
```
[ ] Derive token limit from model name (init_multiagent.go:64)
[ ] Wire profiler callback to reflector (init_memory.go:94)
```

### Phase 2 (Next Sprint)
```
[ ] Create LLM mock harness for lifecycle tests
[ ] Implement lifecycle decision tests (memory_manage_test.go:123)
[ ] Implement consolidation integration tests (reflector_test.go:84)
```

### Phase 3+ (Future Releases)
```
[ ] Implement subprocess backend (backend.go:101-103)
[ ] Implement Docker backend (backend.go:130-132)
[ ] Implement knowledge graph integration tests
[ ] Implement privacy integration tests
```

---

## 🔍 File-by-File Checklist

### Gateway Initialization
- [x] `internal/gateway/init_memory.go` - **1 TODO: profiler callback** (line 94)
- [x] `internal/gateway/init_multiagent.go` - **1 TODO: model token limit** (line 64)

### Agent System
- [x] `internal/agent/backend.go` - **2 unimplemented backends** (lines 101-103, 130-132)
- [x] `internal/agent/backend_test.go` - ✅ Tests both unimplemented backends
- [x] `internal/agent/background_test.go` - ✅ Comprehensive test coverage

### Memory System
- [x] `internal/memory/embedding.go` - Noop implementation (intentional)
- [x] `internal/memory/reflector_test.go` - **3 missing test suites** (lines 84-86)
- [x] `internal/memory/forgetting_curve_test.go` - **1 conditional skip** (line 85)
- [x] `internal/tool/memory_manage_test.go` - **1 test stub** (lines 123-127)

### Hooks
- [x] `internal/hook/injector_test.go` - **1 environment skip** (line 18)

### Build & Dependencies
- [x] `./Makefile` - ✅ All targets working
- [x] `./go.mod` - ✅ No problematic dependencies

---

## 📝 Quick Copy-Paste Commands

### Find All TODOs
```bash
grep -rn "TODO\|FIXME" --include="*.go" /Users/wuqisen/learning/IronClaw | grep -v ".claude" | grep -v ".codex"
```

### Find All Unimplemented
```bash
grep -rn "not yet implemented\|not.*impl" --include="*.go" /Users/wuqisen/learning/IronClaw -i
```

### Find All Skipped Tests
```bash
grep -rn "t.Skip\|SkipNow" --include="*_test.go" /Users/wuqisen/learning/IronClaw
```

### Run Tests
```bash
cd /Users/wuqisen/learning/IronClaw
make test
```

---

## 📌 Notes

- **Total Go Files Scanned:** 233+
- **Total Test Files Reviewed:** 42
- **Build Status:** ✅ No issues
- **Dependency Status:** ✅ No issues
- **Critical Blockers:** 0 (system is functional)
- **Test Coverage:** Good (233 test functions found)

