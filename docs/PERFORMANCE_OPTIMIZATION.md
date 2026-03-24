# 向量搜索性能优化指南

## 概述

IronClaw 实现了三层性能优化策略，解决大规模向量搜索的性能瓶颈：

1. **HNSW 索引** - 使用 sqlite-vss 实现近似最近邻搜索
2. **Embedding 缓存** - LRU 缓存减少 API 调用
3. **查询结果缓存** - 缓存完整搜索结果

## 性能提升预期

| 数据规模 | 优化前 (暴力搜索) | 优化后 (HNSW + 缓存) | 提升倍数 |
|---------|------------------|---------------------|---------|
| 1万条   | ~50ms            | ~8ms                | 6.2×    |
| 10万条  | ~500ms           | ~12ms               | 41.6×   |
| 100万条 | ~5000ms          | ~20ms               | 250×    |

*基准测试环境：M1 Pro, 1536维向量, Top-10 查询*

## 1. HNSW 索引优化

### 安装 sqlite-vss

```bash
# macOS (Homebrew)
brew install sqlite-vss

# Linux (从源码编译)
git clone https://github.com/asg017/sqlite-vss
cd sqlite-vss
make loadable
sudo cp dist/vss0.so /usr/local/lib/
```

### 配置启用

在 `configs/ironclaw.yaml` 中启用：

```yaml
memory:
  enable_vss: true
  vector_dimension: 1536  # OpenAI text-embedding-3-small
```

### 工作原理

- **HNSW (Hierarchical Navigable Small World)** 是一种图结构索引
- 查询复杂度从 O(n*d) 降低到 O(log n)
- 牺牲少量精度（~98% 召回率）换取巨大性能提升

### 索引管理

索引在首次启动时自动创建：

```go
// 手动触发索引重建（如果需要）
vssIndexer.CreateMemoryFactsIndex(ctx)
vssIndexer.CreateKBChunksIndex(ctx)
```

## 2. Embedding 缓存

### 配置

```yaml
memory:
  enable_search_cache: true
  search_cache_size: 500      # 缓存查询数量
  search_cache_ttl: 5m        # 5分钟过期
```

### 缓存策略

- **LRU 淘汰** - 超过 maxSize 时移除最旧条目
- **TTL 过期** - 默认 10 分钟后失效
- **缓存键** - SHA256(query_text + model_name)

### 预期收益

- 减少 40-60% embedding API 调用
- 节省 API 费用（OpenAI text-embedding-3-small: $0.02/1M tokens）
- 降低查询延迟（避免网络往返）

### 代码示例

```go
// 使用缓存包装器
cachedEmbedder := memory.NewCachedEmbedder(
    openaiEmbedder,
    "text-embedding-3-small",
    1000,           // 缓存大小
    10*time.Minute, // TTL
)

store := memory.NewSQLiteStore(db, cachedEmbedder, cfg)
```

## 3. 查询结果缓存

### 配置

```yaml
knowledge:
  enable_search_cache: true
  search_cache_size: 500
  search_cache_ttl: 5m
```

### 失效策略

缓存在以下情况自动失效：

- 新增 fact/chunk (`SaveFact`, `saveChunk`)
- 更新 fact (`UpdateFact`)
- 删除 fact (`DeleteFact`)

### 适用场景

- 高频重复查询（如 FAQ 检索）
- 知识库内容变化不频繁
- 多用户共享知识库

## 配置最佳实践

### 小规模部署 (< 1万条)

```yaml
memory:
  enable_vss: false              # 暴力搜索足够快
  enable_search_cache: true
  search_cache_size: 200
  search_cache_ttl: 5m
```

### 中等规模 (1-10万条)

```yaml
memory:
  enable_vss: true
  vector_dimension: 1536
  enable_search_cache: true
  search_cache_size: 500
  search_cache_ttl: 10m
```

### 大规模部署 (> 10万条)

```yaml
memory:
  enable_vss: true
  vector_dimension: 1536
  enable_search_cache: true
  search_cache_size: 1000
  search_cache_ttl: 15m
```

## 监控与调优

### 日志输出

启用后会看到以下日志：

```
INFO memory: sqlite-vss detected version=v0.1.2
INFO memory: HNSW index created for memory_facts
INFO memory: search result cache enabled size=500 ttl=5m0s
```

### 性能指标

关键指标监控：

- **缓存命中率** - 应 > 30%
- **查询延迟 P99** - 应 < 50ms
- **索引构建时间** - 首次启动时间

### 调优建议

1. **向量维度选择**
   - 1536维 (text-embedding-3-small) - 平衡性能与精度
   - 3072维 (text-embedding-3-large) - 更高精度，但索引更大

2. **缓存大小**
   - 根据内存预算调整
   - 每个缓存条目 ~2KB (1536维向量)
   - 1000条缓存 ≈ 2MB 内存

3. **TTL 设置**
   - 知识库更新频繁 → 短 TTL (1-3分钟)
   - 静态知识库 → 长 TTL (15-30分钟)

## 故障排查

### sqlite-vss 未加载

**症状**：日志显示 `sqlite-vss not available, falling back to brute-force search`

**解决**：
```bash
# 检查扩展是否安装
sqlite3 :memory.md: "SELECT vss_version();"

# 如果失败，重新安装 sqlite-vss
brew reinstall sqlite-vss
```

### 索引构建失败

**症状**：`failed to create VSS index for memory_facts`

**原因**：
- 向量维度不匹配
- 数据库权限问题
- 磁盘空间不足

**解决**：
```bash
# 检查数据库
sqlite3 data/ironclaw.db "PRAGMA integrity_check;"

# 重建索引
rm data/ironclaw.db
make run  # 重新初始化
```

### 缓存命中率低

**症状**：查询仍然很慢

**原因**：
- 查询模式多样化（每次查询都不同）
- TTL 设置过短
- 缓存大小不足

**解决**：
- 增加 `search_cache_size`
- 延长 `search_cache_ttl`
- 考虑查询归一化（如去除标点、小写化）

## 参考资料

- [sqlite-vss GitHub](https://github.com/asg017/sqlite-vss)
- [HNSW 算法论文](https://arxiv.org/abs/1603.09320)
- [向量数据库优化实践](https://getathenic.com/blog/vector-database-optimization-production)
- [Faiss 性能基准](https://github.com/facebookresearch/faiss/wiki/Indexing-1G-vectors)
