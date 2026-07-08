package dataplane

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/go-zeus/zeus/types"

	"github.com/go-ingress/ingress/pkg/discovery"
	"github.com/go-ingress/ingress/pkg/model"
)

// startBackend 启动测试后端，返回实例（IP/Port 从 URL 解析）。
func startBackend(t *testing.T, name string) (*httptest.Server, *types.Instance) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(name))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	return srv, &types.Instance{
		ID:       name,
		Name:     "svc/" + name,
		Cluster:  model.DefaultCluster,
		Protocol: "http",
		IP:       host,
		Port:     port,
	}
}

func TestIngressSelector_Pick_Route(t *testing.T) {
	_, ins := startBackend(t, "echo")
	sel := NewIngressSelector(discovery.NewStatic(ins))
	sel.Update(&model.RoutingTable{
		Hosts: map[string]*model.HostRules{
			"foo.local": {Host: "foo.local", Paths: []*model.PathRule{
				{PathType: model.PathTypePrefix, Path: "/", Backend: &model.BackendRef{ServiceName: ins.Name}},
			}},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Host = "foo.local"
	target, err := sel.Pick(req)
	if err != nil {
		t.Fatalf("Pick error: %v", err)
	}
	if target.Scheme != "http" {
		t.Fatalf("unexpected scheme: %s", target.Scheme)
	}
	if target.Host != net.JoinHostPort(ins.IP, strconv.Itoa(ins.Port)) {
		t.Fatalf("unexpected target host: %s", target.Host)
	}
}

func TestIngressSelector_Pick_NotFound(t *testing.T) {
	_, ins := startBackend(t, "echo")
	sel := NewIngressSelector(discovery.NewStatic(ins))
	sel.Update(&model.RoutingTable{Hosts: map[string]*model.HostRules{}}) // 无 Default
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.local"
	if _, err := sel.Pick(req); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestIngressSelector_Pick_Default(t *testing.T) {
	_, ins := startBackend(t, "echo")
	sel := NewIngressSelector(discovery.NewStatic(ins))
	sel.Update(&model.RoutingTable{
		Hosts:   map[string]*model.HostRules{},
		Default: &model.BackendRef{ServiceName: ins.Name},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.local"
	target, err := sel.Pick(req)
	if err != nil {
		t.Fatalf("expected default backend, got err: %v", err)
	}
	if target.Host == "" {
		t.Fatal("expected non-empty target")
	}
}

func TestIngressSelector_Pick_NoTable(t *testing.T) {
	sel := NewIngressSelector(discovery.NewStatic())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := sel.Pick(req); err != ErrNoTable {
		t.Fatalf("expected ErrNoTable, got %v", err)
	}
}

func TestIngressSelector_Pick_Unavailable(t *testing.T) {
	// Discovery 无实例 → ErrUnavailable
	sel := NewIngressSelector(discovery.NewStatic())
	sel.Update(&model.RoutingTable{
		Default: &model.BackendRef{ServiceName: "missing"},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := sel.Pick(req); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestDecideCanary_Header(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	cfg := &model.CanaryConfig{Header: "X-Canary", Backend: &model.BackendRef{}}

	req.Header.Set("X-Canary", "always")
	if !decideCanary(req, cfg) {
		t.Fatal("header=always should route to canary")
	}
	req.Header.Set("X-Canary", "never")
	if decideCanary(req, cfg) {
		t.Fatal("header=never should not route to canary")
	}
	req.Header.Set("X-Canary", "v2")
	cfg.Value = "v2"
	if !decideCanary(req, cfg) {
		t.Fatal("header=value match should route to canary")
	}
}

func TestDecideCanary_Cookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "canary", Value: "true"})
	if !decideCanary(req, &model.CanaryConfig{Cookie: "canary", Backend: &model.BackendRef{}}) {
		t.Fatal("cookie=true should route to canary")
	}
}

func TestApplyRewrite(t *testing.T) {
	if got := applyRewrite("/api", "/api/users", &model.RewriteConfig{ReplacePrefix: "/v2"}); got != "/v2/users" {
		t.Fatalf("rewrite failed: %s", got)
	}
	// 不匹配前缀时不重写
	if got := applyRewrite("/api", "/other", &model.RewriteConfig{ReplacePrefix: "/v2"}); got != "/other" {
		t.Fatalf("non-matching rewrite should be no-op: %s", got)
	}
}
