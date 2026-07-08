// Package discovery 提供 Hermes 的服务发现适配器。
//
// 阶段 0：StaticDiscovery（硬编码实例，验证数据面链路）。
// 阶段 1：K8sDiscovery（Endpoints/EndpointSlice Informer → zeus registry.Discovery），
//         把 K8s Endpoints 的 Ready Address 映射为 zeus types.Instance。
package discovery

import (
	"context"
	"sync"

	"github.com/go-zeus/zeus/registry"
	"github.com/go-zeus/zeus/types"
)

// 编译期检查 StaticDiscovery 实现 registry.Discovery。
var _ registry.Discovery = (*StaticDiscovery)(nil)

// StaticDiscovery 静态服务发现，按 serviceName 索引预置实例。
type StaticDiscovery struct {
	mu       sync.RWMutex
	services map[string]*types.ServiceEntry
}

// NewStatic 创建静态发现，instances 按 Name 字段归类到 ServiceEntry。
func NewStatic(instances ...*types.Instance) *StaticDiscovery {
	d := &StaticDiscovery{services: make(map[string]*types.ServiceEntry)}
	for _, ins := range instances {
		d.register(ins)
	}
	return d
}

// Register 动态注册实例（阶段 0 测试用；阶段 1 由 informer 事件驱动 K8sDiscovery）。
func (d *StaticDiscovery) Register(ins *types.Instance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.register(ins)
}

func (d *StaticDiscovery) register(ins *types.Instance) {
	if ins == nil {
		return
	}
	entry, ok := d.services[ins.Name]
	if !ok {
		entry = types.NewServiceEntry()
		d.services[ins.Name] = entry
	}
	_ = entry.AddInstance(ins)
}

// GetService 实现 registry.Discovery。
func (d *StaticDiscovery) GetService(_ context.Context, name string) (*types.ServiceEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.services[name], nil
}
