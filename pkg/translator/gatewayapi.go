// Gateway API（HTTPRoute）翻译为 RoutingTable。
//
// 与 Ingress 翻译共用 model.RoutingTable，数据面无感知差异（DRY）。
// 控制面在 --enable-gateway-api 开启时 List HTTPRoute，merge 到主路由表。

package translator

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-ingress/ingress/pkg/model"
)

// BuildTableFromHTTPRoute 把 Gateway API HTTPRoute 翻译为 RoutingTable。
//
// 翻译规则：
//   - Spec.Hostnames → host
//   - Spec.Rules[].Matches[0].Path → PathRule.Path + PathType（仅取首个 match）
//   - Spec.Rules[].Matches[0].Method → PathRule.Method
//   - Spec.Rules[].Matches[0].Headers → PathRule.Headers
//   - Spec.Rules[].BackendRefs[0] → 主 Backend
//   - Spec.Rules[].BackendRefs[1] → CanaryConfig（weight-based 流量拆分，对齐 Gateway API traffic split）
//
// services 当前未使用（HTTPRoute backend 用 port number，不需查 Service 解析命名端口），保留以与 BuildTable 签名对称。
func BuildTableFromHTTPRoute(routes []*gatewayv1.HTTPRoute, _ map[ServiceKey]*corev1.Service) *model.RoutingTable {
	t := &model.RoutingTable{Hosts: make(map[string]*model.HostRules)}
	for _, rt := range routes {
		if rt == nil {
			continue
		}
		for _, host := range rt.Spec.Hostnames {
			h := strings.ToLower(string(host))
			rules := t.Hosts[h]
			if rules == nil {
				rules = &model.HostRules{Host: h}
				t.Hosts[h] = rules
			}
			for i := range rt.Spec.Rules {
				if pr := httpRouteRuleToPathRule(rt.Namespace, &rt.Spec.Rules[i]); pr != nil {
					rules.Paths = append(rules.Paths, pr)
				}
			}
		}
	}
	collectWildcards(t)
	return t
}

func httpRouteRuleToPathRule(defaultNS string, rule *gatewayv1.HTTPRouteRule) *model.PathRule {
	if rule == nil || len(rule.BackendRefs) == 0 {
		return nil
	}
	primary := backendRefFromHTTPBackend(defaultNS, &rule.BackendRefs[0])
	if primary == nil {
		return nil
	}
	pr := &model.PathRule{Path: "/", PathType: model.PathTypePrefix, Backend: primary}
	// 多 backend → traffic split：第二个作为 canary（weight）
	if len(rule.BackendRefs) > 1 {
		if canaryBackend := backendRefFromHTTPBackend(defaultNS, &rule.BackendRefs[1]); canaryBackend != nil {
			pr.Canary = &model.CanaryConfig{
				Backend: canaryBackend,
				Weight:  httpBackendWeight(&rule.BackendRefs[1]),
			}
		}
	}
	if len(rule.Matches) > 0 {
		applyHTTPRouteMatch(pr, &rule.Matches[0])
	}
	return pr
}

func applyHTTPRouteMatch(pr *model.PathRule, m *gatewayv1.HTTPRouteMatch) {
	if m == nil {
		return
	}
	if m.Path != nil && m.Path.Value != nil {
		pr.Path = *m.Path.Value
		pr.PathType = httpPathMatchType(m.Path.Type)
	}
	if m.Method != nil {
		pr.Method = string(*m.Method)
	}
	for i := range m.Headers {
		h := &m.Headers[i]
		pr.Headers = append(pr.Headers, model.HeaderMatch{
			Name:  string(h.Name),
			Value: string(h.Value),
			Type:  httpHeaderMatchType(h.Type),
		})
	}
}

func backendRefFromHTTPBackend(defaultNS string, ref *gatewayv1.HTTPBackendRef) *model.BackendRef {
	if ref == nil || ref.Name == "" {
		return nil
	}
	backendNS := defaultNS
	if ref.Namespace != nil && *ref.Namespace != "" {
		backendNS = string(*ref.Namespace)
	}
	b := &model.BackendRef{ServiceName: backendNS + "/" + string(ref.Name)}
	if ref.Port != nil {
		b.Port = int(*ref.Port)
	}
	b.Weight = httpBackendWeight(ref)
	return b
}

func httpBackendWeight(ref *gatewayv1.HTTPBackendRef) int {
	if ref == nil || ref.Weight == nil {
		return 1
	}
	return int(*ref.Weight)
}

func httpPathMatchType(t *gatewayv1.PathMatchType) model.PathType {
	if t == nil {
		return model.PathTypePrefix
	}
	switch *t {
	case gatewayv1.PathMatchExact:
		return model.PathTypeExact
	case gatewayv1.PathMatchPathPrefix:
		return model.PathTypePrefix
	default: // RegularExpression
		return model.PathTypeImplementationSpecific
	}
}

func httpHeaderMatchType(t *gatewayv1.HeaderMatchType) string {
	if t == nil {
		return "Exact"
	}
	return string(*t)
}
