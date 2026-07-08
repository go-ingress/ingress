# gRPC 路由（实验性）

Hermes 数据面在明文模式启用 **h2c**（HTTP/2 cleartext），支持 gRPC 客户端 h2c 直连路由。

## 工作原理

- 明文监听（`--addr=:8080`，无 TLS）时，数据面用 `h2c.NewHandler` 包装，同时支持 HTTP/1.1 与 HTTP/2 cleartext。
- gRPC 请求（HTTP/2 POST，`content-type: application/grpc`）按 `:authority`（host）+ path 路由，经 `httputil.ReverseProxy` 透传到后端。
- TLS 模式（`CertPool` 启用）由 ALPN 自动协商 HTTP/2，gRPC over TLS 同样可用。

## 示例

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: grpc-svc
spec:
  ingressClassName: hermes
  rules:
    - host: grpc.example.com
      http:
        paths:
          - path: /helloworld.Greeter   # gRPC service 全名（package.Service）
            pathType: Prefix
            backend: { service: { name: grpc-svc, port: { number: 50051 } } }
```

gRPC 客户端（h2c）：

```bash
grpcurl -plaintext -authority grpc.example.com \
  -d '{"name":"hermes"}' \
  <hermes-ip>:8080 helloworld.Greeter/SayHello
```

## 限制（实验性）

- gRPC trailer 透传依赖 `httputil.ReverseProxy`（Go 1.26+ 改进了 trailer 支持），复杂流式场景可能有边缘问题。
- 不解析 gRPC 协议帧，按 HTTP/2 host/path 路由（足够覆盖 service.method 级路由）。
- 治理（熔断/限流/重试）按 HTTP 语义生效：5xx 触发熔断/重试，gRPC 状态码映射到 HTTP 状态码（如 `Unavailable` → 503）。
- 健康检查探测 `/health`（HTTP），gRPC 后端需额外提供 HTTP 健康端点，或后续支持 gRPC health protocol。

## 后续

- gRPC health protocol 探测（主动健康检查）
- gRPC 状态码 → HTTP 状态码显式映射
- HTTP/3（QUIC）支持
