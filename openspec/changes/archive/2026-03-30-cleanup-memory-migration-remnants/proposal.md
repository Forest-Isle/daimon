## Why

`2026-03-28-migrate-memory-to-file-based` 变更已完成并归档，但代码中残留大量旧架构的痕迹：项目当前无法编译（`consolidator.go` 语法错误 + 函数名/类型名不匹配），4 个文件完全是死代码（`index.go`、`metadata.go`、`txlog.go`、`chunker.go`），`forgetting_curve.go` 和 `vss.go` 仍引用已废弃的 `memory_facts` 表，`Store` 接口保留了双表时代的冗余方法。需要立即清理以恢复项目可编译状态并完成迁移的收尾工作。

## What Changes

- **修复编译错误**：移除 `consolidator.go:194` 多余的 `}`，统一 `NewFileStore` → `NewFileMemoryStore` 调用，移除 `gateway.go` 中 `SQLiteStore` fallback 路径
- **删除死代码文件**：移除 `index.go`（JSON 索引管理器）、`metadata.go`（分块元数据）、`txlog.go`（事务日志）、`chunker.go`（文本分块器）— 均无外部引用
- **修复 `forgetting_curve.go`**：消除对 `*SQLiteStore` 类型的依赖，改为通过接口或直接使用 `*sql.DB` 查询 `memory_index` 表
- **清理或移除 `vss.go`**：该文件所有查询指向 `memory_facts` / `memory_facts_vss`，需改为 `memory_embeddings` 或在新架构中移除（`file_store.go` 已内置向量搜索）
- **精简 `Store` 接口**：合并 `Save`/`SaveFact` 和 `Delete`/`DeleteFact` 重复方法
- **更新过时注释和引用**：清除代码中所有对 `memory_facts` 表的非迁移工具引用

## Capabilities

### New Capabilities

（无新增能力，本变更为纯清理）

### Modified Capabilities

（无需求层面的变更，仅实现层面的清理和修复）

## Impact

**代码变更：**
- 删除 4 个文件：`index.go`、`metadata.go`、`txlog.go`、`chunker.go`
- 重构 4 个文件：`gateway.go`、`forgetting_curve.go`、`vss.go`、`store.go`
- 修复 2 个文件：`consolidator.go`、`cmd/ironclaw/memory.go`
- 更新测试：`benchmark_test.go`、`forgetting_curve_test.go`

**编译状态：** 从无法编译恢复到可编译 + 测试通过

**API 变更：** `Store` 接口方法减少（合并重复方法），`FileMemoryStore` 实现不变

**无外部依赖变更**
