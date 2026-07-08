// Package dataplane 实现 Hermes 数据面：7 层路由匹配 + zeus proxy 转发。
//
// IngressSelector 实现 zeus proxy.Selector 接口，补齐 zeus 缺失的 host/path/canary 匹配：
//   1. RoutingTable.Match 做 host/path/method 匹配
//   2. canary 决策（header/cookie/weight）
//   3. registry.Discovery 查找后端实例
//   4. 按 cluster + port 筛选实例（被动健康检查过滤）
//   5. balancer.Balancer 选取实例 → *url.URL
//   6. 注入 backend 信息到 context，供 GoverningRoundTripper 按 service+cluster 隔离治理
//
// 同时 *IngressSelector 满足 controller.TableSink（Update 方法），控制面直接推送路由表。
package dataplane

import (
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-zeus/zeus/balancer"
	"github.com/go-zeus/zeus/balancer/roundrobin"
	"github.com/go-zeus/zeus/proxy"
	"github.com/go-zeus/zeus/registry"
	"github.com/go-zeus/zeus/types"

	"github.com/go-ingress/ingress/pkg/governance"
	"github.com/go-ingress/ingress/pkg/model"
)

// 编译期检查 IngressSelector 实现 proxy.Selector。
var _ proxy.Selector = (*IngressSelector)(nil)

var (
	// ErrNoTable 路由表未加载（控制面尚未推送）。
	ErrNoTable = errors.New("ingress: routing table not loaded")
	// ErrNotFound 无匹配路由（host/path 均未命中且无默认 backend）。
	ErrNotFound = errors.New("ingress: no matching route")
	// ErrUnavailable 后端无可用实例（服务发现返回空或全不健康）。
	ErrUnavailable = errors.New("ingress: no available backend")
)

// LBFactory 负载均衡器工厂，默认 roundrobin.New。
type LBFactory func() balancer.Balancer

// SelectorOption IngressSelector 配置项。
type SelectorOption func(*IngressSelector)

// WithBalancer 指定负载均衡器工厂（默认 roundrobin.New）。
func WithBalancer(f LBFactory) SelectorOption {
	return func(s *IngressSelector) { s.lbFactory = f }
}

// WithHealthCheck 注入被动健康检查（过滤不健康实例）。
func WithHealthCheck(hc *governance.PassiveHealthCheck) SelectorOption {
	return func(s *IngressSelector) { s.hc = hc }
}

// IngressSelector 实现 zeus proxy.Selector。
type IngressSelector struct {
	table     atomic.Pointer[model.RoutingTable]
	disc      registry.Discovery
	lbFactory LBFactory
	hc        *governance.PassiveHealthCheck // 可选，被动健康检查
	lbs       sync.Map                        // serviceName -> *lbEntry
}

// lbEntry 缓存的负载均衡器，sig 为生成时的实例签名（变化时 Reload）。
type lbEntry struct {
	sig string
	lb  balancer.Balancer
}

// NewIngressSelector 创建 IngressSelector。
func NewIngressSelector(disc registry.Discovery, opts ...SelectorOption) *IngressSelector {
	s := &IngressSelector{disc: disc, lbFactory: roundrobin.New}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Update 原子替换路由表（控制面调用，数据面无锁读取）。满足 controller.TableSink。
func (s *IngressSelector) Update(t *model.RoutingTable) {
	s.table.Store(t)
}

// Pick 实现 proxy.Selector。
func (s *IngressSelector) Pick(r *http.Request) (*url.URL, error) {
	t := s.table.Load()
	if t == nil {
		return nil, ErrNoTable
	}
	rule, ok := t.Match(r.Host, r.URL.Path, r.Method, r.Header)
	if !ok {
		if t.Default != nil {
			return s.pickBackend(r, t.Default)
		}
		return nil, ErrNotFound
	}
	backend := rule.Backend
	if rule.Canary != nil && decideCanary(r, rule.Canary) {
		backend = rule.Canary.Backend
	}
	if rule.Rewrite != nil {
		r.URL.Path = applyRewrite(rule.Path, r.URL.Path, rule.Rewrite)
		r.URL.RawPath = ""
	}
	return s.pickBackend(r, backend)
}

// pickBackend 服务发现 + 负载均衡，构造后端 URL。
func (s *IngressSelector) pickBackend(r *http.Request, b *model.BackendRef) (*url.URL, error) {
	if b == nil {
		return nil, ErrUnavailable
	}
	entry, err := s.disc.GetService(r.Context(), b.ServiceName)
	if err != nil {
		return nil, err
	}
	if entry == nil || len(entry.Instances) == 0 {
		return nil, ErrUnavailable
	}
	// 按 cluster + port 筛选；port 不匹配时回退到仅按 cluster。
	instances := pickClusterPort(entry, model.DefaultCluster, b.Port)
	if len(instances) == 0 {
		instances = pickCluster(entry, model.DefaultCluster)
	}
	if len(instances) == 0 {
		instances = allInstances(entry)
	}
	// 被动健康检查过滤。
	if s.hc != nil {
		instances = filterHealthy(s.hc, instances)
	}
	if len(instances) == 0 {
		return nil, ErrUnavailable
	}
	lb := s.getOrCreateLB(b.ServiceName, instances)
	ins, err := lb.Next()
	if err != nil {
		return nil, err
	}
	scheme := b.Scheme
	if scheme == "" {
		scheme = "http"
	}
	target := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(ins.IP, strconv.Itoa(ins.Port)),
	}
	// 注入 backend 信息到 context，供 GoverningRoundTripper 按 service+cluster 隔离治理。
	ctx := governance.WithBackend(r.Context(), governance.BackendInfo{
		Service:  b.ServiceName,
		Cluster:  model.DefaultCluster,
		LimitRPS: b.LimitRPS,
	})
	*r = *r.WithContext(ctx)
	return target, nil
}

// filterHealthy 过滤掉被被动健康检查标记为不健康的实例。
func filterHealthy(hc *governance.PassiveHealthCheck, instances []*types.Instance) []*types.Instance {
	var out []*types.Instance
	for _, ins := range instances {
		if hc.IsHealthy(net.JoinHostPort(ins.IP, strconv.Itoa(ins.Port))) {
			out = append(out, ins)
		}
	}
	return out
}

// getOrCreateLB 按 serviceName 缓存 balancer，实例签名变化时 Reload。
func (s *IngressSelector) getOrCreateLB(svc string, instances []*types.Instance) balancer.Balancer {
	sig := signature(instances)
	if v, ok := s.lbs.Load(svc); ok {
		e := v.(*lbEntry)
		if e.sig == sig {
			return e.lb
		}
		newLB := e.lb.Reload(instances)
		if s.lbs.CompareAndSwap(svc, e, &lbEntry{sig: sig, lb: newLB}) {
			return newLB
		}
		v, _ = s.lbs.Load(svc)
		return v.(*lbEntry).lb
	}
	lb := s.lbFactory().Reload(instances)
	actual, _ := s.lbs.LoadOrStore(svc, &lbEntry{sig: sig, lb: lb})
	return actual.(*lbEntry).lb
}

func signature(instances []*types.Instance) string {
	ids := make([]string, 0, len(instances))
	for _, ins := range instances {
		ids = append(ids, ins.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func pickClusterPort(entry *types.ServiceEntry, cluster string, port int) []*types.Instance {
	var out []*types.Instance
	for _, ins := range entry.Instances {
		if ins.Cluster != cluster {
			continue
		}
		if port > 0 && ins.Port != port {
			continue
		}
		out = append(out, ins)
	}
	return out
}

func pickCluster(entry *types.ServiceEntry, cluster string) []*types.Instance {
	var out []*types.Instance
	for _, ins := range entry.Instances {
		if ins.Cluster == cluster {
			out = append(out, ins)
		}
	}
	return out
}

func allInstances(entry *types.ServiceEntry) []*types.Instance {
	out := make([]*types.Instance, 0, len(entry.Instances))
	for _, ins := range entry.Instances {
		out = append(out, ins)
	}
	return out
}

// decideCanary 金丝雀决策，优先级：Header > Cookie > Weight（对齐 ingress-nginx）。
func decideCanary(r *http.Request, c *model.CanaryConfig) bool {
	if c == nil || c.Backend == nil {
		return false
	}
	if c.Header != "" {
		switch v := r.Header.Get(c.Header); {
		case v == "always":
			return true
		case v == "never":
			return false
		case c.Value != "" && v == c.Value:
			return true
		}
	}
	if c.Cookie != "" {
		if ck, err := r.Cookie(c.Cookie); err == nil && ck.Value == "true" {
			return true
		}
	}
	if c.Weight > 0 {
		return rand.IntN(100) < c.Weight
	}
	return false
}

// applyRewrite 路径重写：把匹配的 rule.Path 前缀替换为 ReplacePrefix。
func applyRewrite(rulePath, reqPath string, rw *model.RewriteConfig) string {
	if rw == nil || rw.ReplacePrefix == "" {
		return reqPath
	}
	if !strings.HasPrefix(reqPath, rulePath) {
		return reqPath
	}
	return rw.ReplacePrefix + strings.TrimPrefix(reqPath, rulePath)
}
