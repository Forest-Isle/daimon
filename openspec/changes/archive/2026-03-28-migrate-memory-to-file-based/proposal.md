## Why

IronClaw 的记忆系统当前使用 SQLite 作为主存储，存在双表冗余（`memories` + `memory_facts`）、17个文件的复杂架构、以及缺乏人类可读性的问题。迁移到 Claude Code 的文件优先架构（Markdown 文件 + SQLite 索引）可以实现：Git 友好的版本控制、用户可编辑的记忆、更清晰的代码结构，同时保持高性能搜索能力。

## What Changes

- **BREAKING**: 将主存储从 SQLite 迁移到 `~/.ironclaw/memory/*.md` Markdown 文件
- **BREAKING**: 删除 `memories` 表，SQLite 仅作为搜索索引（FTS5 + 向量）
- 新增文件结构：`MEMORY.md`（索引）+ 按作用域分类的 Markdown 文件（user/session/feedback/global）
- 简化缓存层：从 3 层（EmbeddingCache + CachedEmbedder + SearchResultCache）合并为 1 层
- 启用遗忘曲线：集成 `ForgettingCurveManager` 到搜索排序和后台清理
- 合并冲突解决逻辑：将 `conflict_resolver.go` 的功能整合到 `lifecycle.go`
- 代码精简：从 17 个文件减少到 9 个核心文件
- 提供数据迁移工具：`ironclaw memory migrate` 命令将现有 SQLite 数据导出为 Markdown

## Capabilities

### New Capabilities
- `file-memory-storage`: Markdown 文件作为主存储，支持 YAML frontmatter 元数据和人类可读内容
- `memory-index`: SQLite 作为辅助索引，提供 FTS5 全文搜索和向量搜索能力
- `memory-file-structure`: 定义 `~/.ironclaw/memory/` 的目录结构和文件命名规范
- `forgetting-curve-integration`: 基于时间衰减和访问频率的记忆强度计算，影响搜索排序和自动归档
- `memory-migration`: 从旧 SQLite 双表结构迁移到新文件结构的工具

### Modified Capabilities
- `memory-lifecycle`: 合并冲突检测逻辑，统一 ADD/UPDATE/DELETE/NOOP 决策流程
- `memory-search`: 搜索流程改为：解析 MEMORY.md 索引 → SQL 索引查询 → 读取 Markdown 文件内容
- `memory-consolidation`: 从数据库记录提升改为文件整理（session 文件夹 → user 文件夹）

## Impact

**代码变更：**
- 删除文件：`sqlite_store.go`, `embeddings_db.go`, `txlog.go`, `migrator.go`, `metadata.go`, `conflict_resolver.go`, `store.go`（7个）
- 新增文件：`file_memory.go`（核心文件存储实现）
- 重构文件：`markdown.go`, `chunker.go`, `index.go`, `lifecycle.go`, `consolidator.go`, `forgetting_curve.go`, `cache.go`（7个）
- 保留文件：`facts.go`, `embedding.go`（2个）

**数据库变更：**
- 删除表：`memories`, `memory_facts`
- 新增表：`memory_index`（文件路径 → 元数据映射）, `memory_fts`（全文搜索索引）, `memory_embeddings`（向量索引）
- 保留表：`fact_access_log`, `fact_access_stats`（用于遗忘曲线）

**API 变更：**
- `Store` 接口方法签名保持不变，但实现从数据库读写改为文件读写
- 新增 CLI 命令：`ironclaw memory migrate`, `ironclaw memory export`, `ironclaw memory import`

**依赖变更：**
- 无新增外部依赖
- 移除对 `memory_facts` 表的所有引用

**向后兼容性：**
- **BREAKING**: 现有 SQLite 数据需要通过迁移工具转换
- 提供自动迁移脚本，在首次启动时检测旧数据并提示迁移
