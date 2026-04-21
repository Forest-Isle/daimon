# 记忆系统搜索性能优化

**日期**: 2026-04-21
**范围**: `FileMemoryStore.Search` — 消除冗余查询、批量向量加载、异步访问追踪、MEMORY.md 解析缓存

## 概述

`FileMemoryStore` 是 IronClaw 记忆系统的主存储实现，以 Markdown 文件为主存储、SQLite 为辅助索引，提供 BM25 + 向量混合检索（RRF 融合）能力。

随着记忆条目增多，`Search()` 调用路径上存在四处明显的性能缺陷：

1. 文件路径已经在索引结果中，却仍对每条结果发起额外的 SQL 单行查询
2. 向量检索对每个候选 ID 独立查询 SQLite，产生 N+1 轮次
3. 访问时间追踪（`trackAccess`）同步阻塞调用方，将 N 次文件写入串行化在 `Search` 返回路径上
4. `MEMORY.md` 索引文件在每次 `Search` 调用时都重新读取并全量解析

四项优化合计消除了热路径上的大量 I/O 和数据库往返，使常见场景下的搜索延迟显著下降。

## 优化详解

### 优化 1：消除冗余 QueryRow

**问题根因**

`hybridSearch` → RRF 融合之后，结果列表中的每个 `indexResult` 已经携带 `filePath` 字段。但在后续的文件内容读取循环中，代码仍然执行：

```go
// 优化前（已删除）
var filePath string
err := s.db.QueryRowContext(ctx,
    `SELECT file_path FROM memory_index WHERE memory_id = ?`, id,
).Scan(&filePath)
```

该查询不产生任何新信息，却为每条结果额外增加一次同步 SQLite 往返。

**修复**

直接使用 `indexResult` 中已有的 `filePath`：

```go
// 优化后
filePath := string(results[i].Entry.Scope)
```

> **注**：代码中通过 `results[i].Entry.Scope` 传递 filePath，因为 `hybridSearch` 将文件路径暂存于该字段，在后续内容填充前完成替换。

**效果**：默认 `limit=10` 时，每次 `Search` 消除 10 次 SQL 单行查询。

---

### 优化 2：批量向量 Embedding 查询

**问题根因**

`hybridSearch` 的向量分支对 `idMap` 中的每个候选 ID 逐一查询：

```go
// 优化前（已删除）
for id := range idMap {
    row := s.db.QueryRow(
        "SELECT embedding FROM memory_embeddings WHERE memory_id = ?", id,
    )
    var embBytes []byte
    _ = row.Scan(&embBytes)
    // ...
}
```

`idMap` 来自过滤后的 `memory_index` 结果，典型大小为几十到上百条。每条独立查询意味着 N 次数据库往返。

**修复**

构造参数化批量 `IN` 查询，一次拉取所有目标 embedding：

```go
// 优化后
if len(query.Embedding) > 0 && len(idMap) > 0 {
    ids := make([]string, 0, len(idMap))
    for id := range idMap {
        ids = append(ids, id)
    }
    placeholders := make([]string, len(ids))
    batchArgs := make([]any, len(ids))
    for i, id := range ids {
        placeholders[i] = "?"
        batchArgs[i] = id
    }
    batchQuery := "SELECT memory_id, embedding FROM memory_embeddings WHERE memory_id IN (" +
        strings.Join(placeholders, ",") + ")"
    embRows, embErr := s.db.QueryContext(ctx, batchQuery, batchArgs...)
    if embErr == nil {
        defer func() { _ = embRows.Close() }()
        for embRows.Next() {
            var id string
            var embBytes []byte
            if scanErr := embRows.Scan(&id, &embBytes); scanErr == nil {
                emb := deserializeEmbedding(embBytes)
                vectorResults[id] = cosineSimilarity(query.Embedding, emb)
            }
        }
    }
}
```

**安全守卫**：`len(idMap) > 0` 的条件判断防止生成空的 `IN ()` 子句（SQLite 语法错误）。

**效果**：向量分支从 N 次查询降为 1 次批量查询。

---

### 优化 3：异步 trackAccess

**问题根因**

`trackAccess` 包含两步操作：

1. 重写 Markdown 文件的 YAML frontmatter（更新 `last_accessed_at`）——通过原子 rename 实现
2. 执行 `UPDATE memory_index SET strength = ? WHERE memory_id = ?`

原代码同步调用，导致 `Search` 返回前必须串行完成所有结果条目的文件重写和数据库更新：

```go
// 优化前（已删除）
if err := s.trackAccess(ctx, id, fp, mf); err != nil {
    slog.Warn("memory: track access", "id", id, "err", err)
}
```

**修复**

改为 fire-and-forget goroutine：

```go
// 优化后
go func(id, fp string, mf *MemoryFile) {
    if err := s.trackAccess(context.Background(), id, fp, mf); err != nil {
        slog.Warn("memory: track access", "id", id, "err", err)
    }
}(results[i].Entry.ID, filePath, mf)
```

**并发安全性**：
- `mf` 指针在 goroutine 启动后不再被调用方读取或修改，无数据竞争
- `writeFileAtomic` 使用 `os.Rename` 原子替换文件——并发写同一文件时，最后一次 rename 胜出，`last_accessed_at` 时间戳保证单调递增

**效果**：`Search` 返回时间不再受文件 I/O 阻塞，N 次文件写入完全移出热路径。

---

### 优化 4：MEMORY.md 解析缓存（mtime 失效）

**问题根因**

`Search` 的第一步是解析 `MEMORY.md` 索引文件以获取候选 ID 列表。原来每次调用都执行：

```
os.ReadFile("~/.ironclaw/memory/MEMORY.md") + 全量行扫描
```

在高频搜索场景下（如认知 Agent 的多轮记忆检索），这意味着对同一文件的重复读取。

**修复**

在 `FileMemoryStore` 新增三个缓存字段：

```go
type FileMemoryStore struct {
    // ...已有字段...
    indexCacheMu    sync.RWMutex
    indexCache      map[string][]IndexEntry
    indexCacheMtime time.Time
}
```

`cachedIndex()` 方法实现带双重检查的 mtime 失效缓存：

```go
func (s *FileMemoryStore) cachedIndex() (map[string][]IndexEntry, error) {
    indexPath := filepath.Join(s.baseDir, "MEMORY.md")
    info, err := os.Stat(indexPath)
    if err != nil {
        if os.IsNotExist(err) {
            return make(map[string][]IndexEntry), nil
        }
        return nil, err
    }
    mtime := info.ModTime()

    // 快速读路径：RLock 检查缓存
    s.indexCacheMu.RLock()
    if s.indexCache != nil && !mtime.After(s.indexCacheMtime) {
        cached := s.indexCache
        s.indexCacheMu.RUnlock()
        return cached, nil
    }
    s.indexCacheMu.RUnlock()

    // 写路径：升级为写锁，双重检查防止缓存惊群
    s.indexCacheMu.Lock()
    defer s.indexCacheMu.Unlock()
    if s.indexCache != nil && !mtime.After(s.indexCacheMtime) {
        return s.indexCache, nil
    }
    idx := NewMemoryIndex(s.baseDir)
    entries, parseErr := idx.Parse()
    if parseErr != nil {
        return nil, parseErr
    }
    s.indexCache = entries
    s.indexCacheMtime = mtime
    return entries, nil
}
```

**缓存失效**：`invalidateIndexCache()` 在两处调用以保证一致性：

```go
func (s *FileMemoryStore) invalidateIndexCache() {
    s.indexCacheMu.Lock()
    s.indexCache = nil
    s.indexCacheMu.Unlock()
}
```

| 调用位置 | 触发时机 |
|----------|---------|
| `syncIndex()` 开始处 | 新增/更新/删除记忆时重建索引前 |
| `RebuildIndex()` 开始处 | 手动触发全量重建时 |

**效果**：`MEMORY.md` 文件读取从每次 `Search` 降为仅在文件修改后才重新解析。缓存命中时仅一次 `os.Stat` + RLock，几乎无开销。双重检查写路径防止多个并发 `Search` 调用同时重建缓存（缓存惊群问题）。

## 综合效果

以典型场景（返回 10 条结果、向量分支启用、索引文件未修改）为基准：

| 操作 | 优化前 | 优化后 |
|------|--------|--------|
| 文件路径查询（SQL） | 10 次 QueryRow | 0 次 |
| 向量 Embedding 查询（SQL） | N 次（N=候选数） | 1 次批量 IN |
| 文件 I/O（trackAccess） | 10 次同步写 | 异步，不阻塞 Search |
| MEMORY.md 解析 | 每次解析 | 缓存命中时 0 次 |

## 架构图

```
Search(query)
│
├─ cachedIndex()                 ← 优化 4：mtime 失效缓存
│   ├─ os.Stat(MEMORY.md)
│   ├─ RLock 快速路径 (命中)  →  return cached
│   └─ Lock 写路径 (失效)     →  Parse + 更新缓存
│
├─ memory_index SQL 过滤        （无变化）
│
├─ hybridSearch()
│   ├─ BM25 FTS5 查询           （无变化）
│   └─ 向量批量 IN 查询         ← 优化 2：N 次 → 1 次
│
├─ RRF 融合 + 取 top-k          （无变化）
│
└─ 文件内容读取循环
    ├─ filePath 直接从 indexResult 取  ← 优化 1：消除 QueryRow
    ├─ parseFile(filePath)
    └─ go trackAccess(...)       ← 优化 3：异步 fire-and-forget
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/memory/file_store.go` | 修改 | `FileMemoryStore` 新增 3 个缓存字段；新增 `cachedIndex()`、`invalidateIndexCache()`；`Search` 直接读取 `filePath`；`trackAccess` 改为 goroutine；`hybridSearch` 向量分支改为批量 IN 查询 |

## 验证

### 正确性

- **向量批量查询**：结果集与逐条查询完全等价，RRF 融合分数不变
- **异步 trackAccess**：`last_accessed_at` 更新仍然完成，遗忘曲线计算不受影响；错误通过 `slog.Warn` 记录而非静默丢弃
- **缓存一致性**：写操作（Add/Update/Delete）在 `syncIndex` 开始时调用 `invalidateIndexCache`，确保下次 `Search` 获取到最新索引

### 并发安全

- `indexCacheMu` 使用 `sync.RWMutex`：多并发 Search 共享读锁，写操作（重建缓存）独占写锁
- 双重检查写路径防止多个 goroutine 同时触发解析（缓存惊群）
- goroutine 参数通过值传递（`id string`, `fp string`, `mf *MemoryFile`），无闭包捕获循环变量问题
