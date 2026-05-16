# L4 — LSP-Aware Code Engine (语言服务器感知代码引擎)

> 优先级: P5 | 工作量: 3-4 周 | 依赖: 无  
> 让 Agent 真正理解代码——不是文本相似度搜索，而是 AST 级别、类型感知、调用图遍历。

---

## 一、核心能力

| 能力 | 当前实现 | V4 目标 |
|------|---------|---------|
| 代码搜索 | 文本相似度 + 文件名匹配 | Tree-sitter 符号索引 + LSP 语义搜索 |
| 代码生成 | LLM 自由发挥 | LSP 补全辅助 + 类型约束 + 编译验证 |
| 代码修改 | 文本替换 (sed 级别) | AST 感知的安全重构 |
| 影响分析 | ❌ 不存在 | 调用图遍历 + 类型依赖分析 |
| 跨文件导航 | ❌ 不存在 | LSP go-to-definition / find-references |
| 实时诊断 | ❌ 不存在 | LSP diagnostics 实时反馈 |

```
┌──────────────────────────────────────────────────────┐
│              LSP-Aware Code Engine                   │
│                                                      │
│  ┌────────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │ LSP Client │  │ Tree-    │  │ Call Graph       │ │
│  │ gopls      │  │ sitter   │  │ Analyzer         │ │
│  │ rust-      │  │ Parser   │  │ 调用图分析器      │ │
│  │ analyzer   │  │ 语法树   │  │                  │ │
│  │ pyright    │  │ 解析器   │  │                  │ │
│  │ typescript │  │          │  │                  │ │
│  └─────┬──────┘  └────┬─────┘  └────────┬─────────┘ │
│        │              │                 │            │
│  ┌─────┴──────────────┴─────────────────┴─────────┐ │
│  │           Symbol Index (符号索引)               │ │
│  │   定义 | 引用 | 类型 | 文档 | 位置             │ │
│  └────────────────────┬───────────────────────────┘ │
│                       │                              │
│  ┌────────────────────┴───────────────────────────┐ │
│  │           Semantic Code Search (语义搜索)       │ │
│  │   "查找所有调用 UserService.Create 的地方"      │ │
│  └────────────────────┬───────────────────────────┘ │
│                       │                              │
│  ┌────────────────────┴───────────────────────────┐ │
│  │           Safe Refactoring (安全重构)           │ │
│  │   AST 级重命名 | 提取函数 | 移动符号            │ │
│  └────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘
```

---

## 二、LSP 客户端管理器

```go
// internal/code_engine/lsp_manager.go

// LSPManager 管理多个 LSP 服务器
type LSPManager struct {
    servers map[string]*LSPServer  // language → server
    index   *SymbolIndex
}

// LSPServer 封装一个 LSP 进程
type LSPServer struct {
    Language    string
    Command     string   // "gopls", "rust-analyzer", "pyright-langserver"
    Args        []string
    cmd         *exec.Cmd
    rpc         *jrpc2.Conn  // JSON-RPC 2.0 连接
    capabilities LSPCapabilities
    rootURI     string       // 项目根目录 URI
}

// StartServer 启动一个 LSP 服务器
func (m *LSPManager) StartServer(ctx context.Context, language, rootPath string) (*LSPServer, error) {
    configs := map[string]struct{ cmd string; args []string }{
        "go":     {"gopls", nil},
        "rust":   {"rust-analyzer", nil},
        "python": {"pyright-langserver", []string{"--stdio"}},
        "typescript": {"typescript-language-server", []string{"--stdio"}},
    }

    cfg, ok := configs[language]
    if !ok {
        return nil, fmt.Errorf("unsupported language: %s", language)
    }

    srv := &LSPServer{
        Language: language,
        Command:  cfg.cmd,
        Args:     cfg.args,
        rootURI:  pathToURI(rootPath),
    }

    // 启动进程，建立 stdio pipe
    srv.cmd = exec.CommandContext(ctx, cfg.cmd, cfg.args...)
    stdin, _ := srv.cmd.StdinPipe()
    stdout, _ := srv.cmd.StdoutPipe()
    srv.cmd.Start()

    // 建立 JSON-RPC 连接
    srv.rpc = jrpc2.NewConn(stdioStream{stdin, stdout})

    // Initialize 握手
    srv.initialize(ctx)

    // 触发首次分析
    srv.didOpen(ctx, rootPath)

    m.servers[language] = srv
    return srv, nil
}

// SendRequest 发送 LSP 请求并获取响应
func (s *LSPServer) SendRequest(ctx context.Context, method string, params interface{}, result interface{}) error {
    return s.rpc.Call(ctx, method, params, result)
}
```

---

## 三、语义代码搜索

```go
// internal/code_engine/semantic_search.go

// SemanticCodeSearch 语义代码搜索
// "查找所有调用 UserService.Create 的地方" → 返回精确的代码位置
func (m *LSPManager) SemanticSearch(ctx context.Context, language, query string) ([]*CodeMatch, error) {
    srv := m.servers[language]

    // 解析查询意图
    intent := m.parseSearchIntent(query)
    // intent: {type: "find-references", symbol: "UserService.Create"}
    // or: {type: "find-definition", symbol: "UserService"}
    // or: {type: "semantic", text: "authentication middleware"}

    switch intent.Type {
    case "find-references":
        // LSP textDocument/references
        params := &lsp.ReferenceParams{
            TextDocumentPositionParams: lsp.TextDocumentPositionParams{
                TextDocument: lsp.TextDocumentIdentifier{URI: intent.FileURI},
                Position:     lsp.Position{Line: intent.Line, Character: intent.Char},
            },
            Context: lsp.ReferenceContext{IncludeDeclaration: true},
        }
        var locations []lsp.Location
        srv.SendRequest(ctx, "textDocument/references", params, &locations)

        return m.locationsToMatches(locations), nil

    case "find-definition":
        // LSP textDocument/definition
        // ...

    case "semantic":
        // 回退: 用符号索引 + 嵌入搜索
        return m.hybridCodeSearch(ctx, intent.Text)
    }

    return nil, nil
}
```

---

## 四、Tree-sitter 符号索引

LSP 需要文件已打开在编辑器中。对于批量代码索引，用 Tree-sitter 更快，不需要启动 LSP。

```go
// internal/code_engine/tree_sitter.go

// TreeSitterIndex 基于 Tree-sitter 的符号索引
type TreeSitterIndex struct {
    parsers  map[string]*sitter.Parser  // language → parser
    symbols  *SymbolDB                   // 持久化符号数据库
}

// IndexFile 索引单个文件的符号
func (ts *TreeSitterIndex) IndexFile(ctx context.Context, filePath string) ([]*Symbol, error) {
    language := detectLanguage(filePath)
    parser := ts.parsers[language]

    content, _ := os.ReadFile(filePath)
    tree := parser.Parse(nil, content)

    // 遍历 AST，提取符号
    symbols := ts.extractSymbols(tree.RootNode(), content, filePath)

    // 存入符号数据库
    ts.symbols.BatchUpsert(ctx, symbols)

    return symbols, nil
}

// Symbol 代码符号
type Symbol struct {
    Name       string
    Kind       string  // function, method, class, interface, variable, constant
    FilePath   string
    StartLine  int
    EndLine    int
    Signature  string  // func CreateUser(name string, email string) (*User, error)
    DocComment string
    Package    string
    Exported   bool
}

// extractSymbols 遍历 AST 提取符号定义
func (ts *TreeSitterIndex) extractSymbols(node *sitter.Node, source []byte, filePath string) []*Symbol {
    var symbols []*Symbol

    // 根据节点类型提取符号
    switch node.Type() {
    case "function_declaration":
        sym := &Symbol{
            Kind:      "function",
            FilePath:  filePath,
            StartLine: int(node.StartPoint().Row),
            EndLine:   int(node.EndPoint().Row),
            Name:      ts.extractName(node, source),
            Signature: ts.extractSignature(node, source),
        }
        symbols = append(symbols, sym)
    case "method_declaration":
        // ...
    case "type_declaration":
        // ...
    }

    // 递归进入子节点
    for i := 0; i < int(node.ChildCount()); i++ {
        child := node.Child(i)
        symbols = append(symbols, ts.extractSymbols(child, source, filePath)...)
    }

    return symbols
}
```

---

## 五、调用图分析

```go
// internal/code_engine/call_graph.go

// CallGraph 表示函数间的调用关系
type CallGraph struct {
    nodes map[string]*CallGraphNode  // functionID → node
    edges []*CallGraphEdge
}

type CallGraphEdge struct {
    Caller   string  // 调用者 functionID
    Callee   string  // 被调用者 functionID
    Location string  // 调用位置 "file.go:42"
    Count    int     // 调用次数（静态分析）
}

// AnalyzeCallGraph 分析项目的调用图
func (cg *CallGraph) AnalyzeCallGraph(ctx context.Context, ts *TreeSitterIndex) (*CallGraph, error) {
    // 1. 获取所有函数符号
    functions := ts.symbols.Query(ctx, SymbolQuery{Kinds: []string{"function", "method"}})

    graph := &CallGraph{nodes: make(map[string]*CallGraphNode)}

    // 2. 对每个函数，解析其调用
    for _, fn := range functions {
        node := &CallGraphNode{Function: fn}
        graph.nodes[fn.ID()] = node

        // 读取函数体
        body := ts.readFunctionBody(fn)
        // 匹配函数调用模式
        calls := ts.extractCalls(body, fn)
        for _, call := range calls {
            // 查找被调用函数的定义
            callee := ts.symbols.Resolve(ctx, call.Name, fn.Package)
            if callee != nil {
                graph.edges = append(graph.edges, &CallGraphEdge{
                    Caller:   fn.ID(),
                    Callee:   callee.ID(),
                    Location: call.Location,
                })
            }
        }
    }

    return graph, nil
}

// ImpactAnalysis 影响分析: "改了函数 X，哪些地方受影响？"
func (cg *CallGraph) ImpactAnalysis(functionID string, depth int) *ImpactReport {
    // BFS 遍历调用图，找到所有传递调用者
    visited := make(map[string]bool)
    queue := []string{functionID}
    var affected []string

    for len(queue) > 0 && depth > 0 {
        current := queue[0]
        queue = queue[1:]
        if visited[current] {
            continue
        }
        visited[current] = true
        affected = append(affected, current)

        // 找到所有调用 current 的函数
        for _, edge := range cg.edges {
            if edge.Callee == current && !visited[edge.Caller] {
                queue = append(queue, edge.Caller)
            }
        }
        depth--
    }

    return &ImpactReport{AffectedFunctions: affected}
}
```

---

## 六、安全重构

```go
// internal/code_engine/refactoring.go

// SafeRefactor 执行 AST 级别的安全重构
type SafeRefactor struct {
    lspMgr   *LSPManager
    tsIndex  *TreeSitterIndex
    callGraph *CallGraph
}

// Rename 安全重命名符号
func (sr *SafeRefactor) Rename(ctx context.Context, filePath string, line, col int, newName string) error {
    // 1. 用 LSP 获取所有引用
    srv := sr.lspMgr.servers[detectLanguage(filePath)]
    params := &lsp.ReferenceParams{...}
    var refs []lsp.Location
    srv.SendRequest(ctx, "textDocument/references", params, &refs)

    // 2. 对每个引用位置执行替换
    for _, ref := range refs {
        sr.applyRename(ref.URI, ref.Range, newName)
    }

    // 3. 可选: 运行测试验证
    // sr.runTests(ctx)

    return nil
}

// ExtractFunction 提取函数重构
func (sr *SafeRefactor) ExtractFunction(ctx context.Context, filePath string, startLine, endLine int, newName string) error {
    // 1. 分析选定代码块的输入/输出变量
    // 2. 生成新函数签名
    // 3. 在原位置替换为函数调用
    // 4. 在文件末尾插入新函数定义
    // 这是 AST 操作,不是文本操作
    return nil
}
```

---

## 七、验收标准

1. **符号索引**: 索引 100K 行代码 < 5 秒，搜索 < 10ms
2. **调用图**: 准确率 > 90%（静态分析），支持递归 CTE 遍历
3. **语义搜索**: "找所有调用 X 的地方" 返回结果比纯文本搜索精确率提升 > 50%
4. **安全重命名**: 用 LSP references 确保不遗漏任何引用点
5. **多语言**: 支持 Go / Rust / Python / TypeScript 四种语言
