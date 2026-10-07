package dataplane

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics，注册到默认 registry（controller-runtime metrics server :8081/metrics 暴露）。
var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermes_requests_total",
		Help: "Total number of requests processed by the Hermes data plane.",
	}, []string{"host", "method", "status"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermes_request_duration_seconds",
		Help:    "Request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"host", "method", "status"})

	upstreamFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermes_upstream_failures_total",
		Help: "Total number of upstream failures (5xx responses).",
	}, []string{"host"})
)

// statusRecorder 包装 ResponseWriter 捕获状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush 实现 http.Flusher，委托底层 ResponseWriter。
// 缺失会导致代理的 w.(http.Flusher) 断言失败，SSE 流式响应被全量缓冲。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 实现 http.Hijacker，委托底层 ResponseWriter。
// 缺失会导致代理的 w.(http.Hijacker) 断言失败，WebSocket 升级被拒
//（proxy: hijack not supported）。hijack 后响应不经 WriteHeader，
// metrics 对该请求记初始值 200（连接时长计入 duration，与 nginx 的
// WS 记账口径一致）。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("metrics: inner ResponseWriter %T does not implement http.Hijacker", r.ResponseWriter)
	}
	return hj.Hijack()
}

// metricsMiddleware 记录请求 metrics（状态码 + 延迟 + 上游失败）。
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		status := strconv.Itoa(rec.status)
		requestsTotal.WithLabelValues(r.Host, r.Method, status).Inc()
		requestDuration.WithLabelValues(r.Host, r.Method, status).Observe(time.Since(start).Seconds())
		if rec.status >= 500 {
			upstreamFailures.WithLabelValues(r.Host).Inc()
		}
	})
}
