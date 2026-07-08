# Hermes — 基于 Zeus 服务治理的 K8s Ingress 控制器设计方案

> 产品代号 **Hermes**（赫尔墨斯，宙斯之子，掌管门户、道路与信使 —— 呼应 `go-zeus/zeus` 体系）。
> 仓库：`github.com/go-ingress/ingress`。主线目标：做一个**功能完整、治理内建、可插拔**的开源 K8s Ingress 产品。

---

## 1. 背景与定位

### 1.1 现状

- 当前仓库 `go-ingress/ingress` 为空仓库（仅 git 历史）。
- `go-zeus/zeus` 已是一个成熟的零依赖 Go 微服务框架，具备完整服务治理能力：`proxy`（HTTP/WS/SSE 反向代理）、`registry`（服务发现，概念对齐 K8s Endpoints + Istio ServiceEntry）、`balancer`、`circuitbreaker`/`ratelimit`/`retry`（按 cluster 隔离）、`routing`（`X-Zeus-Cluster` 端到端路由）、`propagation`（W3C Baggage）、`middleware`、可观测性三件套（trace/metrics/log）、`components`（声明式装配 + 生命周期）。
- 但 zeus **没有 K8s 控制面**：不监听 Ingress/Service/Endpoints 资源，`proxy.Selector` 只支持 cluster 路由，不支持 host/path/header 匹配；治理三件套未集成进 proxy 请求路径；无 TLS 卸载、无健康检查、无 Gateway API 支持。

### 1.2 定位

Hermes = **K8s 控制面（新建）+ Zeus 数据面（复用）**。

- **控制面**：监听 K8s 资源（Ingress / Gateway API / Service / Endpoints / EndpointSlice / Secret / ConfigMap），翻译为内部路由模型，原子推送给数据面。
- **数据面**：复用 zeus `proxy` 作为反向代理核心，实现 `IngressSelector` 做 7 层路由匹配，通过 `WithTransport` 接入 zeus 治理三件套，通过 K8s Endpoints 适配器实现 zeus `registry.Discovery`。

**与业界产品对标**：

| 产品 | 控制面 | 数据面 | 治理内建 | Go 原生 |
|---|---|---|---|---|
| ingress-nginx | Go (client-go informer) | nginx + Lua | 弱（靠 annotation+Lua） | 否（数据面 C） |
| Traefik | Go | Go（自有） | 中 | 是 |
| Contour | Go (controller-runtime) | Envoy | 弱（委托 Envoy） | 否（数据面 C++） |
| Envoy Gateway | Go | Envoy | 弱 | 否 |
| HAProxy Ingress | Go | HAProxy | 弱 | 否 |
| Kong | Go/Lua | nginx + OpenResty | 强（插件） | 否 |
| **Hermes** | **Go (controller-runtime)** | **Go（zeus proxy）** | **强（zeus 治理原生内建）** | **是（全栈 Go）** |

**差异化卖点**：业界唯一一个**全栈 Go + 治理能力原生内建**（熔断/限流/重试/灰度/链路追踪开箱即用）的 Ingress 控制器。数据面无 C/C++ 依赖，部署为一个静态二进制；治理语义与 zeus 微服务框架端到端打通（同一套 cluster/baggage 模型）。

---

## 2. 设计目标

| 目标 | 指标 |
|---|---|
| 功能完整 | 覆盖 Ingress v1 全部语义 + Gateway API HTTPRoute 核心语义 |
| 治理内建 | 熔断/限流/重试/灰度/超时/重写，原生支持，无需 Lua |
| 配置热更新 | Endpoints 变化零 reload；路由/TLS 变化亚秒级生效（原子 swap） |
| 可观测 | access log / metrics / trace 全链路，cluster 维度标签 |
| 可插拔 | 数据面扩展点 + CRD 高级路由 + Plugin 机制 |
| 零外部数据面依赖 | 单二进制，无 nginx/Envoy 依赖 |
| 向下兼容 | 兼容 ingress-nginx 主流 annotation（canary/rewrite/redirect），降低迁移成本 |

**遵循 KISS / YAGNI / DRY / SOLID**：
- KISS：控制面单一 reconcile 循环，数据面 `http.Handler` 直链。
- YAGNI：首版不做 Service Mesh、不做 eBPF、不做非 HTTP 协议（TCP/UDP 留扩展点不实现）。
- DRY：路由模型统一，Ingress 与 Gateway API 共用一套翻译后端。
- SOLID：控制面/数据面/发现/治理四层接口隔离，依赖抽象。

---

## 3. 整体架构

```
┌─────────────────────────────── Kubernetes Cluster ───────────────────────────────┐
│                                                                                   │
│   ┌─────────────────────── Control Plane (Hermes Controller) ──────────────────┐  │
│   │                                                                             │  │
│   │  Informers (controller-runtime)                                            │  │
│   │    ├─ Ingress (networking.k8s.io/v1)         ─┐                            │  │
│   │    ├─ HTTPRoute/Gateway (gateway.networking.k8s.io/v1) ─ Translator ─┐     │  │
│   │    ├─ Service / Endpoints / EndpointSlice    ─┤                       │     │  │
│   │    ├─ Secret (TLS)                            ─┘                       │     │  │
│   │    └─ ConfigMap (全局配置/模板)                                        │     │  │
│   │                                                                        ▼     │  │
│   │                              ┌────────────────────────────┐                  │  │
│   │   Reconcile Loop ◄────────── │  RoutingTable (内部模型)   │                  │  │
│   │   (workqueue + diff)         │  host → path → backend     │                  │  │
│   │        │                     │  + canary + tls + rules    │                  │  │
│   │        ▼                     └────────────┬───────────────┘                  │  │
│   │   atomic.Store(&table)                   │ publish (atomic pointer swap)     │  │
│   └──────────────────────────────────────────┼─────────────────────────────────┘  │
│                                              │                                     │
│   ┌─────────────────────────── Data Plane (in-process) ───────────────────────┐  │
│   │                                                                          ▼  │  │
│   │   http.Server (:80 / :443)                                              │  │
│   │        │                                                                 │  │
│   │        ▼                                                                 │  │
│   │   zeus middleware.Chain (requestid → accesslog → recovery → tracing → metrics) │
│   │        │                                                                 │  │
│   │        ▼                                                                 │  │
│   │   TLS termination (SNI 多证书，Secret 热加载)                            │  │
│   │        │                                                                 │  │
│   │        ▼                                                                 │  │
│   │   ┌──────────────────────────────────────────────────────────────────┐   │  │
│   │   │  IngressSelector (实现 zeus proxy.Selector)                       │   │  │
│   │   │   1. host 匹配 (精确 + 通配 *.example.com)                        │   │  │
│   │   │   2. path 匹配 (Prefix / Exact / ImplementationSpecific)          │   │  │
│   │   │   3. header / method / query 匹配 (Gateway API)                   │   │  │
│   │   │   4. canary 决策 (weight / header / cookie → cluster)             │   │  │
│   │   │   5. 选 backend service → K8sDiscovery.GetService(svc)            │   │  │
│   │   │   6. zeus balancer.Next() 选 Instance → *url.URL                  │   │  │
│   │   └──────────────────────────────────────────────────────────────────┘   │  │
│   │        │                                                                 │  │
│   │        ▼                                                                 │  │
│   │   zeus proxy.Proxy (HTTP / WebSocket / SSE 自动分流)                     │  │
│   │        │  WithTransport = GoverningRoundTripper                          │  │
│   │        ▼                                                                 │  │
│   │   ┌──────────────────────────────────────────────────────────────────┐   │  │
│   │   │  GoverningRoundTripper (按 service+cluster 隔离)                   │   │  │
│   │   │    ├─ ratelimit (cluster.ClusterLimiter)    限流                   │   │  │
│   │   │    ├─ circuitbreaker (cluster.ClusterBreaker) 熔断                 │   │  │
│   │   │    ├─ retry (cluster.ClusterRetrier)        重试                   │   │  │
│   │   │    ├─ timeout                               超时                   │   │  │
│   │   │    └─ passive health check (5xx/超时降权)   被动健康检查           │   │  │
│   │   └──────────────────────────────────────────────────────────────────┘   │  │
│   │        │                                                                 │  │
│   │        ▼                                                                 │  │
│   │   后端 Pod (K8s Endpoints)                                               │  │
│   └──────────────────────────────────────────────────────────────────────────┘  │
│                                                                                   │
│   ┌─────────────────────────── K8sDiscovery Adapter ──────────────────────────┐  │
│   │  实现 zeus registry.Discovery + Watcher                                   │  │
│   │  数据源：Endpoints / EndpointSlice Informer 缓存                          │  │
│   │  Endpoints → zeus types.Instance (IP:Port, cluster=svc-ns/name)          │  │
│   │  原子 swap 缓存；Watcher 通过 informer event 通知 IngressSelector         │  │
│   └──────────────────────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────────────────┘
```

**关键设计**：控制面与数据面**同进程**（单二进制），通过**原子指针 swap** 传递路由表，无 IPC、无 reload。这与 ingress-nginx（Go 控制 + nginx 数据面 + Lua 桥接）和 Contour（xDS gRPC 下发 Envoy）都不同，更接近 Traefik 的同进程模型，但治理能力远强于 Traefik。

---

## 4. 与 Zeus 的对接策略（逐模块）

### 4.1 对接点总览

| Zeus 能力 | Hermes 用法 | 对接方式 |
|---|---|---|
| `proxy.Proxy` + `Selector` | 数据面反向代理核心 | 实现 `IngressSelector`，`proxy.New(WithSelector, WithTransport, WithDirector)` |
| `registry.Discovery` + `Watcher` | K8s Endpoints 作为注册中心 | 新建 `K8sDiscovery` 适配器，实现接口 |
| `balancer.Balancer` | 后端负载均衡 | 复用 `roundrobin`/`random`，按 service 维度持有实例池 |
| `circuitbreaker/cluster` | 熔断 | 包进 `GoverningRoundTripper`，按 `service+cluster` 隔离 |
| `ratelimit/cluster` | 限流 | 同上 |
| `retry/cluster` | 重试 | 同上 |
| `routing` (`X-Zeus-Cluster`) | 灰度/金丝雀 | canary service → cluster；端到端透传 |
| `propagation` (Baggage) | 全链路上下文 | 透传到后端 zeus 服务，自动 extract |
| `middleware.Chain` | 请求中间件链 | 包在 proxy 外层（requestid/accesslog/recovery/tracing/metrics） |
| `trace`/`metrics`/`log` | 可观测性 | 复用 otel/prometheus/slog 插件 |
| `plugins/config/k8s` | ConfigMap 配置 | 复用（全局配置/错误页模板） |

### 4.2 关键适配一：`IngressSelector`（补齐 zeus 缺失的 7 层路由）

zeus `proxy.Selector` 接口：
```go
type Selector interface {
    Pick(r *http.Request) (*url.URL, error)
}
```
当前 `NewDiscoverySelector` 只按 `X-Zeus-Cluster` 路由，不做 HTTP 匹配。Hermes 实现：

```go
// IngressSelector 实现 zeus proxy.Selector，做完整 7 层路由 + canary + 负载均衡。
type IngressSelector struct {
    table atomic.Pointer[RoutingTable]   // 控制面原子推送
    disc  registry.Discovery              // K8sDiscovery 适配器
    lbs   sync.Map                        // service -> balancer.Balancer（实例变化时重建）
}

func (s *IngressSelector) Pick(r *http.Request) (*url.URL, error) {
    table := s.table.Load()
    // 1. host 匹配（精确优先，通配兜底）
    route := table.MatchHostPath(r.Host, r.URL.Path, r.Method, r.Header)
    if route == nil {
        return nil, ErrNotFound  // → 404
    }
    // 2. canary 决策：weight/header/cookie → 选择主 backend 或 canary backend
    backend := route.DecideCanary(r)  // backend 含 serviceName + cluster
    // 3. 服务发现 + 负载均衡
    entry, err := s.disc.GetService(r.Context(), backend.ServiceName)
    if err != nil || len(entry.Instances) == 0 {
        return nil, ErrUnavailable  // → 503
    }
    lb := s.getOrCreateLB(backend, entry)  // 实例签名变化时重建
    ins := lb.Next()
    // 4. cluster 路由（canary 映射为 cluster，或透传 X-Zeus-Cluster）
    target := &url.URL{Scheme: "http", Host: net.JoinHostPort(ins.IP, strconv.Itoa(ins.Port))}
    return target, nil
}
```

### 4.3 关键适配二：`K8sDiscovery`（K8s Endpoints 作为 zeus 注册中心）

```go
// K8sDiscovery 把 K8s Endpoints/EndpointSlice 暴露为 zeus registry.Discovery。
type K8sDiscovery struct {
    epInformer  cache.SharedIndexInformer   // Endpoints 或 EndpointSlice
    mu          sync.RWMutex
    cache       map[string]*types.ServiceEntry  // svcKey(ns/name) -> ServiceEntry
    watchers    map[string][]chan struct{}      // svcKey -> watchers
}

// GetService 返回 ServiceEntry（实例 = Ready 的 Endpoint subsets）
func (d *K8sDiscovery) GetService(ctx context.Context, name string) (*types.ServiceEntry, error) { ... }
// Watch 返回变更通知 channel（informer OnAdd/OnUpdate/OnDelete 触发）
func (d *K8sDiscovery) Watch(ctx context.Context, name string) (<-chan struct{}, error) { ... }
```

**Endpoints → Instance 映射**：
- `Instance.IP/Port` ← Endpoint Address + Port
- `Instance.Name` ← `service.namespace`（zeus 服务名约定）
- `Instance.Cluster` ← `default`（主流量）或 canary 服务对应的 cluster 名
- `Instance.Protocol` ← `http`（按 Service port name/appProtocol 推断）
- `Instance.Labels` ← Endpoint labels + Service labels（供路由/灰度匹配）

### 4.4 关键适配三：`GoverningRoundTripper`（治理三件套接入 proxy）

zeus 治理组件目前是 client 侧独立模块，未进 proxy。Hermes 通过 `proxy.WithTransport` 注入：

```go
// GoverningRoundTripper 把 zeus 熔断/限流/重试接入 proxy 请求路径，按 service+cluster 隔离。
type GoverningRoundTripper struct {
    next    http.RoundTripper            // 底层 transport（连接池）
    cb      *clusterbreaker.ClusterBreaker
    limiter *clusterlimiter.ClusterLimiter
    retrier *clusterretry.ClusterRetrier
    cfg     atomic.Pointer[GovernanceConfig]  // 控制面下发的每服务策略
}

func (g *GoverningRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    svc, cluster := backendFromContext(req.Context())  // Pick 阶段注入
    // 1. 限流
    if !g.limiter.Allow(req.Context()) { return nil, ErrRateLimited }  // → 429
    // 2. 熔断 + 重试（熔断内包重试）
    var resp *http.Response
    err := g.cb.Execute(req.Context(), func() error {
        return g.retrier.Do(req.Context(), func() error {
            resp, err = g.next.RoundTrip(req)
            return classifyErr(resp, err)  // 5xx/超时 → 可重试错误
        })
    })
    // 3. 被动健康检查：失败则上报 K8sDiscovery 降权
    return resp, err
}
```

### 4.5 关键适配四：灰度/金丝雀对接 zeus cluster

nginx-ingress 用 `canary-weight`/`canary-by-header`/`canary-by-cookie` annotation。Hermes 兼容这套 annotation，但**底层用 zeus cluster 模型实现**，获得端到端传播能力：

| Annotation | Hermes 行为 |
|---|---|
| `hermes.ingress.kubernetes.io/canary: "true"` | 标记 canary Ingress（同 ingress-nginx 语义） |
| `hermes.ingress.kubernetes.io/canary-weight: "30"` | 30% 流量路由到 canary backend |
| `hermes.ingress.kubernetes.io/canary-by-header: "X-Canary"` | header=always/never 或自定义值路由 canary |
| `hermes.ingress.kubernetes.io/canary-by-cookie: "canary"` | cookie 路由 canary |

**实现**：canary backend 映射为独立 zeus cluster（如 `svc-ns/name#canary`），`IngressSelector.DecideCanary` 决策后把 cluster 写入请求 context（`routing.WithCluster`），后续 `GoverningRoundTripper`/后端 zeus 服务自动按 cluster 隔离治理 + 链路传播。**这是 Hermes 相对 ingress-nginx 的关键升级**：灰度流量在 ingress 层和后端服务层共享同一 cluster 语义，可观测性（trace/metrics/log 自动带 cluster label）和治理（独立熔断/限流桶）端到端打通。

---

## 5. 核心数据模型

```go
// RoutingTable 是控制面翻译、数据面消费的不可变路由表。
// 控制面每次 reconcile 生成新版本，atomic.Store 推送给 IngressSelector。
type RoutingTable struct {
    Version   int64                 // 单调递增
    Hosts     map[string]*HostRules // host（精确/通配）-> 规则
    Wildcards []*HostRules          // *.example.com 通配规则（按精度排序）
    TLS       map[string]*TLSCert   // host -> 证书（SNI）
    Governance map[svcKey]*GovernanceConfig  // 每服务治理策略
}

type HostRules struct {
    Host    string         // 精确 host 或 *.example.com
    Paths   []*PathRule    // 按长度/精度排序
}

type PathRule struct {
    PathType  PathType       // Prefix / Exact / ImplementationSpecific
    Path      string
    Method    string         // Gateway API method 匹配，空=任意
    Headers   []HeaderMatch  // Gateway API header 匹配
    Backend   *BackendRef    // 主后端
    Canary    *CanaryConfig  // 可选灰度配置
    Rewrite   *RewriteConfig // 路径重写
    Redirect  *RedirectConfig
    Timeout   time.Duration
    // ...更多 annotation 翻译结果
}

type BackendRef struct {
    ServiceName string  // ns/name
    Port        int
    Scheme      string  // http / https
    Weight      int     // 多后端权重（Gateway API）
}

type CanaryConfig struct {
    Weight  int
    Header  string
    Cookie  string
    Backend *BackendRef
}
```

**匹配算法**（对齐 Ingress v1 + Gateway API 语义）：
1. host 精确匹配 → 命中则用；否则最长通配匹配；否则默认 backend（`*`）。
2. path：Exact > Prefix（按长度降序）；Prefix 匹配按 `/` 边界切分比较。
3. canary 决策：header/cookie 优先，其次 weight（一致性哈希/随机）。

---

## 6. 控制面设计

### 6.1 技术选型：controller-runtime

**选 controller-runtime 而非裸 client-go informer**：
- controller-runtime 提供 Manager / Cache / Webhook / Leader Election / prometheus metrics 开箱即用。
- ingress-nginx 用裸 informer 是历史包袱（项目早于 controller-runtime），新项目不应重蹈。
- Contour、Envoy Gateway、Cilium 等新一代控制器均用 controller-runtime。

**依赖**：
```
sigs.k8s.io/controller-runtime  (控制面框架)
k8s.io/api + k8s.io/client-go + k8s.io/apimachinery  (K8s 客户端)
sigs.k8s.io/gateway-api/apis/v1  (Gateway API，可选启用)
github.com/go-zeus/zeus/...      (数据面 + 治理)
```

### 6.2 Reconcile 流程

```
任意资源变更 → 入队 (namespace/name 或全局 key)
   ↓
Reconcile(ctx, req)
   ↓
1. 从 Cache List 全部 Ingress（按 ingressClass 过滤）+ Gateway/HTTPRoute（按 controllerName 过滤）
2. List 相关 Service / Endpoints / Secret，组装 RoutingTable
3. 与 running 版本 diff
   ├─ 仅 Endpoints 变化 → 不重建 table，K8sDiscovery 内部 informer 已原子更新缓存（零开销）
   └─ 路由/TLS/治理变化 → 构建新 RoutingTable，atomic.Store 推送
4. 更新 Ingress status（LB ingress IP/host）
5. 返回 nil，下次有变更再触发
```

**热更新分级**（借鉴 ingress-nginx 但更简洁）：
- **L0 Endpoints 变化**：K8sDiscovery 的 informer 直接更新缓存，`Pick` 下次调用即生效，**不进 reconcile，不重建 table**。
- **L1 路由/TLS/治理变化**：重建 RoutingTable，atomic swap，**无 reload**（数据面是 Go 内存结构，指针替换即生效）。
- **L2 监听端口/证书文件变化**：仅启动时确定；运行期 TLS 证书通过 `tls.Config.GetCertificate` 动态返回，亦无 reload。

### 6.3 IngressClass 与多租户

- 通过 `spec.ingressClassName: hermes` 过滤，`--watch-ingress-class` 可配。
- `--watch-namespace` 支持单/多/全命名空间，多副本 + leader election 保证高可用。

---

## 7. 数据面设计

### 7.1 请求处理链

```
TCP accept
  → tls.Config.GetCertificate (SNI 动态证书)
  → zeus middleware.Chain: requestid → accesslog → recovery → tracing → metrics → timeout
  → IngressSelector.Pick (路由匹配 + canary + LB)
  → zeus proxy.ServeHTTP (HTTP/WS/SSE 分流)
  → GoverningRoundTripper (限流/熔断/重试/被动健康检查)
  → 后端 Pod
```

### 7.2 TLS 卸载

- Secret 类型 `kubernetes.io/tls` 加载为 `tls.Certificate`，缓存于 `RoutingTable.TLS`。
- `tls.Config.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error)` 按 SNI 返回，无通配则用默认证书。
- 证书轮转：Secret informer 触发重建 table，下次握手即用新证书，**无重启**。
- HSTS / TLS 版本 / 加密套件可通过 ConfigMap 全局配置。

### 7.3 健康检查

- **被动健康检查**（首版实现）：`GoverningRoundTripper` 累计后端 5xx/超时，上报 `K8sDiscovery`，对 Instance 降权（权重衰减）；连续失败 N 次暂时摘除；半开探测恢复。
- **主动健康检查**（路线图）：可选周期 HTTP 探测，主动剔除不健康 Endpoints（补齐 K8s readiness 滞后场景）。

### 7.4 协议支持

| 协议 | 状态 | 实现 |
|---|---|---|
| HTTP/HTTPS | ✅ 首版 | zeus proxy（httputil.ReverseProxy） |
| WebSocket | ✅ 首版 | zeus proxy（Hijack + io.Copy） |
| SSE | ✅ 首版 | zeus proxy（禁缓冲 + Flusher） |
| HTTP/2 | ✅ 首版 | stdlib http.Server 原生 |
| HTTP/3 (QUIC) | 🔬 路线图 | golang.org/x/net/http3，alt-svc 协商 |
| gRPC 路由 | 🔬 路线图 | 复用 zeus `plugins/proxy/grpc`，HTTP/2 多路复用 |
| TCP/UDP | 🔬 扩展点 | 不首版实现，留 CRD 接口 |

---

## 8. Ingress + Gateway API 双支持

### 8.1 策略

- **Ingress v1**：首版一等公民，兼容 ingress-nginx 主流 annotation（迁移友好）。
- **Gateway API**：通过 CRD 支持 `Gateway`/`HTTPRoute`/`TLSRoute`，`--enable-gateway-api` 开关。翻译为同一 `RoutingTable`。
- **统一翻译层**：`translator` 包有两个入口 `TranslateIngress` / `TranslateHTTPRoute`，输出同一 `RoutingTable`，避免两套数据面逻辑（DRY）。

### 8.2 Annotation 兼容矩阵（迁移成本最小化）

| ingress-nginx annotation | Hermes 等价（hermes.ingress.k8s.io/* 或兼容前缀） |
|---|---|
| `nginx.ingress.kubernetes.io/rewrite-target` | `hermes.ingress.k8s.io/rewrite-target` |
| `nginx.ingress.kubernetes.io/redirect-to` | `hermes.ingress.k8s.io/redirect-to` |
| `nginx.ingress.kubernetes.io/canary*` | `hermes.ingress.k8s.io/canary*`（语义一致） |
| `nginx.ingress.kubernetes.io/limit-*` | 由 Gateway API BackendTrafficPolicy 或 hermes CRD 表达 |
| `nginx.ingress.kubernetes.io/proxy-*` (超时/缓冲) | `hermes.ingress.k8s.io/timeout` 等 |

首版支持 `--annotation-prefix=nginx` 兼容模式，降低迁移摩擦。

---

## 9. 可观测性（复用 zeus）

| 维度 | 实现 | 标签 |
|---|---|---|
| Access Log | zeus `middleware/accesslog` → stdout/JSON | method/path/status/duration/cluster/upstream |
| Metrics | zeus `plugins/metrics/prometheus`，`/metrics` 端点 | `hermes_requests_total{host,path,method,status,cluster,upstream}` / `hermes_request_duration_seconds` / `hermes_upstream_5xx_total` |
| Trace | zeus `plugins/trace/otel`，span `ingress.route` | attrs: `host`,`path`,`upstream`,`cluster`,`canary` |
| Baggage 透传 | zeus `propagation` | `X-Zeus-Cluster` + W3C Baggage 自动透传到后端 |

后端若是 zeus 服务，自动 extract cluster/baggage，trace/metrics/log 自动带 cluster 维度——**全链路同一套治理语义**，这是 Hermes 区别于所有竞品的护城河。

---

## 10. 扩展点

1. **CRD `HTTPProxy`**（高级路由）：参考 Contour/Project Contour，提供 header 权重、流量拆分、跨命名空间委托等 Ingress 表达不了的能力。
2. **Plugin 机制**：数据面 `http.Handler` 中间件可插拔（Wasm 路线图），首版支持 Go 编译期插件（`middleware.Interceptor` 注册）。
3. **`GoverningRoundTripper` 策略源**：治理策略来自 annotation / CRD / ConfigMap，统一抽象为 `GovernanceConfigProvider`。

---

## 11. 目录结构

```
ingress/
├── cmd/hermes/main.go                # 入口：启动 controller + dataplane
├── pkg/
│   ├── controller/                   # 控制面
│   │   ├── controller.go             # controller-runtime Reconciler
│   │   ├── informers.go              # Ingress/Service/Endpoints/Secret informer 注册
│   │   └── status.go                 # Ingress status 更新
│   ├── translator/                   # K8s → 内部模型
│   │   ├── ingress.go                # Ingress v1 → RoutingTable
│   │   ├── gatewayapi.go             # HTTPRoute → RoutingTable
│   │   └── annotations.go            # annotation 解析（canary/rewrite/...）
│   ├── model/                        # 内部路由模型
│   │   ├── table.go                  # RoutingTable + 匹配算法
│   │   └── types.go
│   ├── dataplane/                    # 数据面
│   │   ├── server.go                 # http.Server + TLS + 生命周期
│   │   └── selector.go               # IngressSelector (zeus proxy.Selector)
│   ├── discovery/                    # K8s → zeus Discovery 适配
│   │   └── k8s.go                    # K8sDiscovery
│   ├── governance/                   # 治理集成
│   │   ├── roundtripper.go           # GoverningRoundTripper
│   │   └── healthcheck.go            # 被动健康检查
│   └── config/                       # 控制器配置 + ConfigMap
├── deploy/
│   ├── crd/                          # HTTPProxy CRD（路线图）
│   ├── rbac.yaml
│   ├── deployment.yaml
│   └── helm/                         # Helm chart
├── examples/                         # 示例 Ingress / canary / Gateway API
├── docs/
│   ├── DESIGN.md                     # 本文档
│   ├── annotations.md                # annotation 参考
│   └── migration-from-nginx.md
├── go.mod                            # 依赖 zeus + controller-runtime + k8s.io/*
└── README.md
```

---

## 12. 实施路线图

### 阶段 0：脚手架（1 周）
- go.mod、目录结构、cmd/hermes/main.go 骨架
- controller-runtime Manager 启动 + leader election
- zeus proxy + IngressSelector 端到端跑通一个硬编码路由

### 阶段 1：Ingress v1 MVP（2 周）✅ 可用基线
- Ingress + Service + Endpoints informer
- translator：Ingress → RoutingTable（host/path 匹配）
- K8sDiscovery：Endpoints → zeus Discovery
- IngressSelector + zeus proxy（HTTP/WS/SSE）
- TLS 卸载（Secret + SNI）
- 基础 middleware 链（requestid/accesslog/recovery）
- Ingress status 更新

### 阶段 2：治理内建（1 周）✅ 差异化卖点
- GoverningRoundTripper：限流/熔断/重试/超时
- 被动健康检查
- annotation 驱动的治理策略
- cluster 维度 metrics 标签

### 阶段 3：灰度/金丝雀（1 周）
- canary annotation（weight/header/cookie）
- canary → zeus cluster 映射 + 端到端传播
- 兼容 `nginx.ingress.kubernetes.io/canary*` 前缀

### 阶段 4：Gateway API（1.5 周）
- CRD 安装 + HTTPRoute/Gateway 翻译
- `--enable-gateway-api` 开关
- conformance 基础用例通过

### 阶段 5：可观测性 + 生产化（1 周）
- otel trace + prometheus metrics 端到端
- Helm chart + RBAC + 多副本部署
- 文档：annotation 参考、迁移指南、性能调优

### 阶段 6：进阶（路线图）
- HTTPProxy CRD（高级路由）
- 主动健康检查
- HTTP/3、gRPC 路由
- Wasm 插件
- 流量镜像、A/B 测试

---

## 13. 风险与对策

| 风险 | 对策 |
|---|---|
| zeus proxy 性能不如 nginx/Envoy | 阶段 1 后立即做基准测试（wrk/vegeta），目标单核万级 RPS；不足则优化热路径（零拷贝 header、sync.Pool）或引入 fasthttp 路径（可选） |
| controller-runtime + zeus 两套生命周期整合 | 用 zeus `components.NewApp` 托管数据面组件，controller-runtime Manager 托管控制面，main.go 编排两者启停顺序 |
| Gateway API 实现质量（业界踩坑多） | 阶段 4 严格跑 gateway-api conformance suite，参考 howardjohn/gateway-api-bench 已知坑 |
| annotation 兼容的语义偏差 | 兼容前缀模式下，明确文档标注"行为对齐但不保证 100% 一致"，提供 dry-run 校验 |
| Endpoints 大规模集群性能 | EndpointSlice 优先（K8s 1.21+），informer cache + 原子 swap，分片处理 |

---

## 14. 与 Zeus 协同的演进建议

Hermes 作为 zeus 生态的"流量入口"组件，反哺 zeus 的方向：

1. **zeus proxy 增强**：Hermes 的 `IngressSelector` 通用化后可反哺为 zeus `proxy/hostmux` 子包（host/path 路由能力 zeus 当前缺失）。
2. **治理集成上游化**：`GoverningRoundTripper` 模式可抽象为 zeus `proxy.WithGovernance` Option，让 zeus proxy 原生支持治理（目前缺失）。
3. **健康检查上游化**：被动健康检查可抽象为 zeus `balancer` 的 `HealthAware` 扩展。
4. **K8sDiscovery 上游化**：可作为 zeus `plugins/registry/k8s`（Endpoints 作为注册中心），补齐 zeus 的 K8s 原生服务发现。

**结论**：Hermes 与 zeus 是共生关系——Hermes 复用 zeus 数据面快速成型，同时把 ingress 场景下沉淀的通用能力（host/path 路由、治理集成、K8s 发现、健康检查）反哺 zeus 核心，形成"框架 + 入口"的完整生态。
