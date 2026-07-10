// Command hermes 是 Hermes ingress 控制器入口。
//
// 两种运行模式：
//
//	hermes --demo                     # demo 模式：硬编码路由 + 内嵌 echo 后端，无 K8s 依赖（开发验证）
//	hermes --ingress-class=hermes     # K8s 模式：controller-runtime 监听 Ingress/Service/Endpoints/Secret
//
// K8s 模式典型参数（多副本 HA + 全功能）：
//
//	hermes --addr=:8080 --ingress-class=hermes --metrics-addr=:8081 \
//	       --status-hostname=hermes.example.com --enable-gateway-api --enable-httpproxy --leader-elect
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-zeus/zeus/types"

	"github.com/go-ingress/ingress/pkg/controller"
	"github.com/go-ingress/ingress/pkg/dataplane"
	"github.com/go-ingress/ingress/pkg/discovery"
	"github.com/go-ingress/ingress/pkg/governance"
	"github.com/go-ingress/ingress/pkg/model"
)

func main() {
	var (
		demo            = flag.Bool("demo", false, "demo 模式：硬编码路由 + 内嵌 echo 后端，无 K8s 依赖")
		addr            = flag.String("addr", ":8080", "数据面监听地址")
		ingressClass    = flag.String("ingress-class", "hermes", "IngressClass 过滤（spec.ingressClassName）")
		watchNS         = flag.String("watch-namespace", "", "监听命名空间（空=全命名空间）")
		statusHost      = flag.String("status-hostname", "", "Ingress status LoadBalancer hostname")
		statusAddr      = flag.String("status-address", "", "Ingress status LoadBalancer IP")
		metricsAddr     = flag.String("metrics-addr", ":8081", "controller-runtime metrics 端点")
		enableGW        = flag.Bool("enable-gateway-api", false, "启用 Gateway API（HTTPRoute）翻译")
		enableHTTPProxy = flag.Bool("enable-httpproxy", false, "启用 Hermes HTTPProxy CRD 翻译")
		leaderElect     = flag.Bool("leader-elect", false, "启用 leader election（多副本 HA）")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *demo {
		runDemo(ctx, *addr)
		return
	}
	runK8s(ctx, *addr, *ingressClass, *watchNS, *statusHost, *statusAddr, *metricsAddr, *enableGW, *enableHTTPProxy, *leaderElect)
}

// runK8s K8s 模式：控制面（controller-runtime）+ 数据面（zeus proxy）同进程。
func runK8s(ctx context.Context, addr, class, watchNS, statusHost, statusAddr, metricsAddr string, enableGatewayAPI, enableHTTPProxy, leaderElect bool) {
	disc := discovery.NewK8sDiscovery()
	hc := governance.NewPassiveHealthCheck(5, 30*time.Second)
	activeHC := governance.NewActiveHealthCheck(hc, disc.Services, disc,
		governance.WithActiveInterval(10*time.Second),
		governance.WithActiveTimeout(2*time.Second),
		governance.WithHealthPathProvider(disc.HealthPath),
	)
	activeHC.Start(ctx)
	selector := dataplane.NewIngressSelector(disc, dataplane.WithHealthCheck(hc))
	certPool := dataplane.NewCertPool()
	gov := governance.NewGoverningRoundTripper(http.DefaultTransport,
		governance.WithCircuitBreaker(governance.DefaultBreaker()),
		governance.WithRateLimiter(governance.DefaultLimiter()),
		governance.WithRetry(governance.DefaultRetrier()),
		governance.WithPassiveHealthCheck(hc),
	)
	srv := dataplane.New(dataplane.Config{
		Addr:      addr,
		Selector:  selector,
		Transport: gov,
		// CertPool 留空：HTTP/h2c 模式。certPool 仍由 controller 更新（TLS 双端口在路线图）。
	})

	mgr, err := controller.NewManager(controller.ManagerOptions{
		WatchNamespace:   watchNS,
		IngressClass:     class,
		StatusHostname:   statusHost,
		StatusAddress:    statusAddr,
		MetricsAddr:      metricsAddr,
		EnableGatewayAPI: enableGatewayAPI,
		EnableHTTPProxy:  enableHTTPProxy,
		LeaderElection:   leaderElect,
	}, selector, disc, certPool)
	if err != nil {
		slog.Error("create controller manager failed", "err", err)
		return
	}

	go func() {
		slog.Info("hermes dataplane listening", "addr", addr, "tls", certPool != nil)
		if err := srv.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("dataplane stopped", "err", err)
		}
	}()
	go func() {
		slog.Info("controller starting", "ingress-class", class, "watch-namespace", watchNS,
			"gateway-api", enableGatewayAPI, "httpproxy", enableHTTPProxy, "leader-elect", leaderElect)
		if err := mgr.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("controller stopped", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// runDemo demo 模式：内嵌 echo 后端 + 硬编码路由，验证数据面链路（无需 K8s 集群）。
//
//	curl -H 'Host: foo.local' http://localhost:8080/
func runDemo(ctx context.Context, addr string) {
	const (
		backendAddr = "127.0.0.1:9090"
		echoService = "default/echo-svc"
	)

	echoSrv := &http.Server{
		Addr:              backendAddr,
		Handler:           http.HandlerFunc(echoHandler),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("echo backend listening", "addr", backendAddr)
		if err := echoSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("echo backend stopped", "err", err)
		}
	}()
	defer func() { _ = echoSrv.Shutdown(context.Background()) }()

	disc := discovery.NewStatic(&types.Instance{
		ID:       "echo-1",
		Name:     echoService,
		Cluster:  model.DefaultCluster,
		Protocol: "http",
		IP:       "127.0.0.1",
		Port:     9090,
	})

	selector := dataplane.NewIngressSelector(disc)
	selector.Update(hardcodedTable(echoService))

	srv := dataplane.New(dataplane.Config{
		Addr:     addr,
		Selector: selector,
	})

	go func() {
		slog.Info("hermes dataplane listening (demo)",
			"addr", addr, "hint", "curl -H 'Host: foo.local' "+addr+"/")
		if err := srv.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("dataplane stopped", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// hardcodedTable demo 硬编码路由：foo.local/* → echo-svc。
func hardcodedTable(echoService string) *model.RoutingTable {
	backend := &model.BackendRef{ServiceName: echoService, Port: 9090}
	return &model.RoutingTable{
		Version: 1,
		Hosts: map[string]*model.HostRules{
			"foo.local": {
				Host: "foo.local",
				Paths: []*model.PathRule{
					{PathType: model.PathTypePrefix, Path: "/", Backend: backend},
				},
			},
		},
		Default: backend,
	}
}

// echoHandler 模拟后端：回显请求信息，便于观察转发是否生效。
func echoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	slog.Info("backend received request", "path", r.URL.Path, "host", r.Host)
	_, _ = w.Write([]byte("hello from echo backend\n"))
	_, _ = w.Write([]byte("path=" + r.URL.Path + "\n"))
	_, _ = w.Write([]byte("host=" + r.Host + "\n"))
}
