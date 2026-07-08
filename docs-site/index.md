---
layout: home

hero:
  name: Hermes
  text: K8s Ingress 控制器
  tagline: 基于 Zeus 服务治理 —— 熔断/限流/重试/健康检查内建，约束明确，可直接投产
  image:
    src: /logo.svg
    alt: Hermes logo
  actions:
    - theme: brand
      text: 快速开始
      link: /quickstart
    - theme: alt
      text: 路由指南
      link: /routing

features:
  - title: Ingress v1 全功能
    details: host（精确 + 通配）/ path（Exact·Prefix·ImplementationSpecific）/ TLS 卸载 / 命名端口 / defaultBackend，K8s Prefix 边界语义对齐。
  - title: 内建服务治理
    details: 复用 Zeus 数据面，按 service+cluster 隔离的熔断、限流、重试与被动健康检查，无需额外 sidecar。
  - title: 灰度与流量切分
    details: canary（weight/header/cookie）跨 Ingress 合并；HTTPProxy CRD / Gateway API 支持流量拆分与 header 匹配。
  - title: 零重载热更新
    details: 控制面翻译路由表 → 原子指针交换 → 数据面即时生效，配置变更不丢连接、不重启。
  - title: 生产就绪
    details: leader election 多副本 HA、Prometheus 指标、PodDisruptionBudget、主动健康检查（opt-in）、Helm Chart。
  - title: 低迁移成本
    details: 兼容 nginx.ingress.kubernetes.io 注解前缀（canary / rewrite-target），附迁移检查清单与回滚步骤。
---
