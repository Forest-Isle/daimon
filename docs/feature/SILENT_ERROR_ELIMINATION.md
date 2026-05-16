# 静默错误消除：审计日志、FTS 索引、文件写入

**日期**: 2026-05-17
**范围**: 消除 memory 和 knowledge 包中所有静默错误丢弃、修复 PDF Ingester 误导性实现

## 概述

项目中有多处在生产路径上使用 `_, _ =` 静默丢弃错误的代码。当这些操作失败时（DB 写入失败、FTS 索引更新失败、文件写入失败），没有任何日志或告警，问题完全不可见。本次改动对所有这些点进行了系统性修复，同时顺手修正了 PDF Ingester 的误导性 `CanHandle` 实现。

## 审计日志与访问日志

### 修复前

```go
// audit.go:27
func (al *AuditLogger) Log(ctx context.Context, entry AuditEntry) {
    _, _ = al.db.ExecContext(ctx, `
        INSERT INTO audit_logs (event, user_id, session_id, details, created_at)
        VALUES (?, ?, ?, ?, ?)
    `, entry.Event, entry.UserID, entry.SessionID, entry.Details, time.Now())
}

// access_log.go:46
_, _ = al.db.ExecContext(ctx, `
    INSERT INTO access_logs (memory_id, user_id, accessed_at)
    VALUES (?, ?, ?)
`, id, userID, time.Now())
```

### 修复后

```go
if _, err := al.db.ExecContext(ctx, `
    INSERT INTO audit_logs ...
`, ...); err != nil {
    slog.Warn("memory: audit log write failed", "err", err)
}
```

审计日志和访问日志写入失败现在至少会记录一条 Warn 级别日志，不会完全悄无声息地丢失。

## FTS 全文索引写入

### 修复前

`file_store.go` 中有 3 处 FTS 索引操作使用 `_, _` 丢弃错误：

```go
// DELETE FTS entry
_, _ = s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, id)
// INSERT FTS entry
_, _ = s.db.ExecContext(ctx, `INSERT INTO memory_fts (memory_id, content) VALUES (?, ?)`, id, content)
```

### 修复后

```go
if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_fts WHERE memory_id = ?`, id); err != nil {
    slog.Warn("memory: FTS index update failed", "err", err)
    return fmt.Errorf("update memory_fts: %w", err)
}
```

FTS 索引更新失败时会记录 Warn 日志并返回错误给调用方。对于 DELETE 操作，返回错误可以让调用方知道索引可能与文件系统状态脱节。

## Embedding 索引写入

### 修复前

```go
embBytes := serializeEmbedding(embedding)
_, _ = s.db.ExecContext(ctx, `
    INSERT INTO memory_embeddings (memory_id, embedding, dimension)
    VALUES (?, ?, ?)
    ON CONFLICT(memory_id) DO UPDATE SET embedding = excluded.embedding
`, id, embBytes, len(embedding))
```

### 修复后

```go
embBytes := serializeEmbedding(embedding)
if _, execErr := s.db.ExecContext(ctx, `
    INSERT INTO memory_embeddings ...
`, id, embBytes, len(embedding)); execErr != nil {
    slog.Warn("memory: embedding index update failed", "err", execErr)
}
```

注意：embedding 写入失败仅记录 Warn 而不返回 error，因为 embedding 是辅助功能（提升搜索精度），其失败不应阻塞主流程。

## MEMORY.md 索引文件写入

### 修复前

`memory_index.go` 中 `WriteMemoryIndex` 函数使用 `fmt.Fprintf(w, ...)` 向真实文件 writer 写入，所有错误被 `_, _` 丢弃：

```go
func (s *FileMemoryStore) WriteMemoryIndex() error {
    w := bufio.NewWriter(f)
    _, _ = fmt.Fprintf(w, "# Memory Index\n\n")
    _, _ = fmt.Fprintf(w, "Last updated: %s\n\n", time.Now().Format(time.RFC3339))
    for scope, entries := range byScope {
        _, _ = fmt.Fprintf(w, "## %s\n\n", scopeTitles[scope])
        for _, entry := range entries {
            _, _ = fmt.Fprintf(w, "- [%s](%s) — %s\n", ...)
        }
    }
    // 连 Flush 的错误也忽略了
}
```

### 修复后

```go
func (s *FileMemoryStore) WriteMemoryIndex() error {
    w := bufio.NewWriter(f)
    if _, err := fmt.Fprintf(w, "# Memory Index\n\n"); err != nil {
        return fmt.Errorf("write index: %w", err)
    }
    // ... 所有 Fprintf 调用改为 if err check + return
    if err := w.Flush(); err != nil {
        return fmt.Errorf("write index: %w", err)
    }
    return nil
}
```

文件写入错误现在会正确传播到调用方，不再静默产生损坏的索引文件。

## PDF Ingester 误导性修复

### 修复前

```go
func (p *PDFIngester) CanHandle(sourceType string) bool {
    return sourceType == "pdf"  // 声称能处理
}

func (p *PDFIngester) Extract(_ context.Context, uri string) (string, string, error) {
    return "", "", fmt.Errorf("PDF ingestion not supported yet")  // 实际不能处理
}
```

当用户注册 PDF source 时，`CanHandle` 返回 true 导致系统认为 PDF 可被摄取，但实际调用 `Extract` 时立即报错。这是一个典型的误导性承诺。

### 修复后

```go
// CanHandle returns false until PDF ingestion is implemented.
func (p *PDFIngester) CanHandle(_ string) bool {
    return false
}
```

`CanHandle` 现在诚实地返回 false。当 PDF 摄取真正实现时（建议使用 `pdfcpu`），再改回 true。`Extract` 方法保留不动（包含对开发者的提示性错误消息）。

## 不改的部分

以下模式保持不变，因为它们在实践中不会失败：

- `fmt.Fprintf(&strings.Builder{})` — 向 `strings.Builder` 写入在 Go 中永远不会返回 error
- `fmt.Fprintf(&bytes.Buffer{})` — 同上
- `_ = resp.Body.Close()` — defer 中的 Close 在读取完成后是安全的

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/memory/audit.go` | 修改 | 审计日志写入 → slog.Warn |
| `internal/memory/access_log.go` | 修改 | 访问日志写入 → slog.Warn |
| `internal/memory/file_store.go` | 修改 | FTS DELETE/INSERT + Embedding INSERT → slog.Warn + error 传播 |
| `internal/memory/memory_index.go` | 修改 | fmt.Fprintf 文件写入 → error 传播 + Flush 错误检查 |
| `internal/knowledge/ingest/pdf.go` | 修改 | CanHandle 改为返回 false |

## 验收清单

- [x] `audit.go` 和 `access_log.go` 的 `_, _ = al.db.ExecContext` 全部清除
- [x] `file_store.go` 的 FTS/Embedding 操作全部改为显式错误处理
- [x] `memory_index.go` 的文件写入全部改为 error 传播
- [x] PDF Ingester 不再误导用户
- [x] 零业务逻辑变更
- [x] `gofmt` 通过
