package translator

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-ingress/ingress/pkg/model"
)

// annotation 前缀：Hermes 原生 + nginx-ingress 兼容（降低迁移成本）。
const (
	AnnotationPrefix = "hermes.ingress.kubernetes.io"
	NginxPrefix      = "nginx.ingress.kubernetes.io"
)

// annotation 名（与前缀拼接使用）。
const (
	annRewriteTarget     = "rewrite-target"
	annCanary            = "canary"
	annCanaryWeight      = "canary-weight"
	annCanaryHeader      = "canary-by-header"
	annCanaryHeaderValue = "canary-by-header-value"
	annCanaryCookie      = "canary-by-cookie"
	annLimitRPS          = "limit-rps" // per-service 限流（每秒请求数）
	annActiveHealthPath  = "active-health-check-path" // Service 注解：主动健康检查探测路径（opt-in）
)

// annotation 按 hermes 前缀取，缺失则回退 nginx 前缀。
func annotation(anns map[string]string, name string) string {
	if anns == nil {
		return ""
	}
	if v, ok := anns[AnnotationPrefix+"/"+name]; ok {
		return v
	}
	if v, ok := anns[NginxPrefix+"/"+name]; ok {
		return v
	}
	return ""
}

// isCanary 判断 Ingress 是否标记为 canary（canary=true）。
func isCanary(anns map[string]string) bool {
	return annotation(anns, annCanary) == "true"
}

// parseRewrite 解析 rewrite-target annotation。
func parseRewrite(anns map[string]string) *model.RewriteConfig {
	target := annotation(anns, annRewriteTarget)
	if target == "" {
		return nil
	}
	return &model.RewriteConfig{ReplacePrefix: target}
}

// parseCanary 从 canary Ingress 的 annotation 解析 CanaryConfig。
// 决策优先级（对齐 ingress-nginx）：header > cookie > weight。
func parseCanary(anns map[string]string, backend *model.BackendRef) *model.CanaryConfig {
	if !isCanary(anns) || backend == nil {
		return nil
	}
	c := &model.CanaryConfig{Backend: backend}
	c.Header = annotation(anns, annCanaryHeader)
	c.Value = annotation(anns, annCanaryHeaderValue)
	c.Cookie = annotation(anns, annCanaryCookie)
	if w := annotation(anns, annCanaryWeight); w != "" {
		if n, err := strconv.Atoi(w); err == nil && n >= 0 {
			c.Weight = n
		}
	}
	return c
}

// ActiveHealthCheckPath 从 Service 注解解析主动健康检查探测路径。
//
// 仅当 Service 显式声明 hermes.ingress.kubernetes.io/active-health-check-path 时，
// 该 service 才会被主动健康检查探测；未声明返回空串（不探测，仅依赖被动健康检查）。
// 这是 opt-in 设计：避免对未实现 /health 的后端（如静态站点）误判剔除。
func ActiveHealthCheckPath(svc *corev1.Service) string {
	if svc == nil || svc.Annotations == nil {
		return ""
	}
	return svc.Annotations[AnnotationPrefix+"/"+annActiveHealthPath]
}

// parseLimitRPS 解析 limit-rps annotation（0=未配置，用全局默认）。
func parseLimitRPS(anns map[string]string) int {
	v := annotation(anns, annLimitRPS)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
