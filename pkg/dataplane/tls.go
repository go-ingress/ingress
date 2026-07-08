package dataplane

import (
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"
)

// CertPool SNI 证书池，支持热更新（原子替换内部 map）。
//
// controller 通过 SetCert/DeleteCert 更新；tls.Config.GetCertificate 读取。
// 证书轮转（Secret 变更）时 controller 重新加载并 SetCert，下次 TLS 握手即生效，无需重启。
type CertPool struct {
	hosts       atomic.Pointer[map[string]*tls.Certificate] // host -> cert
	defaultCert atomic.Pointer[tls.Certificate]
	mu          sync.Mutex // 串行化 SetCert/DeleteCert 的读-改-写
}

// NewCertPool 创建空证书池。
func NewCertPool() *CertPool {
	empty := make(map[string]*tls.Certificate)
	cp := &CertPool{}
	cp.hosts.Store(&empty)
	return cp
}

// SetCert 设置某 host 的证书（覆盖）。
func (p *CertPool) SetCert(host string, cert *tls.Certificate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := *p.hosts.Load()
	next := make(map[string]*tls.Certificate, len(cur)+1)
	for k, v := range cur {
		next[k] = v
	}
	next[host] = cert
	p.hosts.Store(&next)
}

// DeleteCert 移除某 host 的证书。
func (p *CertPool) DeleteCert(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := *p.hosts.Load()
	next := make(map[string]*tls.Certificate, len(cur))
	for k, v := range cur {
		if k != host {
			next[k] = v
		}
	}
	p.hosts.Store(&next)
}

// SetDefault 设置默认证书（无 SNI 匹配时兜底）。
func (p *CertPool) SetDefault(cert *tls.Certificate) {
	p.defaultCert.Store(cert)
}

// GetCertificate 实现 tls.Config.GetCertificate，按 SNI 返回证书。
// 满足 controller.CertUpdater 间接使用；运行期由 tls 调用。
func (p *CertPool) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello != nil {
		if cert, ok := (*p.hosts.Load())[hello.ServerName]; ok {
			return cert, nil
		}
	}
	if def := p.defaultCert.Load(); def != nil {
		return def, nil
	}
	return nil, errors.New("dataplane: no tls certificate for host " + nonNilServerName(hello))
}

func nonNilServerName(hello *tls.ClientHelloInfo) string {
	if hello == nil {
		return "<nil>"
	}
	return hello.ServerName
}
