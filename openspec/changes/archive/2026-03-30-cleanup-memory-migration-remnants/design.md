## Context

`2026-03-28-migrate-memory-to-file-based` 变更将记忆系统从 SQLite 主存储迁移到 Markdown 文件主存储 + SQLite 辅助索引的架构。迁移的核心功能（FileMemoryStore、混合搜索、生命周期管理、迁移工具）已实现，但代码中残留了大量旧架构痕迹，导致项目无法编译。

**当前状态：**
- 编译失败：`consolidator.go` 语法错误 + 多处函数名/类型名不匹配
- 25 个文件存在于 `internal/memory/`，其中 4 个是完全无引用的死代码
- `forgetting_curve.go` 和 `vss.go` 仍绑定到已废弃的 `memory_facts` 表和 `SQLiteStore` 类型
- `Store` 接口保留了双表时代（`memories` + `memory_facts`）的冗余方法

**约束：**
- 迁移工具（`migrator.go`）必须保留对 `memory_facts` 的引用，因为它需要读旧数据
- `fact_access_log` / `fact_access_stats` 表仍在使用（遗忘曲线依赖）
- SQL migrations 文件（003）不能删除，因为已有用户的数据库包含这些表

## Goals / Non-Goals

**Goals:**
- 恢复项目可编译状态
- 删除所有无外部引用的死代码文件
- 消除非迁移工具代码中对 `memory_facts` / `SQLiteStore` 的依赖
- 精简 `Store` 接口，移除双表时代的冗余方法
- 确保所有测试通过

**Non-Goals:**
- 不引入新功能或改变现有行为
- 不修改迁移工具对旧表的引用（这是它的正常工作）
- 不删除旧 SQL migration 文件（保持数据库向后兼容）
- 不重构 FileMemoryStore 的实现

## Decisions

### Decision 1: 删除 4 个死代码文件

**Choice:** 删除 `index.go`、`metadata.go`、`txlog.go`、`chunker.go`

**Rationale:**
- 这 4 个文件中定义的类型（`IndexManager`、`MetadataManager`、`TransactionLog`、`Chunker`）均无外部引用
- `index.go` 是旧的 JSON 索引方案，已被 MEMORY.md + SQLite 索引取代
- `metadata.go` 是分块存储的元数据管理器，新架构不使用分块
- `txlog.go` 是批量嵌入的事务日志，FileMemoryStore 未使用
- `chunker.go` 的文本分块功能未被任何文件调用

### Decision 2: 移除 gateway.go 中的 SQLiteStore fallback

**Choice:** 删除 `gateway.go` 中 `storageType != "file"` 的 fallback 分支，强制使用 FileMemoryStore

**Rationale:**
- `SQLiteStore` 类型已不存在，fallback 路径无法编译
- 迁移已完成，不再需要旧存储路径
- 保留旧数据检测逻辑（`SELECT COUNT(*) FROM memory_facts`）用于提示用户迁移

### Decision 3: forgetting_curve.go 改用接口依赖

**Choice:** 将 `ForgettingCurveManager` 的 `store *SQLiteStore` 字段改为 `db *store.DB`，查询改用 `memory_index` 表

**Rationale:**
- `SQLiteStore` 类型已删除
- `memory_index` 表有 `strength`、`last_accessed_at` 等字段，可以完全替代 `memory_facts` 的查询
- 遗忘曲线的 `FadeWeakMemories()` 改为：扫描 `memory_index` 中 strength < 0.3 的记录，移动对应文件到 `archived/`

### Decision 4: 移除 vss.go

**Choice:** 删除 `vss.go`，因为 `file_store.go` 已内置向量搜索

**Rationale:**
- `vss.go` 所有查询指向 `memory_facts` / `memory_facts_vss` 表
- `file_store.go` 的 `Search()` 已通过 `memory_embeddings` 表实现向量搜索
- sqlite-vss 扩展在多数环境不可用，保留意义不大
- 如未来需要 HNSW 加速，应基于 `memory_embeddings` 表重新实现

**Alternatives Considered:**
- 重写 vss.go 适配 `memory_embeddings`：可行但当前不紧急，file_store.go 的暴力搜索对于记忆量级（通常 <10K）足够

### Decision 5: 精简 Store 接口

**Choice:** 合并重复方法

```
Save + SaveFact → Save
Delete + DeleteFact → Delete
UpdateFact → Update（去掉 Fact 后缀）
```

**Rationale:**
- `Save` 和 `SaveFact` 是双表时代分别写 `memories` 和 `memory_facts` 的遗留
- 新架构只有一种存储（文件），不需要两个方法
- 所有调用方需同步更新

## Risks / Trade-offs

### Risk 1: 外部使用者依赖旧接口名
**Risk:** 如果有外部代码依赖 `SaveFact`/`DeleteFact` 方法名，重命名会破坏兼容性
**Mitigation:** 这是内部接口，无外部使用者。搜索确认只有 `internal/` 和 `cmd/` 引用

### Risk 2: 删除 vss.go 后丧失 HNSW 加速能力
**Risk:** 未来记忆量增大后，暴力向量搜索可能变慢
**Mitigation:** 当前记忆量级（<10K）下暴力搜索 <10ms。如需加速，可基于 `memory_embeddings` 表重新实现，不依赖 sqlite-vss

### Risk 3: 遗忘曲线改动可能引入 bug
**Risk:** 将 `memory_facts` 查询改为 `memory_index` 查询时，字段映射可能有误
**Mitigation:** 有现存的 `forgetting_curve_test.go`，更新测试用新表结构验证
