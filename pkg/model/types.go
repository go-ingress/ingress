// Package model 定义 Hermes 内部路由模型。
//
// 控制面把 K8s Ingress / Gateway API 翻译为不可变的 RoutingTable，
// 数据面通过 atomic.Pointer 原子消费，实现无锁热更新。
package model

// DefaultCluster 默认 cluster 名（对齐 zeus routing.Default）。
// canary 流量映射为独立 cluster，主流量走 DefaultCluster。
const DefaultCluster = "default"

// PathType 路径匹配类型，对齐 K8s Ingress networking.k8s.io/v1 PathType。
type PathType string

const (
	PathTypeExact                  PathType = "Exact"
	PathTypePrefix                 PathType = "Prefix"
	PathTypeImplementationSpecific PathType = "ImplementationSpecific"
)

// BackendRef 后端引用（K8s Service）。
type BackendRef struct {
	ServiceName string // "namespace/name"
	Port        int    // Service 端口（阶段1用于匹配 Endpoints subset）
	Scheme      string // "http" | "https"，空默认 http
	Weight      int    // 多后端权重（Gateway API 流量拆分）
	LimitRPS    int    // per-service 限流（0=用全局默认，>0 按 service+cluster 独立令牌桶）
}

// CanaryConfig 金丝雀配置，兼容 nginx-ingress canary annotation 语义。
// 决策优先级：Header > Cookie > Weight（与 ingress-nginx 一致）。
type CanaryConfig struct {
	Weight  int         // 0-100，按百分比随机路由
	Header  string      // canary-by-header（值 always/never 或自定义 Value）
	Value   string      // canary-by-header-value
	Cookie  string      // canary-by-cookie（值为 true 时命中）
	Backend *BackendRef // 灰度后端
}

// RewriteConfig 路径重写（rewrite-target annotation）。
type RewriteConfig struct {
	ReplacePrefix string // 用该前缀替换匹配的 rule.Path 前缀
}

// HeaderMatch header 匹配规则（Gateway API，阶段4启用）。
type HeaderMatch struct {
	Name  string
	Value string
	Type  string // Exact | RegularExpression
}

// PathRule 单条路径规则。
type PathRule struct {
	PathType PathType
	Path     string
	Method   string        // 空表示任意方法（Gateway API method 匹配）
	Headers  []HeaderMatch // Gateway API header 匹配（阶段4）
	Backend  *BackendRef
	Canary   *CanaryConfig
	Rewrite  *RewriteConfig
	Timeout  int64 // 纳秒，0 表示不限
}

// HostRules 单 host 下的路径规则集合。
type HostRules struct {
	Host  string      // 精确 host 或 *.example.com
	Paths []*PathRule // 建议按 pathScore 降序预排
}

// RoutingTable 不可变路由表。
// 控制面每次 reconcile 生成新版本，通过 atomic.Pointer 推送给数据面。
type RoutingTable struct {
	Version   int64                 // 单调递增版本号
	Hosts     map[string]*HostRules // 精确 host -> rules
	Wildcards []*HostRules          // *.example.com 通配（按精度降序，见 SortWildcards）
	Default   *BackendRef           // 默认 backend（无 host/path 匹配时兜底）
}
