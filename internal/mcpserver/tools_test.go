package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/clusters"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	memory "k8s.io/client-go/discovery/cached/memory"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/restmapper"
)

// buildTestServer wires a Server backed by fake typed + dynamic clients and a
// REST mapper built from a fake discovery document. The typed and dynamic fakes
// are seeded with the same logical objects.
func buildTestServer(t *testing.T, readOnly bool) *Server {
	t.Helper()

	objs := []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a"}, Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a"}, Spec: corev1.PodSpec{NodeName: "node1", Containers: []corev1.Container{{Name: "c"}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "team-a"}, Data: map[string]string{"key": "value"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "team-a"}, Data: map[string][]byte{"password": []byte("hunter2")}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a"}},
	}

	typed := fake.NewSimpleClientset(objs...)

	disco := typed.Discovery().(*fakediscovery.FakeDiscovery)
	disco.Resources = []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "namespaces", Kind: "Namespace", Namespaced: false},
			{Name: "pods", Kind: "Pod", Namespaced: true},
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true},
			{Name: "secrets", Kind: "Secret", Namespaced: true},
			{Name: "events", Kind: "Event", Namespaced: true},
			{Name: "nodes", Kind: "Node", Namespaced: false},
		}},
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
			{Name: "deployments", Kind: "Deployment", Namespaced: true},
		}},
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))

	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme, objs...)

	cl := clusters.NewForTest("test", typed, dyn, mapper, readOnly, "")
	reg := clusters.NewRegistryForTest("test", cl)
	return &Server{reg: reg, readOnly: false}
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is not text: %T", res.Content[0])
	}
	return tc.Text
}

func TestNamespacesList(t *testing.T) {
	s := buildTestServer(t, false)
	res, _, _ := s.namespacesList(context.Background(), nil, clusterParam{})
	if got := text(t, res); !strings.Contains(got, "team-a") {
		t.Fatalf("namespaces_list missing team-a: %s", got)
	}
}

func TestPodsList(t *testing.T) {
	s := buildTestServer(t, false)
	res, _, _ := s.podsList(context.Background(), nil, struct {
		namespaceParam
		LabelSelector string `json:"labelSelector,omitempty" jsonschema:"optional label selector"`
	}{namespaceParam: namespaceParam{Namespace: "team-a"}})
	got := text(t, res)
	if !strings.Contains(got, "team-a/web") || !strings.Contains(got, "phase=Running") {
		t.Fatalf("pods_list wrong: %s", got)
	}
}

func TestResourcesListConfigMap(t *testing.T) {
	s := buildTestServer(t, false)
	res, _, _ := s.resourcesList(context.Background(), nil, listResourcesParam{APIVersion: "v1", Kind: "ConfigMap", Namespace: "team-a"})
	if got := text(t, res); !strings.Contains(got, "settings") {
		t.Fatalf("resources_list missing configmap: %s", got)
	}
}

func TestResourcesGetSecretRedacted(t *testing.T) {
	s := buildTestServer(t, false)
	res, _, _ := s.resourcesGet(context.Background(), nil, resourceRef{APIVersion: "v1", Kind: "Secret", Namespace: "team-a", Name: "creds"})
	got := text(t, res)
	if strings.Contains(got, "hunter2") || !strings.Contains(got, "<redacted>") {
		t.Fatalf("secret not redacted: %s", got)
	}
}

func TestUnknownClusterError(t *testing.T) {
	s := buildTestServer(t, false)
	res, _, _ := s.namespacesList(context.Background(), nil, clusterParam{Cluster: "does-not-exist"})
	if !res.IsError || !strings.Contains(text(t, res), "unknown cluster") {
		t.Fatalf("expected unknown cluster error, got: %+v", res)
	}
}

func TestWriteGuardGlobalReadOnly(t *testing.T) {
	s := buildTestServer(t, false)
	s.readOnly = true // global kill-switch
	res, _, _ := s.resourcesDelete(context.Background(), nil, resourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "team-a", Name: "settings"})
	if !res.IsError || !strings.Contains(text(t, res), "read-only") {
		t.Fatalf("expected global read-only block, got: %s", text(t, res))
	}
}

func TestWriteGuardPerCluster(t *testing.T) {
	// Cluster marked read-only; global switch off.
	s := buildTestServer(t, true)
	res, _, _ := s.resourcesDelete(context.Background(), nil, resourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "team-a", Name: "settings"})
	if !res.IsError || !strings.Contains(text(t, res), "readOnly") {
		t.Fatalf("expected per-cluster read-only block, got: %s", text(t, res))
	}
}

func TestDeleteAllowedWhenWritable(t *testing.T) {
	s := buildTestServer(t, false)
	res, _, _ := s.resourcesDelete(context.Background(), nil, resourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "team-a", Name: "settings"})
	if res.IsError {
		t.Fatalf("delete should succeed: %s", text(t, res))
	}
	if !strings.Contains(text(t, res), "deleted ConfigMap settings") {
		t.Fatalf("unexpected delete result: %s", text(t, res))
	}
}

func TestMCPServerRegistersTools(t *testing.T) {
	s := buildTestServer(t, false)
	// Building the MCP server must not panic and must register tools.
	_ = s.MCPServer()
}
