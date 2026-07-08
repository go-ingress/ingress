package translator

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-ingress/ingress/pkg/model"
)

func strPtr(s string) *string { return &s }

func TestBuildTableFromHTTPRoute_Basic(t *testing.T) {
	pathType := gatewayv1.PathMatchPathPrefix
	method := gatewayv1.HTTPMethodGet
	port := gatewayv1.PortNumber(80)
	weight := int32(1)
	rt := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"echo.example.com"},
			Rules: []gatewayv1.HTTPRouteRule{{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path:   &gatewayv1.HTTPPathMatch{Type: &pathType, Value: strPtr("/api")},
					Method: &method,
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{Name: "echo", Port: &port},
						Weight:                 &weight,
					},
				}},
			}},
		},
	}
	tbl := BuildTableFromHTTPRoute([]*gatewayv1.HTTPRoute{rt}, nil)
	rule, ok := tbl.Match("echo.example.com", "/api/x", "GET", nil)
	if !ok {
		t.Fatal("expected match")
	}
	if rule.Backend.ServiceName != "default/echo" || rule.Backend.Port != 80 {
		t.Fatalf("unexpected backend: %+v", rule.Backend)
	}
	if rule.PathType != model.PathTypePrefix || rule.Path != "/api" {
		t.Fatalf("unexpected path: %s %s", rule.PathType, rule.Path)
	}
	if rule.Method != "GET" {
		t.Fatalf("unexpected method: %s", rule.Method)
	}
}

func TestBuildTableFromHTTPRoute_TrafficSplit(t *testing.T) {
	port := gatewayv1.PortNumber(80)
	wPrimary := int32(90)
	wCanary := int32(10)
	rt := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"echo.example.com"},
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{
					{BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{Name: "echo", Port: &port},
						Weight:                 &wPrimary,
					}},
					{BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{Name: "echo-canary", Port: &port},
						Weight:                 &wCanary,
					}},
				},
			}},
		},
	}
	tbl := BuildTableFromHTTPRoute([]*gatewayv1.HTTPRoute{rt}, nil)
	rule, _ := tbl.Match("echo.example.com", "/", "GET", nil)
	if rule.Backend.ServiceName != "default/echo" {
		t.Fatalf("primary backend wrong: %s", rule.Backend.ServiceName)
	}
	if rule.Canary == nil || rule.Canary.Weight != 10 || rule.Canary.Backend.ServiceName != "default/echo-canary" {
		t.Fatalf("canary split not translated: %+v", rule.Canary)
	}
}

func TestBuildTableFromHTTPRoute_ExactPath(t *testing.T) {
	pathType := gatewayv1.PathMatchExact
	port := gatewayv1.PortNumber(80)
	weight := int32(1)
	rt := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"echo.example.com"},
			Rules: []gatewayv1.HTTPRouteRule{{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{Type: &pathType, Value: strPtr("/health")},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{Name: "echo", Port: &port},
						Weight:                 &weight,
					},
				}},
			}},
		},
	}
	tbl := BuildTableFromHTTPRoute([]*gatewayv1.HTTPRoute{rt}, nil)
	rule, _ := tbl.Match("echo.example.com", "/health", "GET", nil)
	if rule.PathType != model.PathTypeExact {
		t.Fatalf("expected Exact, got %s", rule.PathType)
	}
}
