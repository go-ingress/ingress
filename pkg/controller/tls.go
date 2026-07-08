package controller

import (
	"context"
	"crypto/tls"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// secretTLSLoader 从 K8s Secret（类型 kubernetes.io/tls）加载 TLS 证书。
type secretTLSLoader struct {
	client.Client
}

// NewSecretTLSLoader 创建基于 Secret 的 TLS 加载器。
func NewSecretTLSLoader(c client.Client) TLSLoader {
	return &secretTLSLoader{Client: c}
}

// Load 从指定 Secret 加载 tls.crt + tls.key，返回 tls.Certificate。
func (l *secretTLSLoader) Load(ctx context.Context, namespace, name string) (*tls.Certificate, error) {
	sec := &corev1.Secret{}
	if err := l.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, sec); err != nil {
		return nil, err
	}
	certPEM, ok1 := sec.Data[corev1.TLSCertKey]
	keyPEM, ok2 := sec.Data[corev1.TLSPrivateKeyKey]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("controller: secret %s/%s missing tls.crt or tls.key", namespace, name)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("controller: parse secret %s/%s: %w", namespace, name, err)
	}
	return &cert, nil
}
