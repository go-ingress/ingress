package governance

import "context"

// backendCtxKey 后端信息 context key。
type backendCtxKey struct{}

// BackendInfo 后端标识（service + cluster），用于治理按维度隔离。
// 由 IngressSelector.Pick 注入，GoverningRoundTripper 提取。
type BackendInfo struct {
	Service  string
	Cluster  string
	LimitRPS int // per-service 限流（0=用全局默认，>0 触发独立令牌桶）
}

// WithBackend 注入后端信息到 context。
func WithBackend(ctx context.Context, b BackendInfo) context.Context {
	return context.WithValue(ctx, backendCtxKey{}, b)
}

// BackendFromContext 提取后端信息。
func BackendFromContext(ctx context.Context) (BackendInfo, bool) {
	b, ok := ctx.Value(backendCtxKey{}).(BackendInfo)
	return b, ok
}

// Key 治理隔离 key（service#cluster）。
func (b BackendInfo) Key() string {
	if b.Service == "" {
		return ""
	}
	if b.Cluster == "" {
		return b.Service + "#default"
	}
	return b.Service + "#" + b.Cluster
}
