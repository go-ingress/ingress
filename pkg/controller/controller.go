// Package controller 实现 Hermes 控制面。
//
// 基于 controller-runtime：Informer 监听 Ingress/Service/Endpoints/Secret
//（+ HTTPRoute 若启用 Gateway API / HTTPProxy 若启用高级路由），
// 任意资源变更触发全局 Reconcile —— List 全量资源 → translator 翻译为 RoutingTable →
// atomic 推送数据面；Endpoints → K8sDiscovery；Secret → CertPool；更新 Ingress status。
//
// 设计：控制面通过 TableSink / CertUpdater 接口与数据面解耦（鸭子类型），
// controller 不 import dataplane，避免循环依赖。
package controller

import (
	"context"
	"crypto/tls"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-ingress/ingress/pkg/apis/hermes/v1alpha1"
	"github.com/go-ingress/ingress/pkg/discovery"
	"github.com/go-ingress/ingress/pkg/model"
	"github.com/go-ingress/ingress/pkg/translator"
)

// TableSink 路由表推送接口（dataplane.IngressSelector.Update 满足）。
type TableSink interface {
	Update(*model.RoutingTable)
}

// CertUpdater 证书池更新接口（dataplane.CertPool 满足）。
type CertUpdater interface {
	SetCert(host string, cert *tls.Certificate)
	DeleteCert(host string)
	SetDefault(cert *tls.Certificate)
}

// TLSLoader TLS Secret 加载接口。
type TLSLoader interface {
	Load(ctx context.Context, namespace, name string) (*tls.Certificate, error)
}

// Reconciler Hermes 控制面 Reconciler。
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Sink      TableSink              // 数据面路由表推送
	Discovery *discovery.K8sDiscovery // 数据面服务发现
	CertUpd   CertUpdater             // 数据面证书池
	TLSLoad   TLSLoader               // Secret 加载

	IngressClass   string // 过滤 spec.ingressClassName，空=不过滤
	WatchNamespace string // "" = 全命名空间
	StatusHostname string // Ingress status.LoadBalancer hostname
	StatusAddress  string // Ingress status.LoadBalancer IP

	EnableGatewayAPI bool // 启用 Gateway API（HTTPRoute）翻译
	EnableHTTPProxy  bool // 启用 Hermes HTTPProxy CRD 翻译
}

// Reconcile 全量重建路由表 + 服务发现 + TLS + status。
// 任意被监听资源变更触发；req.NamespacedName 被忽略（全局重建模式，对齐 ingress-nginx）。
func (r *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. List Ingresses 并按 ingressClass 过滤。
	ingList := &networkingv1.IngressList{}
	if err := r.List(ctx, ingList, namespaceOptions(r.WatchNamespace)...); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ingresses := filterIngressClass(ingList.Items, r.IngressClass)

	// 2. List 被 Ingress 引用的 Services（解析命名端口）。
	services, err := r.listReferencedServices(ctx, ingresses)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 3. 翻译路由表（Ingress + 可选 HTTPRoute/HTTPProxy）并推送。
	table := translator.BuildTable(ingresses, services)
	if r.EnableGatewayAPI {
		if htTable := r.buildHTTPRouteTable(ctx); htTable != nil {
			mergeTable(table, htTable)
		}
	}
	if r.EnableHTTPProxy {
		if hpTable := r.buildHTTPProxyTable(ctx); hpTable != nil {
			mergeTable(table, hpTable)
		}
	}
	r.Sink.Update(table)
	logger.V(1).Info("routing table updated", "ingresses", len(ingresses), "hosts", len(table.Hosts),
		"gateway-api", r.EnableGatewayAPI, "httpproxy", r.EnableHTTPProxy)

	// 4. Endpoints → K8sDiscovery（全量刷新）。
	if err := r.refreshEndpoints(ctx); err != nil {
		logger.Error(err, "refresh endpoints")
	}

	// 4b. Service 注解 → 主动健康检查路径（opt-in，仅声明的 service 才被主动探测）。
	r.refreshHealthPaths(services)

	// 5. TLS Secret → CertPool。
	if err := r.refreshTLS(ctx, ingresses); err != nil {
		logger.Error(err, "refresh TLS")
	}

	// 6. Ingress status。
	if err := r.updateStatus(ctx, ingresses); err != nil {
		logger.Error(err, "update ingress status")
	}

	return ctrl.Result{}, nil
}

// buildHTTPRouteTable List HTTPRoute 并翻译。CRD 未安装或无权限时返回 nil（安全降级）。
func (r *Reconciler) buildHTTPRouteTable(ctx context.Context) *model.RoutingTable {
	routes := &gatewayv1.HTTPRouteList{}
	if err := r.List(ctx, routes, namespaceOptions(r.WatchNamespace)...); err != nil {
		return nil
	}
	refs := make([]*gatewayv1.HTTPRoute, 0, len(routes.Items))
	for i := range routes.Items {
		refs = append(refs, &routes.Items[i])
	}
	return translator.BuildTableFromHTTPRoute(refs, nil)
}

// buildHTTPProxyTable List HTTPProxy 并翻译。CRD 未安装或无权限时返回 nil（安全降级）。
func (r *Reconciler) buildHTTPProxyTable(ctx context.Context) *model.RoutingTable {
	proxies := &v1alpha1.HTTPProxyList{}
	if err := r.List(ctx, proxies, namespaceOptions(r.WatchNamespace)...); err != nil {
		return nil
	}
	refs := make([]*v1alpha1.HTTPProxy, 0, len(proxies.Items))
	for i := range proxies.Items {
		refs = append(refs, &proxies.Items[i])
	}
	return translator.BuildTableFromHTTPProxy(refs, nil)
}

// mergeTable 把 src 合并到 dst（同 host 的 paths 追加，wildcard 追加并重排）。
func mergeTable(dst, src *model.RoutingTable) {
	for host, rules := range src.Hosts {
		if existing, ok := dst.Hosts[host]; ok {
			existing.Paths = append(existing.Paths, rules.Paths...)
		} else {
			dst.Hosts[host] = rules
		}
	}
	if dst.Default == nil {
		dst.Default = src.Default
	}
	if len(src.Wildcards) > 0 {
		dst.Wildcards = append(dst.Wildcards, src.Wildcards...)
		model.SortWildcards(dst.Wildcards)
	}
}

// filterIngressClass 按 spec.ingressClassName 过滤（class 空则全保留）。
func filterIngressClass(ings []networkingv1.Ingress, class string) []*networkingv1.Ingress {
	var out []*networkingv1.Ingress
	for i := range ings {
		ing := &ings[i]
		if class == "" || (ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName == class) {
			out = append(out, ing)
		}
	}
	return out
}

// namespaceOptions 构造 List 命名空间选项（空=全命名空间，返回空切片）。
// 返回切片而非单个 Option，避免向 variadic ...ListOption 传 nil interface 触发 fakeClient panic。
func namespaceOptions(ns string) []client.ListOption {
	if ns == "" {
		return nil
	}
	return []client.ListOption{client.InNamespace(ns)}
}

// listReferencedServices 批量 Get 被 Ingress 引用的 Service。
func (r *Reconciler) listReferencedServices(ctx context.Context, ings []*networkingv1.Ingress) (map[translator.ServiceKey]*corev1.Service, error) {
	services := make(map[translator.ServiceKey]*corev1.Service)
	for _, ing := range ings {
		for _, ref := range referencedServices(ing) {
			key := translator.ServiceKeyOf(ref.Namespace, ref.Name)
			if _, ok := services[key]; ok {
				continue
			}
			svc := &corev1.Service{}
			if err := r.Get(ctx, ref, svc); err != nil {
				continue // Service 不存在则跳过（路由会指向无实例的 service → 503）
			}
			services[key] = svc
		}
	}
	return services, nil
}

// referencedServices 收集 Ingress 引用的所有 Service（default backend + rules）。
func referencedServices(ing *networkingv1.Ingress) []types.NamespacedName {
	var refs []types.NamespacedName
	add := func(svc *networkingv1.IngressServiceBackend) {
		if svc != nil {
			refs = append(refs, types.NamespacedName{Namespace: ing.Namespace, Name: svc.Name})
		}
	}
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
		add(ing.Spec.DefaultBackend.Service)
	}
	for i := range ing.Spec.Rules {
		rule := &ing.Spec.Rules[i]
		if rule.HTTP == nil {
			continue
		}
		for j := range rule.HTTP.Paths {
			if rule.HTTP.Paths[j].Backend.Service != nil {
				add(rule.HTTP.Paths[j].Backend.Service)
			}
		}
	}
	return refs
}

// refreshEndpoints 全量刷新 Endpoints 到 K8sDiscovery。
func (r *Reconciler) refreshEndpoints(ctx context.Context) error {
	epList := &corev1.EndpointsList{}
	if err := r.List(ctx, epList, namespaceOptions(r.WatchNamespace)...); err != nil {
		return client.IgnoreNotFound(err)
	}
	for i := range epList.Items {
		ep := &epList.Items[i]
		r.Discovery.SetService(ep.Namespace+"/"+ep.Name, EndpointsToInstances(ep))
	}
	return nil
}

// refreshHealthPaths 从 Service 注解刷新主动健康检查路径到 K8sDiscovery。
// 仅显式声明 active-health-check-path 的 service 会被主动探测；每轮全量重建避免残留。
func (r *Reconciler) refreshHealthPaths(services map[translator.ServiceKey]*corev1.Service) {
	if r.Discovery == nil {
		return
	}
	r.Discovery.ResetHealthPaths()
	for key, svc := range services {
		if path := translator.ActiveHealthCheckPath(svc); path != "" {
			r.Discovery.SetHealthPath(key.String(), path)
		}
	}
}

// refreshTLS 加载 Ingress TLS 引用的 Secret，更新 CertPool。
func (r *Reconciler) refreshTLS(ctx context.Context, ings []*networkingv1.Ingress) error {
	if r.CertUpd == nil || r.TLSLoad == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, ing := range ings {
		for _, tls := range ing.Spec.TLS {
			secretKey := ing.Namespace + "/" + tls.SecretName
			if _, ok := seen[secretKey]; ok {
				continue
			}
			seen[secretKey] = struct{}{}
			cert, err := r.TLSLoad.Load(ctx, ing.Namespace, tls.SecretName)
			if err != nil {
				continue // Secret 不存在或无效，跳过
			}
			for _, host := range tls.Hosts {
				r.CertUpd.SetCert(host, cert)
			}
			if len(tls.Hosts) == 0 {
				r.CertUpd.SetDefault(cert)
			}
		}
	}
	return nil
}

// updateStatus 更新 Ingress status.LoadBalancer（仅在变化时）。
func (r *Reconciler) updateStatus(ctx context.Context, ings []*networkingv1.Ingress) error {
	if r.StatusHostname == "" && r.StatusAddress == "" {
		return nil
	}
	var lbIngress []networkingv1.IngressLoadBalancerIngress
	if r.StatusHostname != "" {
		lbIngress = append(lbIngress, networkingv1.IngressLoadBalancerIngress{Hostname: r.StatusHostname})
	}
	if r.StatusAddress != "" {
		lbIngress = append(lbIngress, networkingv1.IngressLoadBalancerIngress{IP: r.StatusAddress})
	}
	newStatus := networkingv1.IngressStatus{
		LoadBalancer: networkingv1.IngressLoadBalancerStatus{Ingress: lbIngress},
	}
	for _, ing := range ings {
		if equality.Semantic.DeepEqual(ing.Status, newStatus) {
			continue
		}
		ing.Status = newStatus
		if err := r.Status().Update(ctx, ing); err != nil {
			return client.IgnoreNotFound(err)
		}
	}
	return nil
}
