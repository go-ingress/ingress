package governance

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-zeus/zeus/registry"
	ztypes "github.com/go-zeus/zeus/types"
)

// ActiveHealthCheck 主动健康检查：周期 HTTP 探测后端实例，结果上报 PassiveHealthCheck。
//
// 与被动健康检查互补：被动依赖真实流量失败降权，主动在无流量时也能剔除不健康实例
//（补齐 K8s readiness 滞后场景）。探测路径 2xx 视为健康，其他视为不健康。
//
// opt-in 设计：仅探测 healthPath provider 返回非空路径的 service（由 Service 注解
// active-health-check-path 声明）。未声明健康路径的 service 不被探测，避免对未实现
// /health 端点的后端（如静态站点、未改造的业务服务）误判剔除导致 503。
type ActiveHealthCheck struct {
	discovery  registry.Discovery
	passive    *PassiveHealthCheck
	services   func() []string       // 返回所有 service key
	healthPath func(string) string   // serviceKey -> 探测路径，空=该 service 不探测
	interval   time.Duration
	timeout    time.Duration
	client     *http.Client
}

// ActiveOption 主动健康检查配置项。
type ActiveOption func(*ActiveHealthCheck)

// WithHealthPathProvider 设置健康路径来源（通常传 K8sDiscovery.HealthPath）。
// 未设置 provider 时不进行任何主动探测（安全降级，避免误剔除）。
func WithHealthPathProvider(fn func(serviceKey string) string) ActiveOption {
	return func(a *ActiveHealthCheck) { a.healthPath = fn }
}

// WithActiveInterval 设置探测间隔（默认 10s）。
func WithActiveInterval(d time.Duration) ActiveOption {
	return func(a *ActiveHealthCheck) { a.interval = d }
}

// WithActiveTimeout 设置探测超时（默认 2s）。
func WithActiveTimeout(d time.Duration) ActiveOption {
	return func(a *ActiveHealthCheck) { a.timeout = d }
}

// NewActiveHealthCheck 创建主动健康检查。
// services 返回所有 service key（通常 K8sDiscovery.Services）；disc 用于 GetService 获取实例。
func NewActiveHealthCheck(passive *PassiveHealthCheck, services func() []string, disc registry.Discovery, opts ...ActiveOption) *ActiveHealthCheck {
	a := &ActiveHealthCheck{
		discovery: disc,
		passive:   passive,
		services:  services,
		interval:  10 * time.Second,
		timeout:   2 * time.Second,
	}
	for _, o := range opts {
		o(a)
	}
	a.client = &http.Client{Timeout: a.timeout}
	return a
}

// Start 启动周期探测（非阻塞，goroutine 随 ctx 退出）。
func (a *ActiveHealthCheck) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		a.probeAll(ctx) // 立即首次探测
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.probeAll(ctx)
			}
		}
	}()
}

// probeAll 探测所有声明了健康路径的 service 的全部实例。
// 无 healthPath provider 或 service 未声明路径时跳过（opt-in，避免误剔除）。
func (a *ActiveHealthCheck) probeAll(ctx context.Context) {
	if a.services == nil || a.healthPath == nil {
		return // 未提供健康路径来源，不主动探测
	}
	for _, svc := range a.services() {
		path := a.healthPath(svc)
		if path == "" {
			continue // 该 service 未声明健康检查路径，跳过
		}
		entry, err := a.discovery.GetService(ctx, svc)
		if err != nil || entry == nil {
			continue
		}
		for _, ins := range entry.Instances {
			a.probe(ctx, ins, path)
		}
	}
}

// probe 探测单个实例，2xx 上报健康，其他上报不健康。
func (a *ActiveHealthCheck) probe(ctx context.Context, ins *ztypes.Instance, path string) {
	key := net.JoinHostPort(ins.IP, strconv.Itoa(ins.Port))
	target := "http://" + key + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		a.passive.ReportHealth(key, false)
		return
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.passive.ReportHealth(key, false)
		return
	}
	_ = resp.Body.Close()
	a.passive.ReportHealth(key, resp.StatusCode >= 200 && resp.StatusCode < 300)
}
