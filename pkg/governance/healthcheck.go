package governance

import (
	"net/http"
	"sync"
	"time"
)

// PassiveHealthCheck 被动健康检查（outlier detection 风格）。
//
// 记录每个实例（ip:port）的连续失败数，达到阈值标记不健康（ ejected ），
// 经过 ejectTime 后半开（允许探测请求）；成功则恢复。
// IngressSelector.Pick 时调用 IsHealthy 过滤不健康实例。
type PassiveHealthCheck struct {
	mu        sync.RWMutex
	failures  map[string]int       // instanceKey -> 连续失败数
	unhealthy map[string]time.Time // instanceKey -> 标记不健康的时刻
	threshold int
	ejectTime time.Duration
}

// NewPassiveHealthCheck 创建被动健康检查。
// threshold：连续失败次数阈值；ejectTime：不健康实例的驱逐时长（半开恢复）。
func NewPassiveHealthCheck(threshold int, ejectTime time.Duration) *PassiveHealthCheck {
	if threshold <= 0 {
		threshold = 5
	}
	if ejectTime <= 0 {
		ejectTime = 30 * time.Second
	}
	return &PassiveHealthCheck{
		failures:  make(map[string]int),
		unhealthy: make(map[string]time.Time),
		threshold: threshold,
		ejectTime: ejectTime,
	}
}

// IsHealthy 判断实例是否健康。
// 未被标记不健康返回 true；被标记但已过 ejectTime（半开）也返回 true。
func (p *PassiveHealthCheck) IsHealthy(instanceKey string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, ok := p.unhealthy[instanceKey]
	if !ok {
		return true
	}
	return time.Since(t) > p.ejectTime
}

// Report 上报请求结果。
// 5xx 或错误计失败，连续达阈值则标记不健康；2xx/3xx/4xx 成功则清零恢复。
func (p *PassiveHealthCheck) Report(instanceKey string, resp *http.Response, err error) {
	failed := err != nil || (resp != nil && resp.StatusCode >= 500)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !failed {
		delete(p.failures, instanceKey)
		delete(p.unhealthy, instanceKey)
		return
	}
	p.failures[instanceKey]++
	if p.failures[instanceKey] >= p.threshold {
		p.unhealthy[instanceKey] = time.Now()
	}
}

// ReportHealth 直接上报健康状态（主动健康检查用，不依赖 HTTP 响应语义）。
func (p *PassiveHealthCheck) ReportHealth(instanceKey string, healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if healthy {
		delete(p.failures, instanceKey)
		delete(p.unhealthy, instanceKey)
		return
	}
	p.failures[instanceKey]++
	if p.failures[instanceKey] >= p.threshold {
		p.unhealthy[instanceKey] = time.Now()
	}
}
