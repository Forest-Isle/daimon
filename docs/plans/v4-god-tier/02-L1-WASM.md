# L1 — WASM Plugin System (WebAssembly 插件生态)

> 优先级: P2 | 工作量: 3-4 周 | 依赖: 无  
> 将工具系统从 Go 硬编码转变为 WebAssembly 插件生态，任何人可以用任何语言写工具。

---

## 一、现状

```go
// 当前的工具扩展方式 —— 改 Go 源码
// internal/tool/bash.go
type BashTool struct { ... }
func (b *BashTool) Execute(ctx context.Context, input string) (string, error) { ... }

// internal/tool/http.go
type HTTPTool struct { ... }

// 要加新工具？写 Go → 实现 Tool 接口 → 在 init_tools.go 注册 → 重编译
```

**痛点:**
- 新工具 = 改源码 + 重编译
- 不能动态加载/卸载
- 第三方无法贡献工具
- 工具代码运行在宿主进程内，无安全隔离

---

## 二、目标架构

```
┌────────────────────────────────────────────────────────┐
│                  WASM Plugin System                    │
│                                                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │ wazero RT    │  │ Capability   │  │ Plugin        │ │
│  │ (纯Go WASM)  │  │ System       │  │ Registry      │ │
│  │ 零CGO依赖    │  │ 能力安全模型  │  │ 本地+远程注册 │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
│         │                 │                 │          │
│  ┌──────┴─────────────────┴─────────────────┴───────┐  │
│  │              Plugin SDK                          │  │
│  │  Go SDK  │  Rust SDK  │  TypeScript SDK         │  │
│  │  (tinygo)│  (wasm-pack)│ (jco/assemblyscript)   │  │
│  └─────────────────────────────────────────────────┘  │
│                                                        │
│  ┌─────────────────────────────────────────────────┐  │
│  │           Marketplace (工具市场)                  │  │
│  │  GitHub Releases │ OCI Registry │ 评分/评论      │  │
│  └─────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────┘
```

---

## 三、详细设计

### 3.1 插件清单规范

每个 WASM 工具 = 一个 `.wasm` 文件 + 一个 `plugin.yaml` 清单：

```yaml
# plugin.yaml
name: postgres-query
version: 1.2.0
description: Execute read-only SQL queries against PostgreSQL databases
author: community
license: MIT

# 能力声明 — 工具需要的宿主能力
capabilities:
  network:
    - host: "*"              # 允许连接任意 PostgreSQL 主机
      port: 5432
      protocol: tcp
  env:
    - PGHOST
    - PGPORT
    - PGDATABASE
    - PGUSER
  filesystem:
    read: []                 # 不允许读文件
    write: []                # 不允许写文件

# 工具接口定义
interface:
  inputs:
    query:
      type: string
      description: SQL SELECT query to execute
      required: true
    params:
      type: array
      items: string
      description: Query parameters
      required: false
  outputs:
    rows:
      type: array
      description: Query result rows as JSON array
    error:
      type: string
      description: Error message if query failed

# WASM 运行时配置
runtime:
  wasm_file: postgres_query.wasm
  memory_limit_mb: 64
  timeout_ms: 30000
  max_instances: 4           # 最大并发实例数(池化)

# 签名验证
signature:
  alg: ed25519
  public_key: abc123...
  signature: def456...
```

### 3.2 宿主 SDK (Go side)

```go
// internal/wasm/host.go

// WasmPluginHost 管理 WASM 插件的生命周期
type WasmPluginHost struct {
    runtime    wazero.Runtime
    registry   *PluginRegistry
    capability *CapabilityManager
    pool       *InstancePool
}

// LoadPlugin 从文件系统加载一个插件
func (h *WasmPluginHost) LoadPlugin(ctx context.Context, manifestPath string) (*LoadedPlugin, error) {
    // 1. 解析 plugin.yaml
    manifest, err := ParseManifest(manifestPath)
    if err != nil {
        return nil, fmt.Errorf("parse manifest: %w", err)
    }

    // 2. 验证签名（可选）
    if manifest.Signature != nil {
        if err := h.verifySignature(manifest); err != nil {
            return nil, fmt.Errorf("signature verification: %w", err)
        }
    }

    // 3. 编译 WASM 模块
    wasmBytes, err := os.ReadFile(manifest.Runtime.WasmFile)
    if err != nil {
        return nil, fmt.Errorf("read wasm: %w", err)
    }
    module, err := h.runtime.CompileModule(ctx, wasmBytes)
    if err != nil {
        return nil, fmt.Errorf("compile: %w", err)
    }

    // 4. 创建能力边界
    caps := h.capability.BuildCapabilities(manifest.Capabilities)

    // 5. 注册到工具注册表
    tool := &WasmTool{
        manifest: manifest,
        module:   module,
        host:     h,
        caps:     caps,
    }
    h.registry.Register(tool)

    return &LoadedPlugin{Tool: tool}, nil
}

// WasmTool 实现 tool.Tool 接口
type WasmTool struct {
    manifest   *PluginManifest
    module     wazero.CompiledModule
    host       *WasmPluginHost
    caps       *CapabilitySet
    pool       *InstancePool
}

func (wt *WasmTool) Execute(ctx context.Context, input string) (string, error) {
    // 1. 从实例池获取空闲实例
    inst, err := wt.pool.Acquire(ctx)
    if err != nil {
        return "", err
    }
    defer wt.pool.Release(inst)

    // 2. 调用 WASM 导出函数
    results, err := inst.CallFunction(ctx, "execute", []uint64{ptr, len})
    if err != nil {
        return "", fmt.Errorf("wasm execution: %w", err)
    }

    // 3. 从 WASM 线性内存读取输出
    output := inst.ReadMemory(results[0], results[1])
    return string(output), nil
}
```

### 3.3 能力安全模型

这是最关键的设计。不能让一个数据库插件去读 `/etc/passwd`。

```go
// internal/wasm/capability.go

// CapabilityManager 管理每个插件的权限边界
type CapabilityManager struct {
    policies []CapabilityPolicy
}

// CapabilitySet 定义单个插件的能力边界
type CapabilitySet struct {
    Network   *NetworkPolicy
    FS        *FilesystemPolicy
    Env       *EnvPolicy
    Exec      *ExecPolicy       // 是否允许启动子进程
    MemoryMB  int64
    TimeoutMS int64
}

// NetworkPolicy — 精确控制网络访问
type NetworkPolicy struct {
    AllowedHosts []string  // "*.postgresql.org", "192.168.1.0/24"
    AllowedPorts []int
    DeniedHosts  []string  // 黑名单优先
}

// 宿主函数实现 —— 当 WASM 调用 "http_get" 时
func (cm *CapabilityManager) HostHTTPGet(ctx context.Context, caps *CapabilitySet, url string) ([]byte, error) {
    // 1. 解析 URL
    parsed, _ := url.Parse(url)

    // 2. 检查网络策略
    if caps.Network == nil {
        return nil, ErrCapabilityDenied("network access not granted")
    }
    if !caps.Network.IsAllowed(parsed.Host) {
        return nil, ErrCapabilityDenied(fmt.Sprintf("host %s not in allowlist", parsed.Host))
    }

    // 3. 执行实际 HTTP 请求
    return doHTTPRequest(ctx, url)
}
```

```go
// internal/wasm/capability.go

// 宿主导出函数表 —— 按能力分类
const (
    HostFuncReadFile  = "host_read_file"
    HostFuncWriteFile = "host_write_file"
    HostFuncHTTPGet   = "host_http_get"
    HostFuncHTTPPost  = "host_http_post"
    HostFuncGetEnv    = "host_get_env"
    HostFuncLog       = "host_log"
    HostFuncNow       = "host_now"
    HostFuncSleep     = "host_sleep"
)

// registerHostFunctions 注册宿主函数到 WASM 实例
// 每个宿主函数在执行前都会检查能力边界
func (cm *CapabilityManager) RegisterHostFunctions(builder wazero.HostModuleBuilder) {
    builder.NewFunctionBuilder().
        WithFunc(func(ctx context.Context, mod wazero.Module, urlPtr, urlLen uint32) uint32 {
            caps := ctx.Value(capabilityCtxKey{}).(*CapabilitySet)
            url := readString(mod, urlPtr, urlLen)
            data, err := cm.HostHTTPGet(ctx, caps, url)
            if err != nil {
                return writeError(mod, err)
            }
            return writeBytes(mod, data)
        }).
        Export(HostFuncHTTPGet)
    // ... 其他宿主函数
}
```

### 3.4 插件 SDK（给工具开发者用）

**Go SDK (via TinyGo → WASM):**

```go
// sdk/go/plugin.go — 工具开发者用的 SDK
package plugin

import "encoding/json"

// Tool 是工具开发者需要实现的接口
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]interface{}
    Execute(ctx Context, input json.RawMessage) (json.RawMessage, error)
}

// Context 提供受限的宿主能力
type Context struct {
    HTTP  *HTTPClient   // 网络请求（受策略限制）
    Log   func(string)  // 日志输出
    Now   func() int64  // 当前时间戳
}

// Export 注册工具到宿主
func Export(tool Tool) {
    // 由 SDK 自动生成 WASM 导出函数
    registerExport("name", tool.Name())
    registerExport("description", tool.Description())
    registerExport("input_schema", mustJSON(tool.InputSchema()))
    registerExport("execute", func(inputPtr, inputLen uint32) (uint32, uint32) {
        input := readHostMemory(inputPtr, inputLen)
        result, err := tool.Execute(newContext(), input)
        if err != nil {
            return writeError(err)
        }
        return writeResult(result)
    })
}
```

**Rust SDK:**

```rust
// sdk/rust/src/lib.rs
use serde_json::Value;

pub trait Tool: Send + Sync {
    fn name(&self) -> &str;
    fn description(&self) -> &str;
    fn input_schema(&self) -> Value;
    fn execute(&self, ctx: &Context, input: Value) -> Result<Value, Error>;
}

pub struct Context {
    pub http: HttpClient,
    pub log: Box<dyn Fn(&str)>,
}

#[macro_export]
macro_rules! export_tool {
    ($tool:expr) => {
        #[no_mangle]
        pub extern "C" fn name() -> *const u8 { /* ... */ }
        #[no_mangle]
        pub extern "C" fn execute(input_ptr: *const u8, input_len: usize) -> i64 { /* ... */ }
    };
}
```

**TypeScript SDK (via AssemblyScript / Jco):**

```typescript
// sdk/typescript/plugin.ts
export interface Tool {
  name(): string;
  description(): string;
  inputSchema(): Record<string, unknown>;
  execute(ctx: Context, input: unknown): Promise<unknown>;
}

export interface Context {
  http: HTTPClient;
  log(msg: string): void;
}

// 工具示例
class WeatherTool implements Tool {
  name() { return "weather"; }
  description() { return "Get current weather for a city"; }
  inputSchema() {
    return {
      type: "object",
      properties: { city: { type: "string" } }
    };
  }
  async execute(ctx: Context, input: { city: string }) {
    const resp = await ctx.http.get(
      `https://api.weather.com/${input.city}`
    );
    return resp.json();
  }
}

export default new WeatherTool();
```

### 3.5 实例池 — 预热与并发

```go
// internal/wasm/pool.go

// InstancePool 管理 WASM 实例池
// WASM 实例创建是有成本的（分配线性内存、初始化模块），
// 预创建实例可以消除冷启动延迟
type InstancePool struct {
    module    wazero.CompiledModule
    config    wazero.ModuleConfig
    instances chan *PooledInstance
    maxSize   int
}

type PooledInstance struct {
    mod    wazero.Module
    inUse  bool
    lastUsed time.Time
}

func (p *InstancePool) Acquire(ctx context.Context) (*PooledInstance, error) {
    select {
    case inst := <-p.instances:
        inst.inUse = true
        return inst, nil
    default:
        // 池中无空闲实例，创建新的
        if p.currentSize < p.maxSize {
            return p.createInstance(ctx)
        }
        // 等待归还
        select {
        case inst := <-p.instances:
            return inst, nil
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }
}

func (p *InstancePool) Release(inst *PooledInstance) {
    inst.inUse = false
    inst.lastUsed = time.Now()
    select {
    case p.instances <- inst:
    default:
        // 池满了，丢弃
    }
}

// 后台 goroutine: 定期清理长时间未使用的实例
func (p *InstancePool) reapIdle(dur time.Duration) {
    // ...
}
```

### 3.6 工具市场

```go
// internal/wasm/marketplace.go

// Marketplace 连接本地和远程插件仓库
type Marketplace struct {
    localPath   string              // ~/.IronClaw/plugins/
    remotes     []RemoteRegistry
    cache       map[string]*PluginIndex
}

// RemoteRegistry 接口 —— 支持多种后端
type RemoteRegistry interface {
    List(ctx context.Context, query string) ([]*PluginInfo, error)
    Download(ctx context.Context, name, version string) ([]byte, error)  // 返回 .wasm 字节
    GetManifest(ctx context.Context, name, version string) (*PluginManifest, error)
}

// GitHubRegistry 从 GitHub Releases 拉取插件
type GitHubRegistry struct {
    owner string  // "ironclaw-plugins"
    repo  string  // "community-registry"
}
func (g *GitHubRegistry) List(ctx context.Context, query string) ([]*PluginInfo, error) { /* gh api */ }

// OCIREgistry 从 OCI 兼容仓库拉取 (支持 Docker Hub, GitHub Container Registry)
type OCIREgistry struct {
    registry string  // "ghcr.io"
    namespace string // "ironclaw/plugins"
}
```

CLI 体验:
```bash
# 搜索插件
ironclaw plugin search "postgres"
# → postgres-query v1.2.0 (⭐ 42) - Execute read-only SQL queries
# → postgres-admin v0.9.0 (⭐ 12) - Full PostgreSQL administration

# 安装插件
ironclaw plugin install postgres-query
# → Downloading postgres-query v1.2.0... done
# → Verifying signature... ok
# → Installed to ~/.IronClaw/plugins/postgres-query/

# 列出已安装
ironclaw plugin list
# → postgres-query v1.2.0 (enabled)
# → slack-notify v2.1.0 (disabled)

# 启用/禁用
ironclaw plugin enable slack-notify
```

---

## 四、为什么选 wazero

| 方案 | 优点 | 缺点 |
|------|------|------|
| **wazero** | 纯 Go，零 CGO，零系统依赖 | 性能比原生 WASM runtimes 稍低 |
| Wasmtime-go | 性能好，成熟 | 需要 CGO，编译复杂 |
| Wasmer-go | 功能丰富 | 需要 CGO，许可证变化风险 |
| Docker 容器 | 完全隔离 | 启动慢（>1s），依赖 Docker daemon |

**选 wazero 的原因:**
- IronClaw 是 Go 项目，wazero 是纯 Go，不需要 CGO
- 实例启动 <1ms（vs Docker 的 1s+）
- 支持 WASI preview1，足够覆盖工具场景
- 一个插件实例的内存占用 <10MB

---

## 五、与现有工具系统的共存

```go
// internal/gateway/init_tools.go (改造后)

func (gw *Gateway) initToolsAndHooks() error {
    // 1. 加载内置 Go 工具 (bash, file, http — 保留)
    gw.tools.Register(NewBashTool())
    gw.tools.Register(NewFileTool())
    gw.tools.Register(NewHTTPTool())

    // 2. 加载 WASM 插件
    if gw.features.IsEnabled("wasm_plugins") {
        wasmHost := wasm.NewPluginHost()
        wasmTools, err := wasmHost.LoadPluginsFromDir("~/.IronClaw/plugins/")
        if err != nil {
            return err
        }
        for _, t := range wasmTools {
            gw.tools.Register(t)
        }
    }

    // 3. MCP 工具 (保持现有逻辑)
    // ...

    return nil
}
```

**内置 Go 工具保留的原因:** bash 需要深度系统集成（Docker 沙箱），HTTP 工具需要配合 NetworkPolicy 做 SSRF 防护，这些用 WASM 做反而更复杂。

---

## 六、验收标准

1. **加载延迟**: 插件加载（编译+实例化）<50ms
2. **执行延迟**: WASM 调用开销 <1ms（不含工具自身逻辑）
3. **安全性**: 未授权网络访问被拦截率 100%（通过能力策略测试套件验证）
4. **SDK 可用性**: Go/Rust/TypeScript 三种 SDK 均有工作示例
5. **热加载**: 启用/禁用插件不需要重启 IronClaw
