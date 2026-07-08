package governance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-zeus/zeus/ratelimit"
	clusterlimiter "github.com/go-zeus/zeus/ratelimit/cluster"
)

func TestBackendContext(t *testing.T) {
	ctx := WithBackend(context.Background(), BackendInfo{Service: "ns/svc", Cluster: "canary"})
	b, ok := BackendFromContext(ctx)
	if !ok || b.Service != "ns/svc" || b.Cluster != "canary" {
		t.Fatalf("unexpected backend: %+v ok=%v", b, ok)
	}
	if b.Key() != "ns/svc#canary" {
		t.Fatalf("unexpected key: %s", b.Key())
	}
}

func TestBackendInfo_KeyEmpty(t *testing.T) {
	if (BackendInfo{}).Key() != "" {
		t.Fatal("empty service should yield empty key")
	}
	if (BackendInfo{Service: "s"}).Key() != "s#default" {
		t.Fatal("missing cluster should default to #default")
	}
}

func TestRoundTrip_NoBackendPassthrough(t *testing.T) {
	called := false
	rt := NewGoverningRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}))
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil) // 无 backend context
	resp, err := rt.RoundTrip(req)
	if err != nil || !called || resp.StatusCode != 200 {
		t.Fatalf("passthrough failed: called=%v resp=%v err=%v", called, resp, err)
	}
}

func TestRoundTrip_RateLimited(t *testing.T) {
	rt := NewGoverningRoundTripper(http.DefaultTransport, WithRateLimiter(NewAlwaysDenyLimiter()))
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	req = req.WithContext(WithBackend(context.Background(), BackendInfo{Service: "ns/svc"}))
	_, err := rt.RoundTrip(req)
	if err != ErrRateLimited {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestRoundTrip_SuccessPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer backend.Close()

	rt := NewGoverningRoundTripper(http.DefaultTransport,
		WithRateLimiter(NewAlwaysAllowLimiter()),
	)
	req := httptest.NewRequest(http.MethodGet, backend.URL+"/", nil)
	req = req.WithContext(WithBackend(context.Background(), BackendInfo{Service: "ns/svc"}))
	resp, err := rt.RoundTrip(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("success path failed: resp=%v err=%v", resp, err)
	}
}

func TestPassiveHealthCheck(t *testing.T) {
	p := NewPassiveHealthCheck(2, 0) // threshold=2, ejectTime=0（立即半开）
	if !p.IsHealthy("a:80") {
		t.Fatal("should be healthy initially")
	}
	p.Report("a:80", &http.Response{StatusCode: 500}, nil)
	if !p.IsHealthy("a:80") {
		t.Fatal("1 failure should not eject")
	}
	// 成功后清零恢复
	p.Report("a:80", &http.Response{StatusCode: 200}, nil)
	if !p.IsHealthy("a:80") {
		t.Fatal("should recover after success")
	}
}

func TestShouldRetry(t *testing.T) {
	if !shouldRetry(nil, errFake) {
		t.Fatal("network error should retry")
	}
	if !shouldRetry(&http.Response{StatusCode: 502}, nil) {
		t.Fatal("502 should retry")
	}
	if !shouldRetry(&http.Response{StatusCode: 503}, nil) {
		t.Fatal("503 should retry")
	}
	if shouldRetry(&http.Response{StatusCode: 200}, nil) {
		t.Fatal("200 should not retry")
	}
	if shouldRetry(&http.Response{StatusCode: 400}, nil) {
		t.Fatal("400 should not retry")
	}
}

// --- 测试辅助 ---

var errFake = errors.New("fake")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// NewAlwaysDenyLimiter 始终拒绝的限流器（测试用）。
func NewAlwaysDenyLimiter() *clusterlimiter.ClusterLimiter {
	return clusterlimiter.New(func() ratelimit.Limiter { return denyLimiter{} })
}

// NewAlwaysAllowLimiter 始终允许的限流器（测试用）。
func NewAlwaysAllowLimiter() *clusterlimiter.ClusterLimiter {
	return clusterlimiter.New(func() ratelimit.Limiter { return allowLimiter{} })
}

type denyLimiter struct{}

func (denyLimiter) Allow() bool                     { return false }
func (denyLimiter) Reserve() ratelimit.WaitDuration { return ratelimit.WaitDuration{Allow: false} }
func (denyLimiter) Rate() float64                   { return 0 }

type allowLimiter struct{}

func (allowLimiter) Allow() bool                     { return true }
func (allowLimiter) Reserve() ratelimit.WaitDuration { return ratelimit.WaitDuration{Allow: true} }
func (allowLimiter) Rate() float64                   { return 100 }

func TestRoundTrip_PerServiceLimitRPS(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer backend.Close()
	// 不注入全局限流器（g.limiter=nil），仅 per-key LimitRPS=1
	rt := NewGoverningRoundTripper(http.DefaultTransport)
	limited := false
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, backend.URL+"/", nil)
		req = req.WithContext(WithBackend(context.Background(), BackendInfo{Service: "ns/svc", LimitRPS: 1}))
		if _, err := rt.RoundTrip(req); err == ErrRateLimited {
			limited = true
		}
	}
	if !limited {
		t.Fatal("expected at least one rate-limited request with LimitRPS=1")
	}
}
