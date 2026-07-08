# 运维与可观测

面向 **SRE / 平台运维**：部署参数、监控指标、日志、健康检查、高可用与故障排查。

## 部署参数（启动 flag）

| Flag | 默认 | 说明 |
|---|---|---|
| `--addr` | `:8080` | 数据面监听地址（接收外部流量） |
| `--ingress-class` | `hermes` | 只处理 `spec.ingressClassName` 等于此值的 Ingress；空=处理全部 |
| `--watch-namespace` | `""` | 限定监听命名空间；空=全命名空间 |
| `--metrics-addr` | `:8081` | controller-runtime metrics 端点（`/metrics`） |
| `--status-hostname` | `""` | 写入 Ingress `status.loadBalancer.hostname`（如 LB 域名） |
| `--status-address` | `""` | 写入 Ingress `status.loadBalancer.ip` |
| `--enable-gateway-api` | `false` | 启用 Gateway API（HTTPRoute）翻译 |
| `--enable-httpproxy` | `false` | 启用 Hermes HTTPProxy CRD 翻译 |
| `--leader-elect` | `false` | 启用 leader election（多副本 HA 时开启） |
| `--demo` | `false` | demo 模式：硬编码路由 + 内嵌 echo，无 K8s 依赖（开发验证用） |

生产多副本推荐：`--leader-elect` 开启 + 副本数 ≥ 2 + PodDisruptionBudget（Helm chart 已内置）。

## 监控指标（Prometheus）

metrics 端点 `http://<hermes-pod>:8081/metrics`，三个核心指标：

| 指标 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `hermes_requests_total` | Counter | `host`, `method`, `status` | 处理的请求总数 |
| `hermes_request_duration_seconds` | Histogram | `host`, `method`, `status` | 请求耗时（默认桶） |
| `hermes_upstream_failures_total` | Counter | `host` | 后端失败次数（5xx 响应） |

### 常用 PromQL

```txt
# 各 host 请求速率（QPS）
sum by (host) (rate(hermes_requests_total[1m]))

# 各 host P99 延迟
histogram_quantile(0.99, sum by (host, le) (rate(hermes_request_duration_seconds_bucket[5m])))

# 5xx 错误率
sum by (host) (rate(hermes_requests_total{status=~"5.."}[1m]))
  / sum by (host) (rate(hermes_requests_total[1m]))

# 后端失败率（熔断/健康问题的早期信号）
sum by (host) (rate(hermes_upstream_failures_total[5m]))
```

建议告警：某 host 5xx 率 > 1% 持续 5min；某 host `upstream_failures` 速率突增。

## 访问日志

每个请求一行结构化日志（slog），字段：

```
[INFO] req GET /api/users status=200 duration=1.5ms ip=10.0.0.1:39498 request_id=8bcf03f6...
```

- `status`：HTTP 状态码
- `duration`：总耗时（含后端）
- `ip`：客户端地址
- `request_id`：请求追踪 ID（从 `X-Request-Id` 取或自动生成）

查看：`kubectl logs deploy/hermes -n hermes-system`

> 日志默认不含请求体/响应体。`duration` 异常高通常意味着后端慢（Hermes 本身转发开销在微秒级）。

## 健康检查

Hermes 用**两层**健康检查保护可用性：

| 层级 | 默认 | 行为 |
|---|---|---|
| **被动健康检查** | 始终开启 | 真实请求连续 5 次失败（5xx/网络错误）→ 标记不健康 → 从负载均衡剔除 30s → 半开自动探测恢复 |
| **主动健康检查** | **opt-in** | 周期探测 Service 声明的健康路径，2xx=健康，其他=不健康。**仅当 Service 显式声明** `hermes.ingress.kubernetes.io/active-health-check-path` 才探测 |

**为什么主动检查是 opt-in**：主动探测 `/health` 对未实现该端点的后端（静态站点等）会误判剔除导致 503。被动检查（基于真实流量）才是安全默认。需要主动检查时，在 **Service** 上加注解：

```yaml
apiVersion: v1
kind: Service
metadata:
  annotations:
    hermes.ingress.kubernetes.io/active-health-check-path: "/healthz"
```

## 治理（熔断/限流/重试）

数据面默认接入 zeus 治理，按 `service+cluster` 维度隔离，无需配置即生效：

| 能力 | 默认策略 | 超出行为 |
|---|---|---|
| 限流 | 100 rps / burst 100 | 429 |
| 熔断 | 20 请求窗口，50% 失败率触发 | 503，半开自动探测 |
| 重试 | 最多 2 次，100ms 起步指数退避 | 仅重试 502/503/504 与网络错误 |

per-service 限流覆盖：在 Service 或 Ingress 加 `hermes.ingress.kubernetes.io/limit-rps: "200"`（按 `service+cluster` 独立令牌桶）。详见 [Annotations 参考](./annotations.md)。

## 高可用（HA）

- **单副本**：控制面 + 数据面同进程，足够开发/小规模生产。
- **多副本**：`--leader-elect` 开启后，**控制面**只有 leader 做 Reconcile（避免重复写 status）；**数据面**所有副本同时接流量（前端 LB/NodePort 负载均衡）。无状态转发，副本可水平扩展。
- **滚动更新**：路由表原子热更新（atomic pointer swap），配置变更不丢连接、不重启。

## Ingress status

`--status-hostname` / `--status-address` 把外部可达地址写入被管理 Ingress 的 `status.loadBalancer`，供平台工具（如 `kubectl get ingress` 看到 ADDRESS 列）和外部 DNS controller 消费。仅值变化时才写，避免无谓 API 调用。

## 故障排查

### 某域名 503 Service Unavailable
1. `kubectl get endpoints <svc> -n <ns>` —— 有就绪端点吗？无 = 后端问题（Deployment 没 pod / readinessProbe 失败），与 Hermes 无关。
2. 有端点却 503 —— 看是否被健康检查/熔断剔除：日志里该 host 的 `status` 序列；主动健康检查是否误配（Service 上挂了 `active-health-check-path` 指向后端不存在的路径）。
3. `kubectl get ingress <name>` 确认 `spec.ingressClassName: hermes` 且 host 拼写正确。

### 某域名 404
host/path 没匹配到规则。检查：`spec.ingressClassName: hermes`（不是注解）、host 精确拼写、pathType。旧式 `kubernetes.io/ingress.class` 注解 Hermes **不识别**。

### 配置改了不生效
Hermes 由 Informer 事件触发全局 Reconcile，通常 < 1s。看日志 `routing table updated ingresses=N hosts=M` 确认表已重建。若没看到该日志，确认改的 Ingress class 正确、是否在 `--watch-namespace` 范围内。

### metrics 抓不到
确认 metrics-addr 的 Service 暴露且 Prometheus 抓取目标可达；`curl http://<pod-ip>:8081/metrics` 应返回文本。

## 集群接管 / 替换现有 ingress 控制器

把现有 ingress 控制器（如 ingress-nginx）切到 Hermes 的安全步骤：

1. **并行期**：Hermes 用独立 `IngressClass`（如 `hermes`）部署，新 Ingress 用 `class: hermes`，旧的不动。两条控制器共存，各管各的 class。
2. **逐条迁移**：把要接管的 Ingress 的 `spec.ingressClassName` 改为 `hermes`（旧式 `kubernetes.io/ingress.class` 注解记得删除，否则旧控制器可能仍认领）。
3. **验证**：每个迁移的域名经 Hermes 入口返回正常。
4. **缩容旧控制器**：全部迁移完成后，旧控制器 `replicas=0` 保留一段时间供回滚，观察无异常再删除。

> **不要直接删旧控制器 Service 再迁**——先确认 Hermes 入口已接管外部端口（NodePort/LB），否则会有外部中断窗口。回滚：把 `ingressClassName` 改回旧值 + 恢复旧控制器副本。
