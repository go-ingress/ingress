# 架构

Hermes = **K8s 控制面（新建）+ Zeus 数据面（复用）**，同进程单二进制。

## 控制面（controller-runtime）

```
Informer: Ingress / HTTPRoute / Service / Endpoints / Secret
   ↓ globalReconcile（任意变更 → 固定 key "global" 入队）
Reconcile（全局重建）
   ├─ List Ingress（按 ingressClass 过滤）+ List HTTPRoute（--enable-gateway-api 时）
   ├─ translator.BuildTable / BuildTableFromHTTPRoute → RoutingTable
   ├─ atomic.Store(&table) → 推送数据面 IngressSelector
   ├─ Endpoints → K8sDiscovery.SetService
   ├─ TLS Secret → CertPool.SetCert
   └─ Ingress status.LoadBalancer 更新
```

- **零 reload**：Endpoints 走 K8sDiscovery 缓存；路由/TLS 走 atomic pointer swap。
- **高可用**：`--leader-elect` 多副本 leader election（lease 锁）。

## 数据面（zeus proxy）

```
请求
  → requestid → accesslog → metrics → recovery（middleware 链）
  → TLS SNI（CertPool.GetCertificate，Secret 热加载）
  → IngressSelector.Pick
      ├─ RoutingTable.Match（host 精确/通配 + path Exact/Prefix + method）
      ├─ canary 决策（header > cookie > weight）
      ├─ K8sDiscovery.GetService → 按 cluster+port 筛选 → 被动+主动健康过滤
      └─ balancer.Next() → *url.URL（注入 BackendInfo 到 context）
  → zeus proxy（HTTP / WebSocket / SSE 自动分流）
  → GoverningRoundTripper
      ├─ 限流（per-key LimitRPS 或全局默认，cluster.ClusterLimiter）
      ├─ 熔断（cluster.ClusterBreaker，fn 未执行=打开）
      ├─ 重试（cluster.ClusterRetrier，502/503/504 + 网络错误，body 重放）
      └─ 被动健康检查 Report（5xx/超时降权）
  → 后端 Pod
```

## 治理隔离

按 `service+cluster` 维度独立熔断/限流/重试桶（zeus cluster 包）。

- canary 流量映射为独立 service（不同 BackendRef.ServiceName）→ 自动独立治理桶，canary 故障不影响主流量。
- per-service 限流：`hermes.ingress.kubernetes.io/limit-rps` annotation 触发独立令牌桶。

## 健康检查

| 类型 | 触发 | 行为 |
|---|---|---|
| 被动 | 真实流量 5xx/超时 | 连续 5 次失败标记不健康，驱逐 30s，半开恢复 |
| 主动 | 周期探测 `/health`（10s） | 2xx=健康，其他=不健康，补齐无流量场景 |

两者共享 `PassiveHealthCheck` 的不健康实例 map，`IngressSelector.Pick` 过滤。

## 热更新矩阵

| 变化 | 生效方式 | 延迟 |
|---|---|---|
| Endpoints 增删 | K8sDiscovery.SetService → 下次 Pick | 毫秒级 |
| Ingress/HTTPRoute 路由 | RoutingTable atomic swap → 下次 Pick | 毫秒级 |
| TLS Secret 轮转 | CertPool.SetCert → 下次握手 | 毫秒级 |
| 治理策略 | GoverningRoundTripper 配置（运行期固定） | 重启 |

## 模块依赖

```
cmd/hermes
  ├─ controller（controller-runtime + k8s.io/* + gateway-api）
  ├─ dataplane（zeus proxy/middleware/balancer + governance + model）
  ├─ governance（zeus 熔断/限流/重试 + 健康检查）
  ├─ discovery（zeus registry.Discovery 适配）
  ├─ translator（k8s.io 类型 → model，纯函数）
  └─ model（零依赖路由模型）
```

控制面与数据面经 `TableSink` / `CertUpdater` 接口解耦（鸭子类型），无循环依赖。
