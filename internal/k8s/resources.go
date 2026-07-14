// Package k8s provides generic, GVK-driven access to Kubernetes resources via
// the dynamic client and a REST mapper, so the MCP tools work uniformly for
// built-in types and CRDs alike.
package k8s

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// FieldManager is used for server-side apply so ownership is attributable.
const FieldManager = "kubernetes-mcp"

// ResettableRESTMapper is a REST mapper whose discovery cache can be cleared to
// pick up newly installed CRDs. *restmapper.DeferredDiscoveryRESTMapper (used in
// production) and test fakes both satisfy it.
type ResettableRESTMapper interface {
	meta.RESTMapper
	Reset()
}

// ResolveMapping maps apiVersion+kind to a REST mapping (GVR + scope). On a
// NoMatch (e.g. a CRD installed after the mapper cached), it resets the mapper
// once and retries so newly registered types are picked up.
func ResolveMapping(mapper ResettableRESTMapper, apiVersion, kind string) (*meta.RESTMapping, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}
	gk := schema.GroupKind{Group: gv.Group, Kind: kind}

	m, err := mapper.RESTMapping(gk, gv.Version)
	if err != nil {
		mapper.Reset()
		m, err = mapper.RESTMapping(gk, gv.Version)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve %s/%s: %w", apiVersion, kind, err)
		}
	}
	return m, nil
}

// resourceClient returns the dynamic resource interface scoped correctly for the
// mapping (namespaced vs cluster-scoped).
func resourceClient(dyn dynamic.Interface, m *meta.RESTMapping, namespace string) dynamic.ResourceInterface {
	if m.Scope.Name() == meta.RESTScopeNameNamespace {
		return dyn.Resource(m.Resource).Namespace(namespace)
	}
	return dyn.Resource(m.Resource)
}

// List lists objects of the given type. An empty namespace lists across all
// namespaces (for namespaced types).
func List(ctx context.Context, dyn dynamic.Interface, m *meta.RESTMapping, namespace string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return resourceClient(dyn, m, namespace).List(ctx, opts)
}

// Get fetches a single object.
func Get(ctx context.Context, dyn dynamic.Interface, m *meta.RESTMapping, namespace, name string) (*unstructured.Unstructured, error) {
	return resourceClient(dyn, m, namespace).Get(ctx, name, metav1.GetOptions{})
}

// Delete removes a single object.
func Delete(ctx context.Context, dyn dynamic.Interface, m *meta.RESTMapping, namespace, name string) error {
	return resourceClient(dyn, m, namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// Apply performs a server-side apply of a YAML or JSON manifest and returns the
// resulting object. The manifest must carry apiVersion, kind and metadata.name.
func Apply(ctx context.Context, dyn dynamic.Interface, mapper ResettableRESTMapper, manifest []byte, defaultNamespace string) (*unstructured.Unstructured, error) {
	jsonBytes, err := yaml.YAMLToJSON(manifest)
	if err != nil {
		return nil, fmt.Errorf("manifest is not valid YAML/JSON: %w", err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonBytes); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if obj.GetAPIVersion() == "" || obj.GetKind() == "" {
		return nil, fmt.Errorf("manifest must set apiVersion and kind")
	}
	if obj.GetName() == "" {
		return nil, fmt.Errorf("manifest must set metadata.name")
	}

	m, err := ResolveMapping(mapper, obj.GetAPIVersion(), obj.GetKind())
	if err != nil {
		return nil, err
	}
	ns := obj.GetNamespace()
	if ns == "" && m.Scope.Name() == meta.RESTScopeNameNamespace {
		ns = defaultNamespace
		obj.SetNamespace(ns)
	}

	return resourceClient(dyn, m, ns).Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{
		FieldManager: FieldManager,
		Force:        true,
	})
}

// Patch applies a strategic-merge / merge patch to a single object (used e.g. by
// rollout restart and scale).
func Patch(ctx context.Context, dyn dynamic.Interface, m *meta.RESTMapping, namespace, name string, pt types.PatchType, data []byte) (*unstructured.Unstructured, error) {
	return resourceClient(dyn, m, namespace).Patch(ctx, name, pt, data, metav1.PatchOptions{FieldManager: FieldManager})
}
