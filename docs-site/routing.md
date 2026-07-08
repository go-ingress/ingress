# 路由指南

如何通过 Hermes 把你的服务暴露到集群外。本文面向**应用开发者**，覆盖最常见的暴露场景。安装见 [快速开始](./quickstart.md)，注解细节见 [Annotations 参考](./annotations.md)。

> 约定：以下示例假设 Hermes 已部署，`IngressClass` 名为 `hermes`，外部域名经 DNS 解析到 Hermes 接管的外部入口（如 `*.example.com`）。

## 工作原理（30 秒）

```
浏览器 → DNS 解析 → Hermes 数据面 → 按 host/path 匹配 RoutingTable → 转发到 Service → Pod
```

Hermes 监听带 `spec.ingressClassName: hermes` 的 Ingress，翻译成内部路由表，热更新到数据面（零重启）。控制面只认 `spec.ingressClassName`，不看旧式 `kubernetes.io/ingress.class` 注解。

## 1. 最简单的暴露：单服务

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  namespace: myapp
  name: myapp
spec:
  ingressClassName: hermes
  rules:
    - host: myapp.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend: { service: { name: web, port: { number: 80 } } }
```

访问 `http://myapp.example.com/` 即到达 `myapp/web` 服务的 80 端口。

## 2. 同一域名多路径：前端 + 后端分离

最常见模式——前端静态站点在 `/`，后端 API 在 `/api`。**按路径长度降序匹配**，更具体的 `/api` 优先于 `/`：

```yaml
rules:
  - host: app.example.com
    http:
      paths:
        - path: /api
          pathType: Prefix
          backend: { service: { name: api, port: { number: 8080 } } }
        - path: /
          pathType: Prefix
          backend: { service: { name: web, port: { number: 80 } } }
```

- `/`、`/index.html`、`/assets/app.js` → `web`
- `/api/users`、`/api/orders` → `api`

> **注意前端硬编码路径**：若前端代码里把某个后端入口写在根路径（如 `fetch('/login')` 而非 `fetch('/api/login')`），需要像 `/api` 一样为它单独加一条规则指向后端，否则会落到 `/` 的前端服务。不要为了「整齐」强行重写——改前端 bundle 成本更高。

## 3. 多域名：一个 Ingress 多 host

```yaml
rules:
  - host: admin.example.com
    http: { paths: [ { path: /, pathType: Prefix, backend: { service: { name: admin, port: { number: 80 } } } } ] }
  - host: www.example.com
    http: { paths: [ { path: /, pathType: Prefix, backend: { service: { name: www, port: { number: 80 } } } } ] }
```

## 4. 通配 host

`*.example.com` 匹配任意一级子域（`a.example.com`、`b.example.com`），但**不**匹配 `example.com` 本身。多个通配按精度降序兜底：

```yaml
rules:
  - host: "*.example.com"      # 兜底：a.example.com, b.example.com
    http: { ... }
```

精确 host 永远优先于通配。

## 5. 默认后端

未匹配任何 host/path 的请求，若 Ingress 配了 `spec.defaultBackend` 则走它，否则返回 404：

```yaml
spec:
  ingressClassName: hermes
  defaultBackend:
    service: { name: fallback, port: { number: 80 } }
```

## 6. 命名端口

后端端口可用 Service 里定义的端口名而非数字：

```yaml
backend: { service: { name: api, port: { name: http } } }
```

Hermes 会解析 Service 的 `spec.ports[].name` 自动映射。命名端口的好处：Service 改端口号不用同步改 Ingress。

## 7. path 匹配语义

| pathType | 行为 |
|---|---|
| `Exact` | 完全相等（`/api` 只匹配 `/api`） |
| `Prefix` | 前缀 + `/` 边界（`/api` 匹配 `/api`、`/api/x`，**不**匹配 `/apis`） |
| `ImplementationSpecific` | 前缀匹配，无边界约束 |

同一 host 多路径命中时优先级：`Exact` > `Prefix` > `ImplementationSpecific`，同类按 path 长度降序。

## 8. 路径重写

把匹配的前缀替换掉，用 annotation（`hermes` 与 `nginx` 前缀都认）：

```yaml
metadata:
  annotations:
    hermes.ingress.kubernetes.io/rewrite-target: /v2
# 规则 path: /api，rewrite-target: /v2
# 请求 /api/users → 后端收到 /v2/users
```

## 9. 金丝雀（灰度）

两条同 `host`+`path` 的 Ingress，主 Ingress 正常配，canary Ingress 用 `canary: "true"` 标记并指向灰度 Service。详见 [Annotations 参考 · 金丝雀](./annotations.md#金丝雀canary)。

按权重：`hermes.ingress.kubernetes.io/canary-weight: "10"`（10% 流量灰度）。
按 header：`canary-by-header: x-canary` + `canary-by-header-value: true`。

## 10. TLS / HTTPS

在 Ingress 的 `spec.tls` 引用同命名空间的 `Secret`（类型 `kubernetes.io/tls`）：

```yaml
spec:
  ingressClassName: hermes
  tls:
    - hosts: [app.example.com]
      secretName: app-tls
  rules: [ ... ]
```

Hermes 控制面加载 Secret 到证书池，按 SNI 选择证书。证书更新自动热加载，无需重启。

> 当前默认数据面以 HTTP 模式运行（明文，常用于经上级 LB/TLS 终止的场景）。直接由 Hermes 终止 TLS 的双端口模式在路线图。

## 11. Gateway API / HTTPProxy（高级路由）

Ingress v1 表达不了流量拆分、header 匹配时，启用高级路由：

- **HTTPRoute**（Gateway API）：`--enable-gateway-api`
- **HTTPProxy CRD**（`hermes.io/v1alpha1`）：`--enable-httpproxy`，支持流量拆分/header 匹配/路径重写，见 [Annotations 参考 · HTTPProxy CRD](./annotations.md#httpproxy-crd高级路由)。

两者与 Ingress 共存，翻译后合并到同一路由表。

## 排错速查

| 现象 | 可能原因 |
|---|---|
| 404 Not Found | host/path 未匹配任何规则，且无 defaultBackend |
| 503 Service Unavailable | 后端 Service 无就绪 endpoint / 实例全被熔断或健康检查剔除 / 无路由表 |
| 429 Too Many Requests | 触发限流（超 `limit-rps` 或全局 100 rps） |
| 502 Bad Gateway | 连上后端但响应失败（后端错误/协议不匹配） |

诊断步骤：`kubectl get endpoints <svc> -n <ns>` 确认有就绪端点 → 看 Hermes 日志 `kubectl logs deploy/hermes -n hermes-system` 中的 `status=` 与 `duration=` → 见 [运维与可观测](./operations.md)。
