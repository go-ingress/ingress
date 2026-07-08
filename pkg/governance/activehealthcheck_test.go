package governance

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/go-zeus/zeus/registry"
	ztypes "github.com/go-zeus/zeus/types"
)

func TestActiveHealthCheck_HealthyBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer backend.Close()
	host, port := parseHostPort(t, backend.URL)

	disc := &mockDisc{services: []string{"svc"}, entry: serviceEntry("i1", "svc", host, port)}
	passive := NewPassiveHealthCheck(1, time.Minute)
	active := NewActiveHealthCheck(passive, disc.Services, disc,
		WithActiveInterval(50*time.Millisecond),
		WithHealthPathProvider(func(string) string { return "/health" }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active.Start(ctx)

	time.Sleep(150 * time.Millisecond)
	if !passive.IsHealthy(net.JoinHostPort(host, strconv.Itoa(port))) {
		t.Fatal("healthy backend should be marked healthy")
	}
}

func TestActiveHealthCheck_UnhealthyBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer backend.Close()
	host, port := parseHostPort(t, backend.URL)

	disc := &mockDisc{services: []string{"svc"}, entry: serviceEntry("i1", "svc", host, port)}
	passive := NewPassiveHealthCheck(2, time.Minute) // threshold=2
	active := NewActiveHealthCheck(passive, disc.Services, disc,
		WithActiveInterval(50*time.Millisecond),
		WithHealthPathProvider(func(string) string { return "/health" }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active.Start(ctx)

	time.Sleep(250 * time.Millisecond) // 等待 2+ 次探测
	if passive.IsHealthy(net.JoinHostPort(host, strconv.Itoa(port))) {
		t.Fatal("unhealthy backend should be marked unhealthy after threshold")
	}
}

// TestActiveHealthCheck_NotOptInSkipsProbing 验证 opt-in 语义：
// service 未声明健康路径（provider 返回空）时不探测，即使后端对 /health 返回 500 也不剔除。
// 这是核心安全保证——避免误剔除未实现健康端点的后端（回归此 bug 的守护测试）。
func TestActiveHealthCheck_NotOptInSkipsProbing(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // 即使后端 500
	}))
	defer backend.Close()
	host, port := parseHostPort(t, backend.URL)

	disc := &mockDisc{services: []string{"svc"}, entry: serviceEntry("i1", "svc", host, port)}
	passive := NewPassiveHealthCheck(1, time.Minute)
	active := NewActiveHealthCheck(passive, disc.Services, disc,
		WithActiveInterval(50*time.Millisecond),
		WithHealthPathProvider(func(string) string { return "" }), // 未声明健康路径
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	if !passive.IsHealthy(net.JoinHostPort(host, strconv.Itoa(port))) {
		t.Fatal("未声明健康路径的 service 不应被探测/剔除")
	}
}

// TestActiveHealthCheck_NoProviderSkipsProbing 验证无 provider 时安全降级（不探测）。
func TestActiveHealthCheck_NoProviderSkipsProbing(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer backend.Close()
	host, port := parseHostPort(t, backend.URL)

	disc := &mockDisc{services: []string{"svc"}, entry: serviceEntry("i1", "svc", host, port)}
	passive := NewPassiveHealthCheck(1, time.Minute)
	active := NewActiveHealthCheck(passive, disc.Services, disc, WithActiveInterval(50*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	if !passive.IsHealthy(net.JoinHostPort(host, strconv.Itoa(port))) {
		t.Fatal("未提供 healthPath provider 时不应探测任何 service")
	}
}

func parseHostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func serviceEntry(id, name, ip string, port int) *ztypes.ServiceEntry {
	entry := ztypes.NewServiceEntry()
	_ = entry.AddInstance(&ztypes.Instance{ID: id, Name: name, IP: ip, Port: port})
	return entry
}

type mockDisc struct {
	services []string
	entry    *ztypes.ServiceEntry
}

func (m *mockDisc) GetService(_ context.Context, _ string) (*ztypes.ServiceEntry, error) {
	return m.entry, nil
}
func (m *mockDisc) Services() []string { return m.services }

// 编译期检查 mockDisc 实现 registry.Discovery。
var _ registry.Discovery = (*mockDisc)(nil)
