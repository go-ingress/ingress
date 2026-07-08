package translator

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-ingress/ingress/pkg/apis/hermes/v1alpha1"
	"github.com/go-ingress/ingress/pkg/model"
)

func TestBuildTableFromHTTPProxy_Basic(t *testing.T) {
	px := &v1alpha1.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: v1alpha1.HTTPProxySpec{
			Host: "echo.example.com",
			Routes: []v1alpha1.HTTPProxyRoute{{
				Conditions: []v1alpha1.HTTPProxyCondition{{Path: "/api", Prefix: true}},
				Services:   []v1alpha1.HTTPProxyService{{Name: "echo", Port: 80}},
			}},
		},
	}
	tbl := BuildTableFromHTTPProxy([]*v1alpha1.HTTPProxy{px}, nil)
	rule, ok := tbl.Match("echo.example.com", "/api/x", "GET", nil)
	if !ok || rule.Backend.ServiceName != "default/echo" {
		t.Fatalf("unexpected: %+v ok=%v", rule, ok)
	}
	if rule.PathType != model.PathTypePrefix {
		t.Fatalf("expected Prefix, got %s", rule.PathType)
	}
}

func TestBuildTableFromHTTPProxy_TrafficSplit(t *testing.T) {
	px := &v1alpha1.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: v1alpha1.HTTPProxySpec{
			Host: "echo.example.com",
			Routes: []v1alpha1.HTTPProxyRoute{{
				Services: []v1alpha1.HTTPProxyService{
					{Name: "echo", Port: 80, Weight: 90},
					{Name: "echo-canary", Port: 80, Weight: 10},
				},
			}},
		},
	}
	tbl := BuildTableFromHTTPProxy([]*v1alpha1.HTTPProxy{px}, nil)
	rule, _ := tbl.Match("echo.example.com", "/", "GET", nil)
	if rule.Backend.ServiceName != "default/echo" {
		t.Fatalf("primary wrong: %s", rule.Backend.ServiceName)
	}
	if rule.Canary == nil || rule.Canary.Weight != 10 || rule.Canary.Backend.ServiceName != "default/echo-canary" {
		t.Fatalf("canary split wrong: %+v", rule.Canary)
	}
}

func TestBuildTableFromHTTPProxy_Rewrite(t *testing.T) {
	px := &v1alpha1.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: v1alpha1.HTTPProxySpec{
			Host: "echo.example.com",
			Routes: []v1alpha1.HTTPProxyRoute{{
				Conditions: []v1alpha1.HTTPProxyCondition{{Path: "/api", Prefix: true}},
				Services:   []v1alpha1.HTTPProxyService{{Name: "echo", Port: 80}},
				Rewrite:    &v1alpha1.HTTPProxyRewrite{Prefix: "/v2"},
			}},
		},
	}
	tbl := BuildTableFromHTTPProxy([]*v1alpha1.HTTPProxy{px}, nil)
	rule, _ := tbl.Match("echo.example.com", "/api", "GET", nil)
	if rule.Rewrite == nil || rule.Rewrite.ReplacePrefix != "/v2" {
		t.Fatalf("rewrite wrong: %+v", rule.Rewrite)
	}
}

func TestBuildTableFromHTTPProxy_ExactPath(t *testing.T) {
	px := &v1alpha1.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "echo"},
		Spec: v1alpha1.HTTPProxySpec{
			Host: "echo.example.com",
			Routes: []v1alpha1.HTTPProxyRoute{{
				Conditions: []v1alpha1.HTTPProxyCondition{{Path: "/health", Prefix: false}},
				Services:   []v1alpha1.HTTPProxyService{{Name: "echo", Port: 80}},
			}},
		},
	}
	tbl := BuildTableFromHTTPProxy([]*v1alpha1.HTTPProxy{px}, nil)
	rule, _ := tbl.Match("echo.example.com", "/health", "GET", nil)
	if rule.PathType != model.PathTypeExact {
		t.Fatalf("expected Exact, got %s", rule.PathType)
	}
}
