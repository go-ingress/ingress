# 快速开始

5 分钟上手 Hermes。

## 1. demo 模式（无需 K8s 集群）

```bash
go run ./cmd/hermes --demo
```

另开终端验证：

```bash
curl -H 'Host: foo.local' http://localhost:8080/
# hello from echo backend
# path=/
# host=foo.local
```

## 2. K8s 部署

```bash
# 一体化清单
kubectl apply -f deploy/manifests.yaml

# 或 Helm（推荐生产）
helm install hermes deploy/helm -n hermes-system --create-namespace \
  --set controller.statusHostname=hermes.example.com

# 验证
kubectl get pods -n hermes-system
kubectl get ingressclass hermes
```

## 3. 部署示例应用 + Ingress

```bash
kubectl apply -f examples/basic.yaml
# 获取 LB 地址
kubectl get svc hermes -n hermes-system
curl -H 'Host: echo.example.com' http://<LB-IP>/
```

## 4. 金丝雀发布

```bash
kubectl apply -f examples/canary.yaml
# 30% 流量路由到 canary
for i in $(seq 1 20); do curl -s -H 'Host: echo.example.com' http://<LB-IP>/; done | sort | uniq -c
```

## 5. per-service 限流

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: echo
  annotations:
    hermes.ingress.kubernetes.io/limit-rps: "100"   # 按 service 独立限流 100 rps
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

## 6. 可观测

```bash
# Prometheus metrics（含 hermes_requests_total / hermes_request_duration_seconds）
kubectl port-forward -n hermes-system svc/hermes 8081:8081
curl http://localhost:8081/metrics | grep hermes_
```

## 7. 多副本高可用

```bash
helm install hermes deploy/helm -n hermes-system --create-namespace \
  --set replicaCount=2 \
  --set controller.leaderElect=true \
  --set podDisruptionBudget.enabled=true
```

## 下一步

- [Annotations 参考](./annotations.md)
- [nginx 迁移指南](./migration-from-nginx.md)
- [设计文档](./DESIGN.md)
- [示例](../examples)
