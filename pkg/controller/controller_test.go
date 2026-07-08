package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-ingress/ingress/pkg/apis/hermes/v1alpha1"
	"github.com/go-ingress/ingress/pkg/discovery"
	"github.com/go-ingress/ingress/pkg/model"
)

// mockSink 收集 Reconciler 推送的路由表（实现 TableSink）。
type mockSink struct{ table *model.RoutingTable }

func (m *mockSink) Update(t *model.RoutingTable) { m.table = t }

func ptrPathType(p networkingv1.PathType) *networkingv1.PathType { return &p }

func newTestReconciler(t *testing.T, objs ...client.Object) (*Reconciler, *mockSink, *discovery.K8sDiscovery) {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	_ = v1alpha1.AddToScheme(s) // HTTPProxy CRD 类型（fake client 需识别）
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	sink := &mockSink{}
	disc := discovery.NewK8sDiscovery()
	return &Reconciler{
		Client:       cl,
		Scheme:       s,
		Sink:         sink,
		Discovery:    disc,
		IngressClass: "hermes",
	}, sink, disc
}

func TestReconcile_BasicIngress(t *testing.T) {
	ingClass := "hermes"
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingClass,
			Rules: []networkingv1.IngressRule{{
				Host: "echo.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: "echo", Port: networkingv1.ServiceBackendPort{Number: 80},
						}},
					}},
				}},
			}},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
	}
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
			Ports:     []corev1.EndpointPort{{Port: 8080}},
		}},
	}

	r, sink, disc := newTestReconciler(t, ing, svc, ep)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "global"}}); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	if sink.table == nil {
		t.Fatal("table not pushed")
	}
	rule, ok := sink.table.Match("echo.example.com", "/", "GET", nil)
	if !ok || rule.Backend.ServiceName != "default/echo" {
		t.Fatalf("route not built correctly: %+v ok=%v", rule, ok)
	}
	// Discovery 应被 Endpoints 填充。
	entry, _ := disc.GetService(context.Background(), "default/echo")
	if entry == nil || len(entry.Instances) != 1 {
		t.Fatalf("discovery not populated: %+v", entry)
	}
	for _, ins := range entry.Instances {
		if ins.IP != "10.0.0.1" {
			t.Fatalf("unexpected instance ip: %s", ins.IP)
		}
	}
}

func TestReconcile_IngressClassFilter(t *testing.T) {
	hermes := "hermes"
	nginx := "nginx"
	ingHermes := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "h"},
		Spec: networkingv1.IngressSpec{IngressClassName: &hermes, Rules: []networkingv1.IngressRule{{
			Host: "h.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "h"}}}},
			}},
		}}},
	}
	ingNginx := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "n"},
		Spec: networkingv1.IngressSpec{IngressClassName: &nginx, Rules: []networkingv1.IngressRule{{
			Host: "n.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "n"}}}},
			}},
		}}},
	}

	r, sink, _ := newTestReconciler(t, ingHermes, ingNginx)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	if _, ok := sink.table.Match("h.example.com", "/", "GET", nil); !ok {
		t.Fatal("hermes-class ingress should be included")
	}
	if _, ok := sink.table.Match("n.example.com", "/", "GET", nil); ok {
		t.Fatal("nginx-class ingress should be filtered out")
	}
}

func TestReconcile_CanaryMerge(t *testing.T) {
	hermes := "hermes"
	main := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "main"},
		Spec: networkingv1.IngressSpec{IngressClassName: &hermes, Rules: []networkingv1.IngressRule{{
			Host: "echo.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "echo"}}}},
			}},
		}}},
	}
	canary := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "canary",
			Annotations: map[string]string{"hermes.ingress.kubernetes.io/canary": "true",
				"hermes.ingress.kubernetes.io/canary-weight": "30"}},
		Spec: networkingv1.IngressSpec{IngressClassName: &hermes, Rules: []networkingv1.IngressRule{{
			Host: "echo.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "echo-canary"}}}},
			}},
		}}},
	}

	r, sink, _ := newTestReconciler(t, main, canary)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	rule, _ := sink.table.Match("echo.example.com", "/", "GET", nil)
	if rule.Canary == nil || rule.Canary.Weight != 30 {
		t.Fatalf("canary not merged in Reconcile: %+v", rule.Canary)
	}
}

func TestReconcile_HTTPProxy(t *testing.T) {
	px := &v1alpha1.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: v1alpha1.HTTPProxySpec{
			Host: "proxy.example.com",
			Routes: []v1alpha1.HTTPProxyRoute{{
				Conditions: []v1alpha1.HTTPProxyCondition{{Path: "/", Prefix: true}},
				Services:   []v1alpha1.HTTPProxyService{{Name: "echo", Port: 80}},
			}},
		},
	}
	r, sink, _ := newTestReconciler(t, px)
	r.EnableHTTPProxy = true
	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("Reconcile error: %v", err)
	}
	rule, ok := sink.table.Match("proxy.example.com", "/", "GET", nil)
	if !ok || rule.Backend.ServiceName != "default/echo" {
		t.Fatalf("httpproxy route not built: %+v ok=%v", rule, ok)
	}
}
