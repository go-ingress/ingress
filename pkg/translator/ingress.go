// Package translator 把 K8s Ingress / Gateway API 资源翻译为内部 RoutingTable。
//
// 纯函数设计，无 K8s 客户端依赖，便于单测。controller 调用 BuildTable 后通过 TableSink 推送数据面。
package translator

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-ingress/ingress/pkg/model"
)

// ServiceKey 服务唯一键 "namespace/name"，与 BackendRef.ServiceName、discovery 缓存键一致。
type ServiceKey string

// ServiceKeyOf 构造服务键。
func ServiceKeyOf(namespace, name string) ServiceKey { return ServiceKey(namespace + "/" + name) }

// String 转字符串。
func (k ServiceKey) String() string { return string(k) }

// BuildTable 从 Ingress 列表构建路由表。
//
// 两遍扫描：
//  1. 主 Ingress（非 canary）→ rules + default backend
//  2. canary Ingress → 合并为同 host+path 主 rule 的 CanaryConfig
//
// services 用于把 IngressBackend.Service.Port.Name（命名端口）解析为端口号。
func BuildTable(ingresses []*networkingv1.Ingress, services map[ServiceKey]*corev1.Service) *model.RoutingTable {
	t := &model.RoutingTable{Hosts: make(map[string]*model.HostRules)}
	for _, ing := range ingresses {
		if ing == nil || isCanary(ing.Annotations) {
			continue
		}
		addDefaultBackend(t, ing, services)
		addRules(t, ing, services)
	}
	for _, ing := range ingresses {
		if ing == nil || !isCanary(ing.Annotations) {
			continue
		}
		mergeCanary(t, ing, services)
	}
	collectWildcards(t)
	return t
}

func addDefaultBackend(t *model.RoutingTable, ing *networkingv1.Ingress, services map[ServiceKey]*corev1.Service) {
	if ing.Spec.DefaultBackend == nil {
		return
	}
	if b := backendRef(ing.Namespace, ing.Spec.DefaultBackend, ing.Annotations, services); b != nil {
		t.Default = b
	}
}

func addRules(t *model.RoutingTable, ing *networkingv1.Ingress, services map[ServiceKey]*corev1.Service) {
	for i := range ing.Spec.Rules {
		rule := &ing.Spec.Rules[i]
		if rule.HTTP == nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(rule.Host))
		rules := t.Hosts[host]
		if rules == nil {
			rules = &model.HostRules{Host: host}
			t.Hosts[host] = rules
		}
		for j := range rule.HTTP.Paths {
			if pr := pathRule(ing, &rule.HTTP.Paths[j], services); pr != nil {
				rules.Paths = append(rules.Paths, pr)
			}
		}
	}
}

// mergeCanary 把 canary Ingress 的 backend 合并为同 host+path 主 rule 的 CanaryConfig。
func mergeCanary(t *model.RoutingTable, ing *networkingv1.Ingress, services map[ServiceKey]*corev1.Service) {
	for i := range ing.Spec.Rules {
		rule := &ing.Spec.Rules[i]
		if rule.HTTP == nil {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(rule.Host))
		rules := t.Hosts[host]
		if rules == nil {
			continue // 无主 rule，canary 无意义
		}
		for j := range rule.HTTP.Paths {
			p := &rule.HTTP.Paths[j]
			canaryBackend := backendRef(ing.Namespace, &p.Backend, ing.Annotations, services)
			if canaryBackend == nil {
				continue
			}
			mergeCanaryIntoRule(rules.Paths, p, parseCanary(ing.Annotations, canaryBackend))
		}
	}
}

// mergeCanaryIntoRule 把 canary 配置合并到 path + pathType 匹配的主 rule。
func mergeCanaryIntoRule(paths []*model.PathRule, canaryPath *networkingv1.HTTPIngressPath, canary *model.CanaryConfig) {
	pt := pathType(canaryPath.PathType)
	for _, pr := range paths {
		if pr.Path == canaryPath.Path && pr.PathType == pt {
			pr.Canary = canary
			return
		}
	}
}

func collectWildcards(t *model.RoutingTable) {
	for host, rules := range t.Hosts {
		if strings.HasPrefix(host, "*.") {
			t.Wildcards = append(t.Wildcards, rules)
		}
	}
	model.SortWildcards(t.Wildcards)
}

func pathRule(ing *networkingv1.Ingress, p *networkingv1.HTTPIngressPath, services map[ServiceKey]*corev1.Service) *model.PathRule {
	backend := backendRef(ing.Namespace, &p.Backend, ing.Annotations, services)
	if backend == nil {
		return nil
	}
	return &model.PathRule{
		PathType: pathType(p.PathType),
		Path:     p.Path,
		Backend:  backend,
		Rewrite:  parseRewrite(ing.Annotations),
	}
}

// backendRef 解析 IngressBackend 为 BackendRef。支持端口号与命名端口（命名端口查 services）。
// anns 用于解析 per-service 治理 annotation（如 limit-rps）。
func backendRef(namespace string, b *networkingv1.IngressBackend, anns map[string]string, services map[ServiceKey]*corev1.Service) *model.BackendRef {
	if b == nil || b.Service == nil {
		return nil
	}
	ref := &model.BackendRef{ServiceName: namespace + "/" + b.Service.Name}
	if b.Service.Port.Number > 0 {
		ref.Port = int(b.Service.Port.Number)
	} else if b.Service.Port.Name != "" {
		if svc := services[ServiceKeyOf(namespace, b.Service.Name)]; svc != nil {
			ref.Port = resolveServicePort(svc, b.Service.Port.Name)
		}
	}
	ref.LimitRPS = parseLimitRPS(anns)
	return ref
}

func resolveServicePort(svc *corev1.Service, name string) int {
	for _, p := range svc.Spec.Ports {
		if p.Name == name {
			return int(p.Port)
		}
	}
	return 0
}

func pathType(pt *networkingv1.PathType) model.PathType {
	if pt == nil {
		return model.PathTypeImplementationSpecific
	}
	switch *pt {
	case networkingv1.PathTypeExact:
		return model.PathTypeExact
	case networkingv1.PathTypePrefix:
		return model.PathTypePrefix
	default:
		return model.PathTypeImplementationSpecific
	}
}
