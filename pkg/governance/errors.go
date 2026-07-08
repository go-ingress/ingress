package governance

import "errors"

// 治理错误，dataplane 映射为对应 HTTP 状态码。
var (
	// ErrRateLimited 限流拒绝（429）。
	ErrRateLimited = errors.New("governance: rate limited")
	// ErrCircuitOpen 熔断器打开（503）。
	ErrCircuitOpen = errors.New("governance: circuit breaker open")
)
