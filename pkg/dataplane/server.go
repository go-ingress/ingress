package dataplane

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-zeus/zeus/middleware/accesslog"
	"github.com/go-zeus/zeus/middleware/requestid"
	"github.com/go-zeus/zeus/proxy"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/go-ingress/ingress/pkg/governance"
)

// Config 数据面服务器配置。
type Config struct {
	Addr      string            // 监听地址，如 ":8080"
	Selector  proxy.Selector    // 必填，通常为 *IngressSelector
	Transport http.RoundTripper // 可选，默认 http.DefaultTransport（推荐 GoverningRoundTripper）
	CertPool  *CertPool         // 可选，启用 TLS（SNI 动态证书）
}

// Server Hermes 数据面服务器。
type Server struct {
	cfg     Config
	proxy   proxy.Proxy
	httpSrv *http.Server
}

// New 装配数据面：middleware 链 → proxy → http.Server。
//
// 中间件链顺序（外→内）：requestid → accesslog → metrics → recovery → proxy。
func New(cfg Config) *Server {
	if cfg.Selector == nil {
		panic("dataplane: Config.Selector is required")
	}
	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	p := proxy.New(
		proxy.WithSelector(cfg.Selector),
		proxy.WithTransport(transport),
		proxy.WithErrorHandler(httpErrorHandler),
	)
	handler := requestid.HTTPMiddleware(accesslog.HTTPMiddleware(metricsMiddleware(recoverHTTP(p))))
	return &Server{
		cfg:   cfg,
		proxy: p,
		httpSrv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
		},
	}
}

// Start 启动服务器（阻塞）。
//
// 配置 CertPool 时启用 TLS（SNI 动态证书，HTTP/2 over TLS 自动协商）；
// 明文模式启用 h2c（HTTP/2 cleartext），支持 gRPC 路由（实验性）。
func (s *Server) Start(_ context.Context) error {
	if s.cfg.CertPool != nil {
		s.httpSrv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: s.cfg.CertPool.GetCertificate,
		}
		return s.httpSrv.ListenAndServeTLS("", "")
	}
	// 明文：h2c 包装，支持 HTTP/2 cleartext（gRPC 客户端 h2c 直连）。
	s.httpSrv.Handler = h2c.NewHandler(s.httpSrv.Handler, &http2.Server{})
	return s.httpSrv.ListenAndServe()
}

// Shutdown 优雅关闭。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// recoverHTTP panic 恢复（zeus recovery 仅有 Interceptor 风格，数据面用 http.Handler 风格内联）。
func recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("dataplane panic recovered", "err", rec, "path", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// httpErrorHandler 把路由/治理错误映射为合适的 HTTP 状态码。
// proxy.handleHTTP 在 Pick 失败 / 转发失败时调用此函数。
func httpErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 Not Found\n"))
	case errors.Is(err, governance.ErrRateLimited):
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("429 Too Many Requests\n"))
	case errors.Is(err, governance.ErrCircuitOpen), errors.Is(err, ErrUnavailable), errors.Is(err, ErrNoTable):
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("503 Service Unavailable\n"))
	default:
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprintf(w, "502 Bad Gateway: %v\n", err)
	}
}
