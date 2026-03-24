# 向量搜索性能优化 - 实现总结

## 已完成的优化

### 1. HNSW 索引支持 (sqlite-vss)

**文件**: `internal/memory/vss.go`

- 实现 `VSSIndexer` 管理 HNSW 索引
- 支持 `memory_facts` 和 `kb_chunks` 表的索引
- 自动检测 sqlite-vss 扩展可用性
- 优雅降级到暴力搜索（如果扩展不可用）

**关键方法**:
- `CreateMemoryFactsIndex()` - 创建记忆索引
- `CreateKBChunksIndex()` - 创建知识库索引
- `SearchMemoryFacts()` - HNSW 加速搜索
- `IndexNewFact()` / `IndexNewChunk()` - 增量索引更新

### 2. Embedding 缓存层

**文件**: `internal/memory/cache.go`

- 实现 `EmbeddingCache` - LRU 缓存 + TTL 过期
- `CachedEmbedder` - 包装任意 `EmbeddingProvider`
- SHA256 缓存键（text + model）
- 自动淘汰过期条目

**预期收益**:
- 减少 40-60% embedding API 调用
- 降低查询延迟（避免网络往返）
- 节省 API 费用

### 3. 查询结果缓存

**文件**:
- `internal/memory/cache.go` - `SearchResultCache`
- `internal/knowledge/cache.go` - `KnowledgeSearchCache`

- 缓存完整搜索结果（包括 RRF 融合后）
- 自动失效策略（新增/更新/删除时清除）
- 独立的 TTL 和大小限制

### 4. 集成到现有系统

**更新的文件**:
- `internal/memory/sqlite_store.go` - 集成 VSS + 缓存
- `internal/knowledge/store.go` - 集成缓存
- `internal/config/config.go` - 新增配置选项
- `internal/gateway/gateway.go` - 初始化优化组件
- `configs/ironclaw.example.yaml` - 配置示例

**新增配置项**:

```yaml
memory:
  enable_vss: true              # HNSW 索引
  vector_dimension: 1536        # 向量维度
  enable_search_cache: true     # 搜索缓存
  search_cache_size: 500        # 缓存大小
  search_cache_ttl: 5m          # 缓存 TTL

knowledge:
  enable_search_cache: true
  search_cache_size: 500
  search_cache_ttl: 5m
```

## 性能基准测试

**文件**: `internal/memory/benchmark_test.go`

运行基准测试：

```bash
cd internal/memory.md
CGO_ENABLED=1 go test -tags fts5 -bench=. -benchmem
```

预期结果示例：

```
BenchmarkVectorSearch/BruteForce_1000-8      100    12.5 ms/op
BenchmarkVectorSearch/WithCache_1000-8      1000     1.2 ms/op
BenchmarkVectorSearch/WithVSS_1000-8        5000     0.3 ms/op
BenchmarkVectorSearch/FullOptimized_1000-8  10000    0.1 ms/op
```

## 文档

**文件**: `docs/PERFORMANCE_OPTIMIZATION.md`

完整的性能优化指南，包括：
- 安装 sqlite-vss 扩展
- 配置最佳实践
- 性能调优建议
- 故障排查

## 使用方法

### 1. 安装 sqlite-vss（可选，用于 HNSW 索引）

```bash
# macOS
brew install sqlite-vss

# Linux
git clone https://github.com/asg017/sqlite-vss
cd sqlite-vss && make loadable
sudo cp dist/vss0.so /usr/local/lib/
```

### 2. 更新配置

编辑 `configs/ironclaw.yaml`:

```yaml
memory:
  enabled: true
  embedding_model: text-embedding-3-small
  openai_api_key: ${OPENAI_API_KEY}

  # 启用所有优化
  enable_vss: true
  vector_dimension: 1536
  enable_search_cache: true
  search_cache_size: 1000
  search_cache_ttl: 10m
```

### 3. 重新构建并运行

```bash
make build
make run
```

查看日志确认优化已启用：

```
INFO memory: sqlite-vss detected version=v0.1.2
INFO memory: HNSW index created for memory_facts
INFO memory: embedding cache enabled
INFO memory: search result cache enabled size=1000 ttl=10m0s
```

## 性能提升预期

| 数据规模 | 优化前 | 优化后 | 提升 |
|---------|-------|-------|------|
| 1万条   | 50ms  | 8ms   | 6×   |
| 10万条  | 500ms | 12ms  | 41×  |
| 100万条 | 5s    | 20ms  | 250× |

## 架构决策

### 为什么选择 sqlite-vss？

1. **零依赖** - SQLite 扩展，无需额外服务
2. **成熟稳定** - 基于 Faiss，经过生产验证
3. **易于部署** - 单个 .so 文件
4. **性能优秀** - HNSW 算法，O(log n) 查询

### 为什么使用两层缓存？

1. **Embedding 缓存** - 减少 API 调用（最昂贵）
2. **结果缓存** - 减少数据库查询（次昂贵）
3. **分层失效** - 结果缓存在数据变化时失效，embedding 缓存保留

### 优雅降级策略

- sqlite-vss 不可用 → 暴力搜索
- FTS5 不可用 → LIKE 查询
- 缓存失效 → 重新计算
- 所有优化都是可选的，不影响核心功能

## 后续优化方向

### 短期（已实现）
- ✅ HNSW 索引
- ✅ Embedding 缓存
- ✅ 查询结果缓存

### 中期（建议）
- [ ] 量化压缩（PQ/SQ）- 减少内存占用
- [ ] 批量 embedding - 提高吞吐量
- [ ] 异步索引更新 - 减少写入延迟

### 长期（探索）
- [ ] 分布式索引 - 支持超大规模
- [ ] GPU 加速 - Faiss GPU 版本
- [ ] 混合精度 - FP16/INT8 量化

## 测试覆盖

- ✅ 单元测试 - `benchmark_test.go`
- ✅ 缓存命中率测试
- ✅ 失效策略测试
- ⚠️ 集成测试 - 需要手动验证
- ⚠️ 压力测试 - 需要大规模数据集

## 已知限制

1. **sqlite-vss 依赖** - 需要手动安装扩展
2. **内存占用** - 缓存会增加内存使用
3. **冷启动** - 首次查询需要构建索引
4. **精度损失** - HNSW 是近似算法（~98% 召回率）

## 参考资料

- [sqlite-vss GitHub](https://github.com/asg017/sqlite-vss)
- [HNSW 论文](https://arxiv.org/abs/1603.09320)
- [Faiss Wiki](https://github.com/facebookresearch/faiss/wiki)
- [向量数据库优化实践](https://getathenic.com/blog/vector-database-optimization-production)
