# Security Policy

## Supported Versions

Hermes 是一个年轻的开源项目，仅对最新次版本（latest minor）提供安全更新。

| Version | Supported          |
|---------|--------------------|
| 0.x     | ✅ 最新次版本       |
| < 0.x   | ❌ 不支持           |

## Reporting a Vulnerability

请**勿**通过公开 Issue 报告安全漏洞。

发现漏洞请通过以下方式私下披露：
- 发送邮件至：security@go-ingress.dev（占位，仓库维护者邮箱）
- 或使用 GitHub 私密漏洞报告（Security 选项卡 → Report a vulnerability）

**请在报告中包含：**
- 漏洞影响的版本与复现步骤
- 影响范围与潜在攻击场景
- 建议的修复方案（如有）

## Response SLA

| 阶段 | 目标时效 |
|------|----------|
| 确认收到报告 | 3 个工作日 |
| 初步评估与严重性分级 | 7 个工作日 |
| 修复版本发布 | 30 天（严重漏洞优先） |

修复发布后，报告者将在致谢名单中署名（除非要求匿名）。

## Scope

**在范围内：**
- Hermes 控制器本身的鉴权绕过、RCE、SSRF、TLS 校验缺失
- 路由匹配 / canary / 治理逻辑导致的安全边界破坏
- 控制面 RBAC 越权

**不在范围内：**
- 上游依赖（zeus / controller-runtime / k8s.io）的漏洞，请直接上报对应项目
- 部署在不受信任集群的配置错误
- DoS（资源耗尽）类问题需有明确攻击向量才会被接受

## Disclosure

我们采用**协同披露**：修复发布后，漏洞细节将在 CVE/ advisory 中公开，报告者享有署名权。
