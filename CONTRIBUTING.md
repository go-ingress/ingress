# Contributing to Hermes

感谢你考虑为 Hermes 贡献代码！本文档说明贡献流程与代码规范。

## 贡献流程

1. **Fork & Branch**：Fork 仓库，从 `main` 切出特性分支（`feat/xxx`、`fix/xxx`、`docs/xxx`）。
2. **开发**：遵循下方代码规范，确保 `go test ./...` 与 `go vet ./...` 通过。
3. **提交**：使用[约定式提交](https://www.conventionalcommits.org/)（`feat:` / `fix:` / `docs:` / `refactor:` / `test:` / `chore:`）。
4. **PR**：向 `main` 发起 PR，描述动机、变更、测试方式，关联相关 Issue。
5. **Review**：维护者 review，可能要求修改。通过后合并。

## 代码规范

- **Go 版本**：1.26.0+（`controller-runtime v0.24.1` / `k8s.io v0.36` 强制要求 go >= 1.26.0，`go mod tidy` 会拒绝更低版本）。本地装 go 1.26 即可，无需额外配置——`go build` 会按需自动拉取工具链。
- **注释语言**：与现有代码保持一致（**中文注释**），与 zeus 体系对齐。
- **零依赖原则**：核心包（`pkg/model`、`pkg/dataplane`、`pkg/translator`、`pkg/governance`、`pkg/discovery`、`pkg/controller`）尽量保持依赖精简；K8s 客户端仅在 `pkg/controller` 与 `pkg/translator` 必要时引入。
- **接口优先**：功能域定义接口 + 实现，构造器注入（参考 zeus 模式）。
- **KISS / YAGNI / DRY / SOLID**：每次变更体现工程原则。
- **危险操作**：删除文件 / 重置 git / 变更依赖主版本前，PR 中需明确说明并征得维护者同意。

## 测试要求

- 所有新代码必须有对应测试（对齐 zeus 规范）。
- 纯函数优先表驱动测试；接口用 fake/mock（controller 用 `controller-runtime` 的 `fake` client）。
- 目标覆盖率：核心包（`model` / `translator` / `governance`）≥ 70%。
- 提交前本地运行：

```bash
go test -race ./...
go vet ./...
```

## 提交一个新 annotation

1. 在 `pkg/translator/annotations.go` 增加常量与解析函数。
2. 在 `pkg/translator/ingress_test.go` 增加单测。
3. 在 `docs/annotations.md` 增加文档条目。
4. 必要时在 `examples/` 增加示例清单。

## 提交一个新治理策略

1. 在 `pkg/governance/` 增加策略组件。
2. 在 `GoverningRoundTripper` 增加 `With*` Option。
3. 单测覆盖策略生效与隔离。
4. 文档更新（`docs/DESIGN.md` 对应章节）。

## 行为准则

参与本项目即表示你同意遵守 [Code of Conduct](./CODE_OF_CONDUCT.md)。请保持友善、尊重、建设性。

## 许可证

贡献的代码将在 [MIT License](./LICENSE) 下发布。提交 PR 即表示你同意该许可。
