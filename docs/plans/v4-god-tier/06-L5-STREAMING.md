# L5 — Streaming Cognitive Pipeline (流式认知管道)

> 优先级: P1 | 工作量: 2-3 周 | 依赖: 无  
> 将同步阶段式认知循环改造为 channel-based 流式流水线，实现零等待体感。

---

## 一、当前 vs 目标

```
v3 (同步阶段):                          v4 (流式管道):
                                       
PERCEIVE ████████ (3s)                  PERCEIVE ──────────────────▶
         ↓                               ├─ 记忆检索完成 → 推入 planChan
PLAN     ████████████ (5s)               ├─ KB检索完成 → 推入 planChan  
         ↓                               └─ 上下文完成 → 关闭 planeChan
ACT      ██████████████████ (10s)        
         ↓                              PLAN ──────────────────────▶
OBSERVE  ████ (2s)                       ├─ 子任务1生成 → 推入 actChan
         ↓                               ├─ 子任务2生成 → 推入 actChan
REFLECT  ██████ (4s)                     └─ 全部生成 → 关闭 actChan
         ↓
用户看到  (等24秒 → 完整结果)            ACT ───────────────────────▶
                                          ├─ 工具1完成 → channel通知 → OBSERVE增量分析
                                          ├─ 工具2完成 → channel通知
                                          └─ 全部完成 → 关闭 obsChan

用户看到 (2秒 → 流开始 → 持续输出 → 18秒完成)
                                         总耗时相同，但体感完全不同
```

---

## 二、架构设计

```go
// internal/agent/streaming_pipeline.go

// StreamingPipeline 替换同步 5-phase 循环
type StreamingPipeline struct {
    perceiver  *StreamingPerceiver
    planner    *StreamingPlanner
    executor   *StreamingExecutor
    observer   *StreamingObserver
    reflector  *StreamingReflector
}

// PipelineChannels 管道间的通信 channel
type PipelineChannels struct {
    // PERCEIVE → PLAN: 增量上下文片段
    ContextChunks chan *ContextChunk

    // PLAN → ACT: 增量子任务
    SubtaskChunks chan *SubTask

    // ACT → OBSERVE: 工具执行结果
    ObservationChunks chan *Observation

    // OBSERVE → REFLECT: 断言和进度
    AssertionChunks chan *Assertion

    // 控制信号
    ErrorCh chan error
    DoneCh  chan struct{}
}

// ContextChunk 从一个语境源获取的增量语境片段
type ContextChunk struct {
    Source     string  // "memory", "knowledge", "graph", "project", "git"
    Content    string
    Priority   int     // 高优先级片段先发送
    IsLast     bool    // 是否是该源的最后一个片段
}
```

### 2.1 流式 PERCEIVE

```go
// internal/agent/streaming_perceive.go

func (sp *StreamingPerceiver) Stream(ctx context.Context, state *CognitiveState, out chan<- *ContextChunk) error {
    defer close(out)

    var wg sync.WaitGroup

    // 每个语境源独立 goroutine 检索
    sources := []struct {
        name string
        fn   func(context.Context, *CognitiveState, chan<- *ContextChunk)
    }{
        {"memory", sp.streamMemory},
        {"knowledge", sp.streamKnowledge},
        {"graph", sp.streamGraph},
        {"project", sp.streamProject},
        {"git", sp.streamGit},
    }

    for _, src := range sources {
        wg.Add(1)
        go func(name string, fn func(context.Context, *CognitiveState, chan<- *ContextChunk)) {
            defer wg.Done()
            localCh := make(chan *ContextChunk, 8)
            go func() {
                fn(ctx, state, localCh)
                close(localCh)
            }()
            // 转发到主输出 channel，按优先级排序
            for chunk := range localCh {
                select {
                case out <- chunk:
                case <-ctx.Done():
                    return
                }
            }
        }(src.name, src.fn)
    }

    wg.Wait()
    return nil
}

// streamMemory 流式检索记忆
func (sp *StreamingPerceiver) streamMemory(ctx context.Context, state *CognitiveState, out chan<- *ContextChunk) {
    results, err := sp.cortex.Search(ctx, state.UserMessage, SearchOptions{Limit: 20})
    if err != nil {
        return
    }
    // 高优先级记忆先发送
    // PLAN 可以尽快开始，不需要等所有记忆都检索完
    for i, mem := range results.Memories {
        out <- &ContextChunk{
            Source:   "memory",
            Content:  mem.Content,
            Priority: 20 - i,  // 前几个最高优先级
            IsLast:   i == len(results.Memories)-1,
        }
    }
}
```

### 2.2 流式 PLAN

```go
// internal/agent/streaming_plan.go

func (sp *StreamingPlanner) Stream(ctx context.Context, state *CognitiveState, contextCh <-chan *ContextChunk, out chan<- *SubTask) error {
    defer close(out)

    // 1. 积累语境（持续消费 contextCh 直到关闭或超时）
    var accumulatedContext strings.Builder
    contextTimeout := time.After(5 * time.Second)

contextLoop:
    for {
        select {
        case chunk, ok := <-contextCh:
            if !ok {
                break contextLoop  // PERCEIVE 完成
            }
            accumulatedContext.WriteString(chunk.Content)
            accumulatedContext.WriteString("\n")
        case <-contextTimeout:
            break contextLoop  // 等够了，开始规划
        case <-ctx.Done():
            return ctx.Err()
        }
    }

    // 2. 调用 LLM 生成计划（流式）
    // 关键：LLM 每生成一个子任务，立即发送
    planPrompt := sp.buildPlanPrompt(state, accumulatedContext.String())

    stream, err := sp.llm.Stream(ctx, CompletionRequest{
        System: planPrompt,
        Messages: []CompletionMessage{{Role: "user", Content: state.UserMessage}},
        MaxTokens: 4096,
    })
    if err != nil {
        return err
    }

    // 3. 增量解析 JSON
    // 边接收 token 边尝试解析子任务
    // 解析到一个完整子任务 → 立即发送到 out channel
    parser := NewIncrementalJSONParser()
    for {
        delta, err := stream.Next()
        if err != nil || delta.Done {
            break
        }
        parser.Feed(delta.Text)

        // 尝试提取完整的子任务对象
        for _, subtask := range parser.ExtractCompleteObjects() {
            out <- subtask
            // 此时 EXECUTOR 就可以开始执行这个子任务了！
            // 不需要等所有子任务都生成完
        }
    }

    // 发送剩余未完成的子任务
    for _, subtask := range parser.Finalize() {
        out <- subtask
    }

    return nil
}

// IncrementalJSONParser 增量 JSON 解析器
// 边接收 LLM 输出边提取完整对象
type IncrementalJSONParser struct {
    buffer strings.Builder
    depth  int
    inString bool
}

func (p *IncrementalJSONParser) Feed(chunk string) {
    p.buffer.WriteString(chunk)
}

func (p *IncrementalJSONParser) ExtractCompleteObjects() []*SubTask {
    // 在 buffer 中寻找完整的 JSON 对象（括号匹配）
    // 找到 → 解析 → 从 buffer 移除
    // 找不到 → 等待更多数据
    // ...
}
```

### 2.3 流式 EXECUTOR

```go
// internal/agent/streaming_execute.go

func (se *StreamingExecutor) Stream(
    ctx context.Context,
    subtaskCh <-chan *SubTask,
    obsCh chan<- *Observation,
) error {
    defer close(obsCh)

    // 拓扑感知的并发执行
    // 子任务的依赖关系决定了执行顺序
    // 但无依赖的子任务可以立即并发执行

    ready := make(chan *SubTask, 16)
    var inflight sync.WaitGroup

    // 拓扑调度器 goroutine
    go func() {
        defer close(ready)
        pending := make(map[string]*SubTask)
        completed := make(map[string]bool)

        for st := range subtaskCh {
            pending[st.ID] = st

            // 检查哪些子任务的所有依赖已满足
            for _, st := range pending {
                if completed[st.ID] {
                    continue
                }
                depsMet := true
                for _, depID := range st.DependsOn {
                    if !completed[depID] {
                        depsMet = false
                        break
                    }
                }
                if depsMet {
                    ready <- st
                }
            }
        }

        // 等待所有 inflight 完成
        inflight.Wait()
    }()

    // 执行 worker pool
    maxParallel := se.cfg.MaxParallelTools
    sem := make(chan struct{}, maxParallel)

    for st := range ready {
        st := st
        sem <- struct{}{}
        inflight.Add(1)
        go func() {
            defer func() { <-sem; inflight.Done() }()
            obs := se.executeSubTask(ctx, st)
            obsCh <- obs

            // 每完成一个子任务，立即通知 OBSERVER
            // OBSERVER 边收到结果边分析
        }()
    }

    return nil
}
```

---

## 三、用户体验对比

```
v3 同步模式体验：
  用户: "帮我重构这个模块"
  [等待 3s]
  [等待 5s]  ← 不知道在干什么
  [等待 10s] ← 怀疑卡住了
  [等待 2s]
  [等待 4s]
  Agent: "重构完成，修改了 12 个文件..."

v4 流式模式体验：
  用户: "帮我重构这个模块"
  [0.5s] "正在分析项目结构..."
  [1.0s] "找到 12 个相关文件"
  [2.0s] "计划: 1. 提取接口 2. 迁移调用 3. 更新测试"
  [3.0s] "执行 1/3: 提取接口... ✅"
  [8.0s] "执行 2/3: 迁移调用... (3/12 文件)"
  [12.0s] "执行 2/3: 迁移调用... ✅"
  [14.0s] "执行 3/3: 更新测试... ✅"
  [15.0s] "检查结果... 所有测试通过"
  [16.0s] "重构完成。修改摘要: ..."
```

---

## 四、实现要点

### 4.1 管道接口

```go
// Stage 是管道中的一个阶段
type Stage[In any, Out any] interface {
    Stream(ctx context.Context, in <-chan In, out chan<- Out) error
}

// Pipeline 组装多个阶段
type Pipeline struct {
    stages []Stage
}

func (p *Pipeline) Run(ctx context.Context) error {
    // 创建 channels
    ch1 := make(chan *ContextChunk, 16)
    ch2 := make(chan *SubTask, 16)
    ch3 := make(chan *Observation, 16)
    ch4 := make(chan *Assertion, 16)

    // 启动各阶段 goroutine
    go p.perceiver.Stream(ctx, state, ch1)
    go p.planner.Stream(ctx, ch1, ch2)
    go p.executor.Stream(ctx, ch2, ch3)
    go p.observer.Stream(ctx, ch3, ch4)
    go p.reflector.Stream(ctx, ch4, nil)

    // 等待完成或首个错误
    return p.waitForCompletion()
}
```

### 4.2 向后兼容

```go
// cognitive.go 中保留同步接口但内部使用流式管道
func (ca *CognitiveAgent) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
    if ca.cfg.Cognitive.StreamingEnabled {
        return ca.handleStreaming(ctx, ch, msg)
    }
    return ca.handleSync(ctx, ch, msg)  // 保留旧的同步路径
}
```

---

## 五、验收标准

1. **首字节时间**: 用户发消息后 < 2 秒开始看到有意义输出
2. **持续输出**: 工具执行结果边完成边展示，不等全部完成
3. **取消支持**: context 取消后所有 goroutine 在 < 1s 内退出
4. **内存安全**: 管道不会因生产快于消费导致内存无限增长（buffer 上限 + backpressure）
5. **成功率不变**: 流式管道的任务成功率不低于同步版本
