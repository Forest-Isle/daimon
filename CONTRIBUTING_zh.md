# 贡献指南

感谢你对 IronClaw 的关注！本指南将帮助你开始贡献。

## 开始之前

1. Fork 本仓库
2. 克隆你的 Fork：
   ```bash
   git clone https://github.com/YOUR_USERNAME/ironclaw.git
   cd ironclaw
   ```
3. 创建功能分支：
   ```bash
   git checkout -b feature/your-feature-name
   ```

## 开发环境

### 前置要求

- Go 1.23 或更高版本
- GCC（SQLite 需要 CGO）
- golangci-lint（可选，用于代码检查）

### 构建与测试

```bash
# 构建
make build

# 运行测试
make test

# 代码检查
make lint

# 格式化代码
make fmt
```

## 提交更改

### 代码风格

- 遵循 Go 标准规范（`gofmt`、`go vet`）
- 保持函数简洁、职责单一
- 为导出的类型和函数添加注释
- 为新功能编写测试

### Commit 信息

使用清晰的描述性 Commit 信息：

```
feat: 添加 Discord 渠道适配器
fix: 处理 Claude API 空响应
docs: 更新配置示例
refactor: 简化会话管理逻辑
```

前缀：`feat`、`fix`、`docs`、`refactor`、`test`、`chore`、`ci`

### Pull Request

1. 确保代码通过 `make test` 和 `make lint`
2. 如果更改影响用户行为，请更新文档
3. 保持 PR 聚焦 — 每个 PR 只包含一个功能或修复
4. 完整填写 PR 模板

## 报告问题

- 使用 [Bug 报告](https://github.com/punkopunko/ironclaw/issues/new?template=bug_report.md) 模板报告缺陷
- 使用 [功能请求](https://github.com/punkopunko/ironclaw/issues/new?template=feature_request.md) 模板提出新想法
- 创建新 Issue 前请先搜索已有 Issue

## 行为准则

本项目遵循 [Contributor Covenant 行为准则](CODE_OF_CONDUCT.md)。参与本项目即表示你同意遵守该准则。

## 许可证

你的贡献将按照 [MIT 许可证](LICENSE) 进行授权。
