package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-ingress/ingress/pkg/model"
	ztypes "github.com/go-zeus/zeus/types"
)

// EndpointsToInstances 把 K8s Endpoints 转为 zeus Instance。
//
// 只取 Ready 的 Addresses（subsets.Addresses，不含 NotReadyAddresses）。
// 每个 subset × address × port 产生一个 Instance（多端口 Service 产生多个 Instance，
// IngressSelector 按 BackendRef.Port 筛选）。
//
// Instance.Name = "namespace/name"（Endpoints 名 = Service 名），与 BackendRef.ServiceName 对齐。
// Instance.Cluster = DefaultCluster（canary 流量在阶段3映射为独立 cluster）。
func EndpointsToInstances(ep *corev1.Endpoints) []*ztypes.Instance {
	if ep == nil {
		return nil
	}
	var out []*ztypes.Instance
	serviceKey := ep.Namespace + "/" + ep.Name
	for _, sub := range ep.Subsets {
		for _, addr := range sub.Addresses {
			for _, port := range sub.Ports {
				out = append(out, &ztypes.Instance{
					ID:       fmt.Sprintf("%s/%s/%s:%d", ep.Namespace, ep.Name, addr.IP, port.Port),
					Name:     serviceKey,
					Cluster:  model.DefaultCluster,
					Protocol: portName(port.Name),
					IP:       addr.IP,
					Port:     int(port.Port),
				})
			}
		}
	}
	return out
}

// portName 端口名转协议标识（空时默认 http）。
func portName(name string) string {
	if name == "" {
		return "http"
	}
	return name
}
