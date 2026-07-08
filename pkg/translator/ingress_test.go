package translator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-ingress/ingress/pkg/model"
)

func ptrPathType(t networkingv1.PathType) *networkingv1.PathType { return &t }

func ingBackend(svc string, port networkingv1.ServiceBackendPort) networkingv1.IngressBackend {
	return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: svc, Port: port}}
}

func TestBuildTable_BasicRule(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("echo", networkingv1.ServiceBackendPort{Number: 80}),
				}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{ing}, nil)
	rule, ok := tbl.Match("foo.local", "/", "GET", nil)
	if !ok {
		t.Fatal("expected match")
	}
	if rule.Backend.ServiceName != "default/echo" || rule.Backend.Port != 80 {
		t.Fatalf("unexpected backend: %+v", rule.Backend)
	}
	if rule.PathType != model.PathTypePrefix {
		t.Fatalf("unexpected path type: %s", rule.PathType)
	}
}

func TestBuildTable_DefaultBackend(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing"},
		Spec: networkingv1.IngressSpec{
			DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
				Name: "default-svc", Port: networkingv1.ServiceBackendPort{Number: 8080},
			}},
		},
	}
	tbl := BuildTable([]*networkingv1.Ingress{ing}, nil)
	if tbl.Default == nil || tbl.Default.ServiceName != "default/default-svc" || tbl.Default.Port != 8080 {
		t.Fatalf("unexpected default: %+v", tbl.Default)
	}
}

func TestBuildTable_RewriteAnnotation(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing",
			Annotations: map[string]string{AnnotationPrefix + "/rewrite-target": "/v2"}},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path: "/api", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("svc", networkingv1.ServiceBackendPort{Number: 80}),
				}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{ing}, nil)
	rule, _ := tbl.Match("foo.local", "/api", "GET", nil)
	if rule.Rewrite == nil || rule.Rewrite.ReplacePrefix != "/v2" {
		t.Fatalf("expected rewrite /v2, got %+v", rule.Rewrite)
	}
}

func TestBuildTable_RewriteAnnotation_NginxCompat(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing",
			Annotations: map[string]string{NginxPrefix + "/rewrite-target": "/v3"}},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("svc", networkingv1.ServiceBackendPort{Number: 80}),
				}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{ing}, nil)
	rule, _ := tbl.Match("foo.local", "/", "GET", nil)
	if rule.Rewrite == nil || rule.Rewrite.ReplacePrefix != "/v3" {
		t.Fatalf("nginx-prefix rewrite should be honored, got %+v", rule.Rewrite)
	}
}

func TestBuildTable_CanaryIngressSkipped(t *testing.T) {
	main := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "main"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("main-svc", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	canary := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "canary",
			Annotations: map[string]string{AnnotationPrefix + "/canary": "true"}},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("canary-svc", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{main, canary}, nil)
	rule, _ := tbl.Match("foo.local", "/", "GET", nil)
	if rule.Backend.ServiceName != "default/main-svc" {
		t.Fatalf("canary should be skipped, got %s", rule.Backend.ServiceName)
	}
}

func TestBuildTable_NamedPort(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
	}
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("echo", networkingv1.ServiceBackendPort{Name: "http"})}},
			}},
		}}},
	}
	services := map[ServiceKey]*corev1.Service{ServiceKeyOf("default", "echo"): svc}
	tbl := BuildTable([]*networkingv1.Ingress{ing}, services)
	rule, _ := tbl.Match("foo.local", "/", "GET", nil)
	if rule.Backend.Port != 8080 {
		t.Fatalf("named port should resolve to 8080, got %d", rule.Backend.Port)
	}
}

func TestBuildTable_MultipleIngressSameHost(t *testing.T) {
	// 同 host 多 Ingress 的 path 应合并到同一 HostRules
	ing1 := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing1"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/api", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("api", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	ing2 := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing2"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/web", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("web", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{ing1, ing2}, nil)
	rules := tbl.Hosts["foo.local"]
	if rules == nil || len(rules.Paths) != 2 {
		t.Fatalf("expected 2 paths merged, got %d", len(rules.Paths))
	}
}

// canaryIngress 构造 canary Ingress（annotation canary=true + 额外 annotation）。
func canaryIngress(name, host, path, svc string, port int, extra map[string]string) *networkingv1.Ingress {
	anns := map[string]string{AnnotationPrefix + "/canary": "true"}
	for k, v := range extra {
		anns[k] = v
	}
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name, Annotations: anns},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: path, PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend(svc, networkingv1.ServiceBackendPort{Number: int32(port)})}},
			}},
		}}},
	}
}

func TestBuildTable_CanaryMerge_Weight(t *testing.T) {
	main := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "main"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("main-svc", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	canary := canaryIngress("canary", "foo.local", "/", "canary-svc", 80,
		map[string]string{AnnotationPrefix + "/canary-weight": "30"})

	tbl := BuildTable([]*networkingv1.Ingress{main, canary}, nil)
	rule, _ := tbl.Match("foo.local", "/", "GET", nil)
	if rule.Backend.ServiceName != "default/main-svc" {
		t.Fatalf("main backend should be unchanged: %s", rule.Backend.ServiceName)
	}
	if rule.Canary == nil || rule.Canary.Weight != 30 || rule.Canary.Backend.ServiceName != "default/canary-svc" {
		t.Fatalf("canary not merged correctly: %+v", rule.Canary)
	}
}

func TestBuildTable_CanaryMerge_Header(t *testing.T) {
	main := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "main"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/api", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("main-svc", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	canary := canaryIngress("canary", "foo.local", "/api", "canary-svc", 80,
		map[string]string{AnnotationPrefix + "/canary-by-header": "X-Canary"})

	tbl := BuildTable([]*networkingv1.Ingress{main, canary}, nil)
	rule, _ := tbl.Match("foo.local", "/api", "GET", nil)
	if rule.Canary == nil || rule.Canary.Header != "X-Canary" {
		t.Fatalf("canary header not merged: %+v", rule.Canary)
	}
}

func TestBuildTable_CanaryMerge_NginxCompat(t *testing.T) {
	main := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "main"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("main-svc", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	// nginx 前缀 canary annotation
	canary := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "canary",
			Annotations: map[string]string{
				NginxPrefix + "/canary":        "true",
				NginxPrefix + "/canary-weight": "20",
			}},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("canary-svc", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{main, canary}, nil)
	rule, _ := tbl.Match("foo.local", "/", "GET", nil)
	if rule.Canary == nil || rule.Canary.Weight != 20 {
		t.Fatalf("nginx-prefix canary should merge: %+v", rule.Canary)
	}
}

func TestBuildTable_LimitRPS(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ing",
			Annotations: map[string]string{AnnotationPrefix + "/limit-rps": "50"}},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: "foo.local",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: ptrPathType(networkingv1.PathTypePrefix),
					Backend: ingBackend("echo", networkingv1.ServiceBackendPort{Number: 80})}},
			}},
		}}},
	}
	tbl := BuildTable([]*networkingv1.Ingress{ing}, nil)
	rule, _ := tbl.Match("foo.local", "/", "GET", nil)
	if rule.Backend.LimitRPS != 50 {
		t.Fatalf("expected LimitRPS=50, got %d", rule.Backend.LimitRPS)
	}
}
