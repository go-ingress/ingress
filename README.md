# Hermes

[![CI](https://github.com/go-ingress/ingress/actions/workflows/ci.yml/badge.svg)](https://github.com/go-ingress/ingress/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/github/license/go-ingress/ingress?color=blue)](./LICENSE)
[![Security Policy](https://img.shields.io/badge/security-policy-blue)](./SECURITY.md)

**基于 [Zeus](https://github.com/go-zeus/zeus) 服务治理的 K8s Ingress 控制器。**

业界唯一一个**全栈 Go + 治理能力原生内建**的 Ingress 控制器：控制面监听 K8s 资源（Ingress v1 + Gateway API），数据面复用 zeus 反向代理，单二进制无 nginx/Envoy 依赖；熔断/限流/重试/灰度/链路追踪开箱即用，治理语义与 zeus 微服务框架端到端打通。

> 产品代号 Hermes（赫尔墨斯，宙斯之子，掌管门户与信使）—— 呼应 `go-zeus/zeus` 体系。

## 核心特性

- **Ingress v1 完整支持**：host（精确 + `*.example.com` 通配）、path（Exact/Prefix/ImplementationSpecific，K8s Prefix 边界语义）、命名端口、default backend、TLS 卸载（Secret + SNI 动态证书，热加载）、Ingress status 更新。
- **Gateway API**：HTTPRoute 翻译（`--enable-gateway-api`），含 traffic split（多 backend → canary weight）。
- **治理内建**：熔断（20 请求窗口/50% 失败率）、限流（100 rps/burst 100）、重试（2 次指数退避，502/503/504）、被动健康检查（outlier detection，5 次失败驱逐 30s），按 `service+cluster` 维度隔离。
- **灰度/金丝雀**：`canary-weight`/`canary-by-header`/`canary-by-cookie`，跨 Ingress 合并；兼容 `nginx.ingress.kubernetes.io/canary*` 前缀。
- **零 reload 热更新**：Endpoints 变更走 K8sDiscovery 缓存；路由/TLS/治理变化走 atomic pointer swap，数据面无锁。
- **多协议**：HTTP/HTTPS、WebSocket、SSE（zeus proxy 内置）。
- **可观测**：access log + Prometheus metrics（`:8081/metrics`）+ trace（zeus otel）+ W3C Baggage 端到端传播。
- **双运行模式**：`--demo`（硬编码路由，无 K8s 依赖，开发验证）与 K8s 模式（controller-runtime）。

## 快速开始

### demo 模式（无需 K8s 集群）

```bash
go run ./cmd/hermes --demo
curl -H 'Host: foo.local' http://localhost:8080/
```

### K8s 部署

```bash
# 一体化清单
kubectl apply -f deploy/manifests.yaml

# 或 Helm
helm install hermes deploy/helm -n hermes-system --create-namespace \
  --set controller.statusHostname=hermes.example.com
```

创建 Ingress：

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: { name: echo }
spec:
  ingressClassName: hermes
  rules:
    - host: echo.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend: { service: { name: echo, port: { number: 80 } } }
```

更多示例见 [`examples/`](./examples)（basic / canary / tls / rewrite）。

## 架构

```
控制面 (controller-runtime)                  数据面 (zeus proxy, 同进程)
  Informer: Ingress/HTTPRoute/Service          middleware.Chain (requestid/accesslog/recovery)
    /Endpoints/Secret                          → TLS(SNI 动态证书)
  → translator → RoutingTable                  → IngressSelector (host/path/canary/health)
  → atomic.Store(&table) ──────────────────▶   → zeus proxy (HTTP/WS/SSE)
                                               → GoverningRoundTripper (限流/熔断/重试/健康检查)
                                               → 后端 Pod (K8s Endpoints)
```

详见 [docs-site/DESIGN.md](./docs-site/DESIGN.md)。

## 构建

```bash
go test -race ./...                  # 测试
go build ./...                        # 编译
CGO_ENABLED=0 go build -o bin/hermes ./cmd/hermes   # 生产二进制
```

## 路线图

| 阶段 | 内容 | 状态 |
|---|---|---|
| 0 | 脚手架 + 硬编码路由端到端 | ✅ |
| 1 | Ingress v1 MVP（controller-runtime + Informer + K8sDiscovery + TLS + status） | ✅ |
| 2 | 治理内建（熔断/限流/重试/被动健康检查） | ✅ |
| 3 | 灰度/金丝雀（canary annotation → zeus cluster 隔离） | ✅ |
| 4 | Gateway API（HTTPRoute 翻译 + traffic split） | ✅ |
| 5 | 生产化开源（Helm/RBAC/CI/治理文档/示例） | ✅ |
| 6 | 生产级增强 | ✅ leader election / 主动健康检查 / per-service 限流 / Prometheus metrics / PDB / HTTPProxy CRD / gRPC 路由（h2c，实验性）；⏳ HTTP3 / Wasm |

## 文档

文档位于 [`docs-site/`](./docs-site)，基于 VitePress，既是 GitHub 可读的 Markdown 源，也可构建为静态站点。

**使用**
- [快速开始](./docs-site/quickstart.md) — 安装与首次验证
- [路由指南](./docs-site/routing.md) — 如何暴露服务：前后端分离 / 多域名 / 通配 / 默认后端
- [运维与可观测](./docs-site/operations.md) — 部署参数 / Prometheus 指标 / 日志 / 健康检查 / 故障排查
- [Annotations 参考](./docs-site/annotations.md) — rewrite / canary / 治理 / 主动健康检查
- [nginx 迁移指南](./docs-site/migration-from-nginx.md)

**深入**
- [设计文档](./docs-site/DESIGN.md) — 架构、数据模型、对接 zeus 策略
- [架构](./docs-site/architecture.md) — 控制面/数据面/治理隔离
- [gRPC 路由（实验性）](./docs-site/grpc.md)

### 本地预览文档站

```bash
cd docs-site
pnpm install          # 或 npm install
pnpm docs:dev         # 开发热更新（默认 http://localhost:5173）
pnpm docs:build       # 构建静态站点到 docs-site/.vitepress/dist
pnpm docs:preview     # 预览构建产物
```

子路径部署（如 GitHub Pages）设 `DOCS_BASE`：`DOCS_BASE=/ingress/ pnpm docs:build`。

### 自动部署文档站到云主机

push 到 `main`（`docs-site/` 有变更）自动触发 [deploy-docs.yml](./.github/workflows/deploy-docs.yml)：构建静态产物 → rsync over SSH 同步到云主机 nginx 目录。

首次接入：在云主机以 root 跑一次性初始化脚本，它会建专用部署账号 + SSH 通道，并打印需填入 GitHub 的 Secrets：

```bash
sudo bash deploy/setup-docs-server.sh
# 按提示把 SERVER_HOST / SERVER_PORT / SERVER_USER / DOCS_PATH / SERVER_SSH_KEY 填入 GitHub Secrets
```

[贡献指南](./CONTRIBUTING.md) / [行为准则](./CODE_OF_CONDUCT.md) / [安全政策](./SECURITY.md)

## 模块结构

```
github.com/go-ingress/ingress
  ├── cmd/hermes              入口（demo + K8s 双模式）
  ├── pkg/model               内部路由模型（零依赖）
  ├── pkg/translator          Ingress / Gateway API → RoutingTable（纯函数）
  ├── pkg/discovery           zeus Discovery 适配（Static / K8s Endpoints）
  ├── pkg/dataplane           IngressSelector + Server + CertPool（zeus proxy）
  ├── pkg/governance          GoverningRoundTripper（zeus 熔断/限流/重试 + 被动健康检查）
  └── pkg/controller          controller-runtime Reconciler（Informer + 翻译 + status）
```

## 致谢

- [Zeus](https://github.com/go-zeus/zeus) — 服务治理框架，Hermes 的数据面与治理基石
- [ingress-nginx](https://github.com/kubernetes/ingress-nginx) — Informer + 全局重建模式参考
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — 控制面框架
- [Gateway API](https://gateway-api.sigs.k8s.io/) — 下一代 K8s 网络 API

## 许可证

[MIT](./LICENSE)
