# Changelog

本项目的所有重要变更记录。格式参考 [Keep a Changelog](https://keepachangelog.com/)，
版本号遵循 [Semantic Versioning](https://semver.org/)。

## [Unreleased]

### Added
- 阶段 0：脚手架 —— 内部路由模型（`pkg/model`）、`IngressSelector`（实现 zeus `proxy.Selector`）、`StaticDiscovery`、`GoverningRoundTripper` 骨架、`dataplane.Server`、控制面 `Reconciler` 抽象。
- 阶段 1：Ingress v1 MVP —— controller-runtime 控制面（Informer 监听 Ingress/Service/Endpoints/Secret）、`K8sDiscovery`（Endpoints → zeus `registry.Discovery`）、`translator`（Ingress v1 → RoutingTable，支持命名端口）、TLS 卸载（Secret + SNI 动态证书）、Ingress status 更新。
- 阶段 2：治理内建 —— `GoverningRoundTripper` 接入 zeus 熔断/限流/重试（按 service+cluster 隔离）、被动健康检查（outlier detection）。
- 阶段 3：灰度/金丝雀 —— canary annotation（weight/header/cookie）跨 Ingress 合并到主 rule 的 `CanaryConfig`；兼容 `nginx.ingress.kubernetes.io/canary*` 前缀。
- 路由匹配：host（精确 + `*.example.com` 通配）、path（Exact/Prefix/ImplementationSpecific，K8s Prefix 边界语义）、method 过滤。
- annotation：`rewrite-target`（hermes + nginx 兼容前缀）。
- 双运行模式：`--demo`（硬编码路由，无 K8s 依赖）与 K8s 模式（controller-runtime）。
- 开源治理：MIT License、CONTRIBUTING、SECURITY、CODE_OF_CONDUCT、CI（lint/test/build）。
- 阶段 6：生产级增强 —— leader election（多副本 HA）、主动健康检查（周期探测 `/health`）、per-service 限流 annotation（`limit-rps`，按 `service+cluster` 独立令牌桶）、Prometheus 自定义 metrics（`hermes_requests_total` / `hermes_request_duration_seconds` / `hermes_upstream_failures_total`）、PodDisruptionBudget、Helm PDB template、HTTPProxy CRD（`hermes.io/v1alpha1`，流量拆分/header 匹配/路径重写）、gRPC 路由（h2c 明文 HTTP/2，实验性）。

### Changed
- **主动健康检查改为 opt-in**：仅当 Service 声明 `hermes.ingress.kubernetes.io/active-health-check-path` 时才主动探测，未声明的后端只依赖被动健康检查。
- **修复主动健康检查误剔除后端**：此前对所有后端无条件探测 `/health`，未实现该端点的后端（静态站点等）被判不健康并剔除，导致 503。现在由 Service 注解显式开启，根除此误判（新增守护测试 `TestActiveHealthCheck_NotOptInSkipsProbing`）。

### Docs
- 新增 [路由指南](docs-site/routing.md)：暴露服务的实战场景（前后端分离 / 多域名 / 通配 host / 默认后端 / 命名端口 / TLS / 排错速查）。
- 新增 [运维与可观测](docs-site/operations.md)：部署参数表、Prometheus 指标 + 常用 PromQL、访问日志格式、双层健康检查说明、HA、集群接管步骤。
- 新增 `docs-site/` 文档站（VitePress）：内容 markdown 与配置同处一目录（单一真源），teal 品牌主题、本地搜索、首页 hero。`pnpm docs:dev` 本地预览，`pnpm docs:build` 生成静态站点（9 个页面 + 404，零警告）。原 `docs/` 内容内迁至 `docs-site/`。
- 新增文档站云主机自动部署：`.github/workflows/deploy-docs.yml`（push main → pnpm build → rsync over SSH）+ `deploy/setup-docs-server.sh`（一次性初始化专用 deploy 账号 + ed25519 SSH 通道 + setgid 文档目录）。必需 Secrets：`SERVER_HOST/PORT/USER/DOCS_PATH/SERVER_SSH_KEY`。

[Unreleased]: https://github.com/go-ingress/ingress/compare/HEAD
