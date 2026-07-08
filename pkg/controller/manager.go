package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-ingress/ingress/pkg/apis/hermes/v1alpha1"
	"github.com/go-ingress/ingress/pkg/discovery"
)

// 默认 leader election 配置。
const (
	defaultLeaderElectionID = "hermes.io.controller-leader-election"
)

// ManagerOptions 构建 controller-runtime Manager 的参数。
type ManagerOptions struct {
	Kubeconfig       string // kubeconfig 路径（空=自动 in-cluster 或 ~/.kube/config）
	WatchNamespace   string // "" = 全命名空间
	IngressClass     string // 过滤 spec.ingressClassName
	StatusHostname   string // Ingress status hostname
	StatusAddress    string // Ingress status IP
	MetricsAddr      string // controller-runtime metrics 端点（如 ":8081"）
	EnableGatewayAPI bool   // 启用 Gateway API（HTTPRoute）翻译
	EnableHTTPProxy  bool   // 启用 Hermes HTTPProxy CRD 翻译
	LeaderElection   bool   // 启用 leader election（多副本 HA 必备）
	LeaderElectionID string // leader election lease 名称（空用默认）
}

// NewManager 构建 controller-runtime Manager 并注册 Reconciler。
// 调用方 mgr.Start(ctx) 启动控制面。
func NewManager(opts ManagerOptions, sink TableSink, disc *discovery.K8sDiscovery, certUpd CertUpdater) (ctrl.Manager, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	// Gateway API 类型注册（CRD 未安装时无害，List 返回 NoKindMatch 被 Reconcile 忽略）。
	_ = gatewayv1.AddToScheme(scheme)
	// Hermes HTTPProxy CRD 类型注册。
	_ = v1alpha1.AddToScheme(scheme)

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{
			DefaultNamespaces: defaultNamespaces(opts.WatchNamespace),
		},
		Metrics: metricsserver.Options{BindAddress: opts.MetricsAddr},
	}
	if opts.LeaderElection {
		mgrOpts.LeaderElection = true
		mgrOpts.LeaderElectionID = defaultLeaderElectionID
		if opts.LeaderElectionID != "" {
			mgrOpts.LeaderElectionID = opts.LeaderElectionID
		}
		mgrOpts.LeaderElectionResourceLock = "leases"
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		return nil, err
	}

	r := &Reconciler{
		Client:           mgr.GetClient(),
		Scheme:           scheme,
		Sink:             sink,
		Discovery:        disc,
		CertUpd:          certUpd,
		TLSLoad:          NewSecretTLSLoader(mgr.GetClient()),
		IngressClass:     opts.IngressClass,
		WatchNamespace:   opts.WatchNamespace,
		StatusHostname:   opts.StatusHostname,
		StatusAddress:    opts.StatusAddress,
		EnableGatewayAPI: opts.EnableGatewayAPI,
		EnableHTTPProxy:  opts.EnableHTTPProxy,
	}

	// 任意被监听资源变更 → 映射到固定 key "global" → 触发全局 Reconcile。
	b := ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(globalReconcile)).
		Watches(&corev1.Endpoints{}, handler.EnqueueRequestsFromMapFunc(globalReconcile)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(globalReconcile))
	if opts.EnableGatewayAPI {
		b = b.Watches(&gatewayv1.HTTPRoute{}, handler.EnqueueRequestsFromMapFunc(globalReconcile))
	}
	if opts.EnableHTTPProxy {
		b = b.Watches(&v1alpha1.HTTPProxy{}, handler.EnqueueRequestsFromMapFunc(globalReconcile))
	}
	if err := b.Complete(r); err != nil {
		return nil, err
	}
	return mgr, nil
}

// globalReconcile 把任意对象变更映射到固定 key，触发全局重建。
func globalReconcile(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "global"}}}
}

// defaultNamespaces 构造 cache 命名空间范围（空=全命名空间）。
func defaultNamespaces(ns string) map[string]cache.Config {
	if ns == "" {
		return nil
	}
	return map[string]cache.Config{ns: {}}
}
