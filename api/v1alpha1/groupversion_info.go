package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group   = "mirror.maude.dev"
	Version = "v1alpha1"
)

var GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

var SchemeBuilder = runtime.NewSchemeBuilder(func(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &GitMirror{}, &GitMirrorList{})
	return nil
})

var AddToScheme = SchemeBuilder.AddToScheme
