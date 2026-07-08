# 从 ingress-nginx 迁移到 Hermes

Hermes 在 annotation 层面兼容 `nginx.ingress.kubernetes.io/*` 前缀，多数 ingress-nginx 配置可平滑迁移。

## 1. 安装 Hermes

```bash
kubectl apply -f deploy/manifests.yaml
# 或 Helm
helm install hermes deploy/helm -n hermes-system --create-namespace \
  --set controller.statusHostname=<你的LB域名>
```

## 2. 切换 IngressClass

将现有 Ingress 的 `spec.ingressClassName` 从 `nginx` 改为 `hermes`：

```yaml
spec:
  ingressClassName: hermes   # 原 nginx
```

或把 Hermes IngressClass 设为默认（`is-default-class: "true"`），无需改 Ingress。

## 3. Annotation 兼容矩阵

| nginx annotation | Hermes 支持 | 说明 |
|---|---|---|
| `nginx.ingress.kubernetes.io/rewrite-target` | ✅ 完全兼容 | 语义一致 |
| `nginx.ingress.kubernetes.io/canary` | ✅ 完全兼容 | |
| `nginx.ingress.kubernetes.io/canary-weight` | ✅ 完全兼容 | |
| `nginx.ingress.kubernetes.io/canary-by-header` | ✅ 完全兼容 | |
| `nginx.ingress.kubernetes.io/canary-by-header-value` | ✅ 完全兼容 | |
| `nginx.ingress.kubernetes.io/canary-by-cookie` | ✅ 完全兼容 | |
| `nginx.ingress.kubernetes.io/limit-*`（限流） | ⚠️ 默认全局策略 | per-service 限流 annotation 将在后续版本支持，当前默认 100 rps |
| `nginx.ingress.kubernetes.io/proxy-*`（超时/缓冲） | ⚠️ 部分支持 | 超时后续支持，缓冲策略不同（zeus proxy 用 stdlib） |
| `nginx.ingress.kubernetes.io/auth-*`（外部鉴权） | ❌ 暂不支持 | 路线图（middleware 扩展点） |
| `nginx.ingress.kubernetes.io/configuration-snippet`（Lua 注入） | ❌ 不支持 | Hermes 无 Lua；用 middleware 扩展点或 Wasm（路线图） |
| `nginx.ingress.kubernetes.io/ssl-*` | ⚠️ 部分 | TLS 通过标准 Ingress TLS + Secret 配置 |

## 4. 行为差异

| 维度 | ingress-nginx | Hermes |
|---|---|---|
| 数据面 | nginx + Lua（C） | zeus proxy（Go，单二进制） |
| 配置更新 | 部分变化触发 nginx reload | 全内存 atomic swap，零 reload |
| 治理 | Lua 实现，annotation 驱动 | zeus 治理原生内建，按 service+cluster 隔离 |
| 灰度 | canary annotation | canary annotation（兼容）+ zeus cluster 端到端传播 |
| WebSocket / SSE | ✅ | ✅ |
| 配置片段（Lua） | ✅ | ❌（设计上拒绝，换取可观测性与可移植性） |

## 5. 迁移检查清单

- [ ] Hermes IngressClass 已创建（`kubectl get ingressclass hermes`）
- [ ] Ingress `spec.ingressClassName` 指向 `hermes`
- [ ] TLS Secret 在 Ingress 同命名空间
- [ ] canary Ingress 的 `canary: "true"` annotation 保留
- [ ] 依赖 `configuration-snippet` / `auth-*` 的 Ingress 需先评估替代方案
- [ ] 监控 metrics 端点从 nginx exporter 切换到 `:8081/metrics`（Prometheus 格式，`hermes_*` 前缀）

## 6. 回滚

Hermes 与 ingress-nginx 可共存（不同 IngressClass）。回滚时把 Ingress 的 `ingressClassName` 改回 `nginx` 即可，无需卸载 Hermes。
