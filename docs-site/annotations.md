# Hermes Annotations 参考

Hermes 兼容两个 annotation 前缀，`hermes.ingress.kubernetes.io/*`（原生）与 `nginx.ingress.kubernetes.io/*`（兼容，降低迁移成本）。同条 annotation 同时存在时，**hermes 前缀优先**。

## 路径重写

| Annotation | 值 | 说明 |
|---|---|---|
| `rewrite-target` | 字符串 | 把匹配的 `path` 前缀替换为该值。例：`/api` 规则 + `rewrite-target: /v2`，请求 `/api/users` → 后端收到 `/v2/users` |

## 金丝雀（Canary）

canary 通过**两条 Ingress**实现：主 Ingress 正常配置，canary Ingress 与主 Ingress 同 `host`+`path`，用 `canary: "true"` 标记，backend 指向灰度 Service。

| Annotation | 值 | 说明 |
|---|---|---|
| `canary` | `"true"` | 标记此 Ingress 为 canary（必填，否则与主 Ingress 冲突） |
| `canary-weight` | `0-100` 整数 | 按百分比随机路由到 canary（0=不发，100=全发） |
| `canary-by-header` | header 名 | 按请求 header 路由；值 `always`=始终走 canary，`never`=永不，其他值配合 `canary-by-header-value` |
| `canary-by-header-value` | 字符串 | header 等于该值时走 canary（须配合 `canary-by-header`） |
| `canary-by-cookie` | cookie 名 | cookie 值为 `true` 时走 canary |

**决策优先级**：`canary-by-header` > `canary-by-cookie` > `canary-weight`（与 ingress-nginx 一致）。

## 治理（阶段2 默认开启，策略可调）

Hermes 数据面默认接入 zeus 治理三件套，按 `service+cluster` 维度隔离：

| 治理能力 | 默认策略 | 说明 |
|---|---|---|
| 限流 | 100 rps / burst 100 | 超出返回 429 |
| 熔断 | 20 请求窗口，50% 失败率触发 | 打开后返回 503，半开自动探测 |
| 重试 | 最多 2 次，100ms 起步指数退避 | 仅重试 502/503/504 与网络错误 |
| 被动健康检查 | 连续 5 次失败标记不健康，驱逐 30s | 不健康实例从负载均衡剔除，半开恢复 |

> per-service 策略覆盖（annotation 驱动）将在后续版本支持。

## HTTPProxy CRD（高级路由）

`--enable-httpproxy` 启用。HTTPProxy（`hermes.io/v1alpha1`）补齐 Ingress v1 表达不了的能力：流量拆分、header 匹配、路径重写。

```yaml
apiVersion: hermes.io/v1alpha1
kind: HTTPProxy
metadata: { name: echo }
spec:
  host: echo.example.com
  routes:
    - conditions:
        - path: /api
          prefix: true          # true=Prefix，false=Exact
      services:
        - { name: echo, port: 80, weight: 90 }
        - { name: echo-canary, port: 80, weight: 10 }   # 流量拆分（第二个作为 canary）
      rewrite:
        prefix: /v2             # 路径重写
```

CRD 安装：`kubectl apply -f deploy/crd/httpproxy.yaml`

## per-service 治理 annotation

| Annotation | 值 | 说明 |
|---|---|---|
| `limit-rps` | 整数 | per-service 限流（每秒请求数）。0/缺省=用全局默认（100 rps）；>0 按 `service+cluster` 独立令牌桶 |

```yaml
metadata:
  annotations:
    hermes.ingress.kubernetes.io/limit-rps: "200"
```

## 主动健康检查

主动健康检查与被动健康检查互补：被动依赖真实流量失败降权，主动在无流量场景下也能剔除不健康实例（补齐 K8s readiness 滞后）。

**opt-in 设计**：Hermes **不会**对未声明的后端探测 `/health`（避免对未实现健康端点的静态站点/业务服务误判剔除导致 503）。仅当 Service 显式声明探测路径时才主动探测；其余后端只依赖被动健康检查。

| Annotation（Service） | 值 | 说明 |
|---|---|---|
| `active-health-check-path` | 路径字符串 | 声明后开启主动探测，2xx=健康，其他=不健康。例：`/healthz`、`/actuator/health` |

```yaml
apiVersion: v1
kind: Service
metadata:
  name: echo
  annotations:
    hermes.ingress.kubernetes.io/active-health-check-path: "/healthz"
spec:
  # ...
```

> 探测间隔/超时当前通过源码 `governance.NewActiveHealthCheck` 选项配置（annotation 化在路线图）。

## 路由匹配语义

- **host**：精确匹配优先，`*.example.com` 通配按精度降序兜底（`*.example.com` 匹配 `a.example.com`，不匹配 `example.com`）。
- **path**：
  - `Exact`：完全相等
  - `Prefix`：前缀匹配，按 `/` 边界（`/api` 匹配 `/api`、`/api/x`，**不**匹配 `/apis`）
  - `ImplementationSpecific`：前缀匹配（无边界约束）
- 同一 host 多路径命中时，优先级：`Exact` > `Prefix` > `ImplementationSpecific`，同类按 path 长度降序。
- 无 host/path 匹配时，走 Ingress `defaultBackend`（若配置）。
