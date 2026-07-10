// Package v1alpha1 定义 Hermes 自定义资源（HTTPProxy 高级路由 CRD）。
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// HTTPProxy Hermes 高级路由 CRD（hermes.io/v1alpha1）。
//
// 补齐 Ingress v1 表达不了的能力：流量拆分（多 service + weight）、
// header 匹配、路径重写。翻译为 model.RoutingTable，数据面无感知差异。
type HTTPProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              HTTPProxySpec `json:"spec,omitempty"`
}

// HTTPProxySpec HTTPProxy 规格。
type HTTPProxySpec struct {
	Host   string           `json:"host,omitempty"`
	Routes []HTTPProxyRoute `json:"routes,omitempty"`
}

// HTTPProxyRoute 单条路由：conditions 匹配 + services 后端（多 service 流量拆分）。
type HTTPProxyRoute struct {
	Conditions []HTTPProxyCondition `json:"conditions,omitempty"`
	Services   []HTTPProxyService   `json:"services,omitempty"`
	Rewrite    *HTTPProxyRewrite    `json:"rewrite,omitempty"`
}

// HTTPProxyCondition 匹配条件：path（prefix 或 exact）或 header。
type HTTPProxyCondition struct {
	Path   string `json:"path,omitempty"`
	Prefix bool   `json:"prefix,omitempty"` // true=Prefix，false=Exact
	Header string `json:"header,omitempty"`
	Value  string `json:"value,omitempty"`
}

// HTTPProxyService 后端 Service 引用（含权重，用于流量拆分）。
type HTTPProxyService struct {
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Weight int    `json:"weight,omitempty"`
}

// HTTPProxyRewrite 路径重写。
type HTTPProxyRewrite struct {
	Prefix string `json:"prefix,omitempty"`
}

// HTTPProxyList HTTPProxy 列表。
type HTTPProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HTTPProxy `json:"items"`
}

// --- DeepCopy 方法（实现 runtime.Object）---

func (in *HTTPProxy) DeepCopyInto(out *HTTPProxy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

func (in *HTTPProxy) DeepCopy() *HTTPProxy {
	if in == nil {
		return nil
	}
	out := new(HTTPProxy)
	in.DeepCopyInto(out)
	return out
}

func (in *HTTPProxy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *HTTPProxySpec) DeepCopyInto(out *HTTPProxySpec) {
	*out = *in
	if in.Routes != nil {
		out.Routes = make([]HTTPProxyRoute, len(in.Routes))
		for i := range in.Routes {
			in.Routes[i].DeepCopyInto(&out.Routes[i])
		}
	}
}

func (in *HTTPProxyRoute) DeepCopyInto(out *HTTPProxyRoute) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = append([]HTTPProxyCondition(nil), in.Conditions...)
	}
	if in.Services != nil {
		out.Services = append([]HTTPProxyService(nil), in.Services...)
	}
	if in.Rewrite != nil {
		out.Rewrite = &HTTPProxyRewrite{Prefix: in.Rewrite.Prefix}
	}
}

func (in *HTTPProxyList) DeepCopyInto(out *HTTPProxyList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]HTTPProxy, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *HTTPProxyList) DeepCopy() *HTTPProxyList {
	if in == nil {
		return nil
	}
	out := new(HTTPProxyList)
	in.DeepCopyInto(out)
	return out
}

func (in *HTTPProxyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
