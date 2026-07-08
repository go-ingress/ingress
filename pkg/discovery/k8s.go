package discovery

import (
	"context"
	"sync"

	"github.com/go-zeus/zeus/registry"
	ztypes "github.com/go-zeus/zeus/types"
)

// 编译期检查 K8sDiscovery 实现 registry.Discovery。
var _ registry.Discovery = (*K8sDiscovery)(nil)

// K8sDiscovery 把 K8s Endpoints/EndpointSlice 暴露为 zeus registry.Discovery。
//
// controller 通过 SetService 全量刷新某 service 的实例集合（由 Endpoints informer 事件驱动），
// 数据面 IngressSelector 通过 GetService 无锁读取。两者经 map+RWMutex 解耦，无 IPC。
type K8sDiscovery struct {
	mu          sync.RWMutex
	services    map[string]*ztypes.ServiceEntry // serviceKey(ns/name) -> ServiceEntry
	healthPaths map[string]string               // serviceKey -> 主动健康检查探测路径（opt-in）
}

// NewK8sDiscovery 创建 K8s 服务发现。
func NewK8sDiscovery() *K8sDiscovery {
	return &K8sDiscovery{
		services:    make(map[string]*ztypes.ServiceEntry),
		healthPaths: make(map[string]string),
	}
}

// SetService 用给定实例全量替换某 service 的实例集合。
// 由 controller 在 Endpoints 变更时调用；instances 为空则移除该 service。
func (d *K8sDiscovery) SetService(serviceKey string, instances []*ztypes.Instance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(instances) == 0 {
		delete(d.services, serviceKey)
		return
	}
	entry := ztypes.NewServiceEntry()
	for _, ins := range instances {
		_ = entry.AddInstance(ins)
	}
	d.services[serviceKey] = entry
}

// DeleteService 移除某 service（Endpoints 删除时调用）。
func (d *K8sDiscovery) DeleteService(serviceKey string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.services, serviceKey)
}

// GetService 实现 registry.Discovery。
// 未找到返回 (nil, nil)，由 IngressSelector 映射为 ErrUnavailable（503）。
func (d *K8sDiscovery) GetService(_ context.Context, serviceKey string) (*ztypes.ServiceEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.services[serviceKey], nil
}

// Services 返回当前所有 service key（供主动健康检查遍历）。
func (d *K8sDiscovery) Services() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	keys := make([]string, 0, len(d.services))
	for k := range d.services {
		keys = append(keys, k)
	}
	return keys
}

// SetHealthPath 设置某 service 的主动健康检查探测路径（path 空则清除）。
// 由 controller 从 Service 注解放 hermes.ingress.kubernetes.io/active-health-check-path 解析后调用。
func (d *K8sDiscovery) SetHealthPath(serviceKey, path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if path == "" {
		delete(d.healthPaths, serviceKey)
		return
	}
	d.healthPaths[serviceKey] = path
}

// ResetHealthPaths 清空所有主动健康检查路径（每轮 Reconcile 前调用，避免残留）。
func (d *K8sDiscovery) ResetHealthPaths() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.healthPaths = make(map[string]string)
}

// HealthPath 返回某 service 的主动健康检查探测路径，未声明返回空串。
// ActiveHealthCheck 仅对返回非空的 service 探测，避免误剔除未实现健康端点的后端。
func (d *K8sDiscovery) HealthPath(serviceKey string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.healthPaths[serviceKey]
}
