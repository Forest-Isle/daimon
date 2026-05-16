# L6 — Native Browser Agent (浏览器原生代理)

> 优先级: P6 | 工作量: 2-3 周 | 依赖: 无  
> 通过 Chrome DevTools Protocol (CDP) 实现完整的浏览器控制能力——IronClaw 版 Computer Use。

---

## 一、能力矩阵

| 能力 | 描述 |
|------|------|
| 导航 | go(url), back(), forward(), refresh() |
| 页面理解 | snapshot(selector/all) — 可交互元素列表 + 属性 |
| 交互 | click(id), type(id, text), select(id, option), hover(id) |
| 视觉分析 | screenshot → 视觉模型理解页面布局和状态 |
| 表单填写 | 基于 JSON schema 的自动表单填写 |
| 数据提取 | extract(selector) → 结构化 JSON |
| 脚本执行 | eval(js) — 受限 JS 执行 |
| Cookie/Storage | 持久化会话，跨场景保持登录态 |
| 多标签页 | 标签页管理，跨标签页数据传递 |

---

## 二、架构

```
┌──────────────────────────────────────────────┐
│           BrowserAgent                       │
│                                              │
│  ┌──────────────┐  ┌──────────────────────┐ │
│  │ CDP Client   │  │ DOM Parser           │ │
│  │ (chrome-     │  │ 可交互元素提取        │ │
│  │  devtools-   │  │ 选择器生成            │ │
│  │  protocol)   │  │ 表单检测              │ │
│  └──────┬───────┘  └──────────┬───────────┘ │
│         │                     │              │
│  ┌──────┴─────────────────────┴───────────┐ │
│  │         Action Executor                 │ │
│  │  点击 | 输入 | 滚动 | 等待 | 截图       │ │
│  └──────┬──────────────────────────────────┘ │
│         │                                     │
│  ┌──────┴──────────────────────────────────┐ │
│  │         Visual Analyzer                  │ │
│  │  截图 → 视觉模型 → 页面状态描述          │ │
│  └──────┬──────────────────────────────────┘ │
│         │                                     │
│  ┌──────┴──────────────────────────────────┐ │
│  │         Web Automator                    │ │
│  │  复杂任务编排 | 错误恢复 | 超时处理       │ │
│  └─────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
```

---

## 三、CDP 客户端

```go
// internal/browser_agent/cdp_client.go

type CDPClient struct {
    conn     *rpc.Conn
    browser  *cdp.Browser
    targets  map[string]*cdp.Target  // targetID → target
    pages    map[string]*Page        // pageID → page
}

type Page struct {
    targetID string
    url      string
    title    string
    dom      *DOMSnapshot
}

// Connect 连接到 Chrome/Chromium
func (c *CDPClient) Connect(ctx context.Context, opts ConnectOptions) error {
    // 选项 1: 连接到已运行的 Chrome (需要 --remote-debugging-port)
    // 选项 2: 启动新的 headless Chrome 实例
    // 选项 3: 连接到 browser-harness 已有的持久化 Chromium

    debugURL := opts.DebugURL  // "http://localhost:9222"
    if debugURL == "" {
        // 启动新的 Chrome
        chromePath := findChrome()
        c.cmd = exec.Command(chromePath,
            "--headless=new",
            "--remote-debugging-port=0",  // 随机端口
            "--no-sandbox",
            "--disable-gpu",
            fmt.Sprintf("--user-data-dir=%s", opts.ProfileDir),
        )
        c.cmd.Start()
        debugURL = c.getDebugURL()
    }

    // 建立 CDP WebSocket 连接
    c.conn = rpc.NewConn(debugURL)
    return nil
}
```

### 3.1 页面快照 (核心操作)

```go
// Snapshot 获取页面可交互元素列表
// 每次操作前调用，获取最新的页面状态
func (p *Page) Snapshot(ctx context.Context, all bool) (*PageSnapshot, error) {
    // 1. 注入 data-claude-id 到所有可交互元素
    _, err := p.eval(ctx, `
        (() => {
            const elements = document.querySelectorAll(
                'a, button, input, select, textarea, [role="button"], [role="link"], [role="textbox"], [onclick]'
            );
            let id = 0;
            elements.forEach(el => {
                el.setAttribute('data-ironclaw-id', String(++id));
            });
            return id;
        })()
    `)

    // 2. 获取每个元素的信息
    elements, err := p.eval(ctx, `
        (() => {
            const results = [];
            document.querySelectorAll('[data-ironclaw-id]').forEach(el => {
                const id = el.getAttribute('data-ironclaw-id');
                const tag = el.tagName.toLowerCase();
                const type = el.type || '';
                const text = (el.textContent || '').trim().substring(0, 200);
                const placeholder = el.placeholder || '';
                const name = el.name || '';
                const value = el.value || '';
                const href = el.href || '';
                const rect = el.getBoundingClientRect();
                const visible = rect.width > 0 && rect.height > 0;
                results.push({ id, tag, type, text, placeholder, name, value, href, visible,
                    x: Math.round(rect.x), y: Math.round(rect.y),
                    w: Math.round(rect.width), h: Math.round(rect.height)
                });
            });
            return JSON.stringify(results);
        })()
    `)

    // 3. 解析结果
    var items []*DOMElement
    json.Unmarshal([]byte(elements), &items)

    return &PageSnapshot{
        URL:      p.url,
        Title:    p.title,
        Elements: items,
    }, nil
}

type DOMElement struct {
    ID          string `json:"id"`
    Tag         string `json:"tag"`
    Type        string `json:"type"`      // text, password, submit, checkbox...
    Text        string `json:"text"`
    Placeholder string `json:"placeholder"`
    Name        string `json:"name"`
    Value       string `json:"value"`
    Href        string `json:"href"`
    Visible     bool   `json:"visible"`
    X, Y, W, H  int    `json:"x,y,w,h"`
}

type PageSnapshot struct {
    URL      string
    Title    string
    Elements []*DOMElement
}
```

### 3.2 操作执行器

```go
// internal/browser_agent/actions.go

type ActionExecutor struct {
    page   *Page
    config ActionConfig
}

// Click 点击指定元素
func (ae *ActionExecutor) Click(ctx context.Context, elementID string) error {
    _, err := ae.page.eval(ctx, fmt.Sprintf(`
        (() => {
            const el = document.querySelector('[data-ironclaw-id="%s"]');
            if (!el) return JSON.stringify({error: "element not found"});
            if (!el.checkVisibility()) return JSON.stringify({error: "element not visible"});
            el.scrollIntoView({block: "center"});
            el.click();
            return JSON.stringify({ok: true});
        })()
    `, elementID))
    return err
}

// Type 在输入框中填入文本
func (ae *ActionExecutor) Type(ctx context.Context, elementID, text string, pressEnter bool) error {
    js := fmt.Sprintf(`
        (() => {
            const el = document.querySelector('[data-ironclaw-id="%s"]');
            if (!el) return JSON.stringify({error: "element not found"});
            el.focus();
            el.value = '';
            // 模拟逐字符输入以触发 React/Vue 的事件系统
            const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
                window.HTMLInputElement.prototype, 'value'
            ).set;
            nativeInputValueSetter.call(el, %q);
            el.dispatchEvent(new Event('input', {bubbles: true}));
            el.dispatchEvent(new Event('change', {bubbles: true}));
            %s
            return JSON.stringify({ok: true, value: el.value});
        })()
    `, elementID, text, enterKey)
    _, err := ae.page.eval(ctx, js)
    return err
}

// Scroll 滚动页面
func (ae *ActionExecutor) Scroll(ctx context.Context, direction string, amount int) error { ... }

// Wait 等待特定条件
func (ae *ActionExecutor) Wait(ctx context.Context, condition string) error {
    // condition: "text:登录成功" / "selector:.result" / "navigation"
    // ...
}
```

### 3.3 视觉分析

```go
// internal/browser_agent/visual.go

type VisualAnalyzer struct {
    visionModel Completer  // 支持图片的 LLM (Claude/GPT-4V)
}

// AnalyzePage 截图并用视觉模型分析页面状态
func (va *VisualAnalyzer) AnalyzePage(ctx context.Context, page *Page) (*VisualAnalysis, error) {
    // 1. 截图
    screenshot, err := page.Screenshot(ctx)
    if err != nil {
        return nil, err
    }

    // 2. 视觉模型分析
    prompt := `Analyze this webpage screenshot. Describe:
1. What type of page is this? (login, search results, dashboard, article, error, etc.)
2. What is the main content?
3. Are there any forms? What fields?
4. Are there any error messages or alerts?
5. What actions can the user take?
6. Is there anything blocking the page (popup, cookie banner, captcha)?`

    analysis, err := va.visionModel.Complete(ctx, CompletionRequest{
        System: "You analyze webpage screenshots. Be specific about interactive elements.",
        Messages: []CompletionMessage{{
            Role: "user",
            Content: []ContentBlock{
                {Type: "image", Source: screenshot},
                {Type: "text", Text: prompt},
            },
        }},
        MaxTokens: 1024,
    })

    return parseVisualAnalysis(analysis.Text), nil
}
```

---

## 四、自动化流程

```go
// internal/browser_agent/automator.go

// WebAutomator 编排复杂的 Web 任务
type WebAutomator struct {
    agent    *BrowserAgent
    planner  Completer  // 用 LLM 规划操作序列
}

// ExecuteTask 执行 Web 自动化任务
// task: "在 GitHub 上创建一个新的 Issue，标题为 'Bug: login broken'"
func (wa *WebAutomator) ExecuteTask(ctx context.Context, task string) (*AutomationResult, error) {
    // 1. Snapshot 当前页面
    snapshot := wa.agent.Page.Snapshot(ctx)

    // 2. LLM 规划下一步操作
    for step := 0; step < wa.config.MaxSteps; step++ {
        action := wa.planNextAction(ctx, task, snapshot, step)

        switch action.Type {
        case "click":
            wa.agent.Actions.Click(ctx, action.ElementID)
        case "type":
            wa.agent.Actions.Type(ctx, action.ElementID, action.Text, false)
        case "scroll":
            wa.agent.Actions.Scroll(ctx, action.Direction, action.Amount)
        case "wait":
            wa.agent.Actions.Wait(ctx, action.Condition)
        case "go":
            wa.agent.Navigate(ctx, action.URL)
        case "done":
            return &AutomationResult{Success: true, Summary: action.Summary}, nil
        case "fail":
            return &AutomationResult{Success: false, Error: action.Error}, nil
        }

        // 3. 重新 snapshot（页面可能已变化）
        time.Sleep(500 * time.Millisecond)
        snapshot = wa.agent.Page.Snapshot(ctx)
    }

    return nil, fmt.Errorf("exceeded max steps (%d)", wa.config.MaxSteps)
}
```

---

## 五、与 IronClaw 集成

```go
// 注册为工具
func (gw *Gateway) initBrowserTools() {
    if gw.features.IsEnabled("browser_agent") {
        browserAgent := browser_agent.New(gw.cfg.Browser)
        gw.tools.Register(browserAgent.AsTool())  // browser_navigate, browser_click, etc.
        gw.browserAgent = browserAgent
    }
}
```

LLM 看到的工具描述:
```json
{
  "name": "browser_navigate",
  "description": "Navigate the browser to a URL",
  "input_schema": {
    "type": "object",
    "properties": {
      "url": {"type": "string", "description": "The URL to navigate to"}
    }
  }
}
```

---

## 六、验收标准

1. **基本操作**: navigate, click, type, scroll, screenshot 全部可用
2. **快照质量**: DOM 快照准确率 > 95%（所有可见可交互元素都被捕获）
3. **表单填写**: 自动识别表单字段并填写，成功率 > 85%
4. **持久化**: 浏览器 cookie/localStorage 会话在重启后保持
5. **性能**: snapshot < 100ms, screenshot < 500ms
