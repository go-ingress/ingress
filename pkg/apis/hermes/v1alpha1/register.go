package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion HTTPProxy CRD 的 GroupVersion。
var SchemeGroupVersion = schema.GroupVersion{Group: "hermes.io", Version: "v1alpha1"}

// SchemeBuilder scheme 构建器。
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme 注册 HTTPProxy 类型到 scheme。
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &HTTPProxy{}, &HTTPProxyList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
