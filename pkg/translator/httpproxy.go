package translator

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-ingress/ingress/pkg/apis/hermes/v1alpha1"
	"github.com/go-ingress/ingress/pkg/model"
)

// BuildTableFromHTTPProxy 把 Hermes HTTPProxy CRD 翻译为 RoutingTable。
//
// 翻译规则：
//   - Spec.Host → host
//   - Spec.Rules[].Conditions[].Path → PathRule.Path + PathType（Prefix/Exact）
//   - Spec.Rules[].Conditions[].Header → PathRule.Headers
//   - Spec.Rules[].Services[0] → 主 Backend
//   - Spec.Rules[].Services[1] → CanaryConfig（weight 流量拆分）
//   - Spec.Rules[].Rewrite → RewriteConfig
//
// services 当前未使用（HTTPProxy service 用 port number），保留以与 BuildTable 签名对称。
func BuildTableFromHTTPProxy(proxies []*v1alpha1.HTTPProxy, _ map[ServiceKey]*corev1.Service) *model.RoutingTable {
	t := &model.RoutingTable{Hosts: make(map[string]*model.HostRules)}
	for _, px := range proxies {
		if px == nil {
			continue
		}
		host := strings.ToLower(px.Spec.Host)
		rules := t.Hosts[host]
		if rules == nil {
			rules = &model.HostRules{Host: host}
			t.Hosts[host] = rules
		}
		for i := range px.Spec.Routes {
			if pr := httpProxyRouteToPathRule(px.Namespace, &px.Spec.Routes[i]); pr != nil {
				rules.Paths = append(rules.Paths, pr)
			}
		}
	}
	collectWildcards(t)
	return t
}

func httpProxyRouteToPathRule(ns string, r *v1alpha1.HTTPProxyRoute) *model.PathRule {
	if len(r.Services) == 0 {
		return nil
	}
	s0 := &r.Services[0]
	primary := &model.BackendRef{
		ServiceName: ns + "/" + s0.Name,
		Port:        s0.Port,
		Weight:      s0.Weight,
	}
	pr := &model.PathRule{Path: "/", PathType: model.PathTypePrefix, Backend: primary}
	// 多 service → 流量拆分：第二个作为 canary（weight）
	if len(r.Services) > 1 {
		s1 := &r.Services[1]
		pr.Canary = &model.CanaryConfig{
			Backend: &model.BackendRef{ServiceName: ns + "/" + s1.Name, Port: s1.Port, Weight: s1.Weight},
			Weight:  s1.Weight,
		}
	}
	if r.Rewrite != nil && r.Rewrite.Prefix != "" {
		pr.Rewrite = &model.RewriteConfig{ReplacePrefix: r.Rewrite.Prefix}
	}
	for i := range r.Conditions {
		c := &r.Conditions[i]
		if c.Path != "" {
			pr.Path = c.Path
			if c.Prefix {
				pr.PathType = model.PathTypePrefix
			} else {
				pr.PathType = model.PathTypeExact
			}
		}
		if c.Header != "" {
			pr.Headers = append(pr.Headers, model.HeaderMatch{Name: c.Header, Value: c.Value, Type: "Exact"})
		}
	}
	return pr
}
