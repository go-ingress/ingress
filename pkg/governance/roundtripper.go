// Package governance 把 zeus 治理三件套（熔断/限流/重试）+ 被动健康检查接入 proxy 请求路径。
//
// GoverningRoundTripper 通过 proxy.WithTransport 注入，按 service+cluster 维度隔离治理
//（key 从请求 context 提取，由 IngressSelector.Pick 注入）。
//
// 请求路径：限流 → 熔断 → 重试 → 转发 → 被动健康检查。
// per-service 限流：BackendInfo.LimitRPS>0 时按 key 独立令牌桶，否则用全局默认。
package governance

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-zeus/zeus/circuitbreaker"
	"github.com/go-zeus/zeus/circuitbreaker/counter"
	clusterbreaker "github.com/go-zeus/zeus/circuitbreaker/cluster"
	"github.com/go-zeus/zeus/ratelimit"
	"github.com/go-zeus/zeus/ratelimit/token"
	clusterlimiter "github.com/go-zeus/zeus/ratelimit/cluster"
	"github.com/go-zeus/zeus/retry"
	"github.com/go-zeus/zeus/retry/exponential"
	clusterretry "github.com/go-zeus/zeus/retry/cluster"
)

// 编译期检查 GoverningRoundTripper 实现 http.RoundTripper。
var _ http.RoundTripper = (*GoverningRoundTripper)(nil)

// GoverningRoundTripper 包装底层 RoundTripper，接入 zeus 治理三件套 + 被动健康检查。
type GoverningRoundTripper struct {
	next     http.RoundTripper
	breaker  *clusterbreaker.ClusterBreaker
	limiter  *clusterlimiter.ClusterLimiter // 全局默认限流（LimitRPS=0 时用）
	retrier  *clusterretry.ClusterRetrier
	passive  *PassiveHealthCheck
	limiters sync.Map // key -> *clusterlimiter.ClusterLimiter（per-key，按 BackendInfo.LimitRPS）
}

// Option 配置项。
type Option func(*GoverningRoundTripper)

// WithCircuitBreaker 注入熔断器（按 service+cluster 隔离）。
func WithCircuitBreaker(cb *clusterbreaker.ClusterBreaker) Option {
	return func(g *GoverningRoundTripper) { g.breaker = cb }
}

// WithRateLimiter 注入全局限流器（LimitRPS=0 的请求用；按 service+cluster 隔离）。
func WithRateLimiter(rl *clusterlimiter.ClusterLimiter) Option {
	return func(g *GoverningRoundTripper) { g.limiter = rl }
}

// WithRetry 注入重试器（按 service+cluster 隔离）。
func WithRetry(cr *clusterretry.ClusterRetrier) Option {
	return func(g *GoverningRoundTripper) { g.retrier = cr }
}

// WithPassiveHealthCheck 注入被动健康检查。
func WithPassiveHealthCheck(p *PassiveHealthCheck) Option {
	return func(g *GoverningRoundTripper) { g.passive = p }
}

// NewGoverningRoundTripper 创建治理 RoundTripper。未注入的治理组件跳过（透传）。
func NewGoverningRoundTripper(next http.RoundTripper, opts ...Option) *GoverningRoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	g := &GoverningRoundTripper{next: next}
	for _, o := range opts {
		o(g)
	}
	return g
}

// DefaultBreaker 默认熔断器：20 请求窗口，50% 失败率触发，按 key 隔离。
func DefaultBreaker() *clusterbreaker.ClusterBreaker {
	return clusterbreaker.New(func() circuitbreaker.Breaker { return counter.New(20, 0.5) })
}

// DefaultLimiter 默认限流器：100 rps，burst 100，按 key 隔离。
func DefaultLimiter() *clusterlimiter.ClusterLimiter {
	return clusterlimiter.New(func() ratelimit.Limiter { return token.New(100, 100) })
}

// DefaultRetrier 默认重试器：最多 2 次，100ms 起步指数退避，按 key 隔离。
func DefaultRetrier() *clusterretry.ClusterRetrier {
	return clusterretry.New(func() retry.Retrier { return exponential.New(2, 100*time.Millisecond) })
}

// RoundTrip 接入治理：限流 → 熔断 → 重试 → 转发 → 被动健康检查。
func (g *GoverningRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := BackendFromContext(req.Context())
	key := b.Key()
	if key == "" {
		// 无 backend 信息（未走 IngressSelector，如控制面自探），透传。
		return g.next.RoundTrip(req)
	}

	// 1. 限流（per-key 自定义 rps 或全局默认）
	if lim := g.getLimiter(key, b.LimitRPS); lim != nil && !lim.AllowKey(key) {
		return nil, ErrRateLimited
	}

	// 2. 熔断 + 转发（含重试）
	var resp *http.Response
	var rtErr error
	fnCalled := false
	if g.breaker != nil {
		_ = g.breaker.ExecuteKey(key, func() error {
			fnCalled = true
			resp, rtErr = g.roundTripWithRetry(req, key)
			return classifyForBreaker(resp, rtErr)
		})
		if !fnCalled {
			return nil, ErrCircuitOpen // 熔断打开，fn 未执行
		}
	} else {
		resp, rtErr = g.roundTripWithRetry(req, key)
	}

	// 3. 被动健康检查
	if g.passive != nil {
		g.passive.Report(req.URL.Host, resp, rtErr)
	}
	return resp, rtErr
}

// getLimiter 返回限流器：LimitRPS>0 用 per-key 独立令牌桶，否则用全局默认。
func (g *GoverningRoundTripper) getLimiter(key string, rps int) *clusterlimiter.ClusterLimiter {
	if rps <= 0 {
		return g.limiter
	}
	if v, ok := g.limiters.Load(key); ok {
		return v.(*clusterlimiter.ClusterLimiter)
	}
	lim := clusterlimiter.New(func() ratelimit.Limiter { return token.New(float64(rps), rps) })
	actual, _ := g.limiters.LoadOrStore(key, lim)
	return actual.(*clusterlimiter.ClusterLimiter)
}

// roundTripWithRetry 转发 + 重试（5xx/网络错误，按 retrier 退避）。
func (g *GoverningRoundTripper) roundTripWithRetry(req *http.Request, key string) (*http.Response, error) {
	if g.retrier == nil {
		return g.next.RoundTrip(req)
	}
	retrier := g.retrier.NewRetrieverForKey(key)
	var resp *http.Response
	var err error
	for {
		resp, err = g.next.RoundTrip(req)
		if !shouldRetry(resp, err) {
			return resp, err
		}
		d, ok := retrier.Next()
		if !ok {
			return resp, err
		}
		if !resetBody(req) {
			return resp, err // 无法重放 body，放弃重试
		}
		select {
		case <-time.After(d):
		case <-req.Context().Done():
			return resp, req.Context().Err()
		}
	}
}

// classifyForBreaker 熔断判定：5xx 或错误视为失败（触发 MarkFailed）。
func classifyForBreaker(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	if resp != nil && resp.StatusCode >= 500 {
		return errors.New("governance: upstream 5xx")
	}
	return nil
}

// shouldRetry 重试判定：网络错误或 502/503/504。
func shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

// resetBody 重放请求 body（用于重试）。无 body 返回 true；有 body 但无法重放返回 false。
func resetBody(req *http.Request) bool {
	if req.Body == nil || req.Body == http.NoBody {
		return true
	}
	if req.GetBody == nil {
		return false
	}
	body, err := req.GetBody()
	if err != nil {
		return false
	}
	req.Body = body
	return true
}
