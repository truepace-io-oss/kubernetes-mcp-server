package clusters

import (
	"context"
	"fmt"

	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/k8s"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// Cluster holds the ready-to-use clients for a single Kubernetes cluster.
// The clients are goroutine-safe and pool connections, so a Cluster is built
// once and shared across all concurrent tool invocations.
type Cluster struct {
	Name             string
	ReadOnly         bool
	DefaultNamespace string

	RESTConfig *rest.Config
	Typed      kubernetes.Interface
	Dynamic    dynamic.Interface
	Discovery  discovery.DiscoveryInterface

	// mapper resolves apiVersion+kind to a REST mapping. It is cached and can be
	// reset to pick up freshly installed CRDs.
	mapper k8s.ResettableRESTMapper
}

// newCluster constructs all clients for one cluster from its config.
func newCluster(c config.ClusterConfig) (*Cluster, error) {
	rc, err := restConfigFor(c)
	if err != nil {
		return nil, err
	}
	typed, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("cluster %q: build typed client: %w", c.Name, err)
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("cluster %q: build dynamic client: %w", c.Name, err)
	}
	disco, err := discovery.NewDiscoveryClientForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("cluster %q: build discovery client: %w", c.Name, err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))

	return &Cluster{
		Name:             c.Name,
		ReadOnly:         c.ReadOnly,
		DefaultNamespace: c.DefaultNamespace,
		RESTConfig:       rc,
		Typed:            typed,
		Dynamic:          dyn,
		Discovery:        disco,
		mapper:           mapper,
	}, nil
}

// Mapper returns the REST mapper for GVK→GVR resolution.
func (c *Cluster) Mapper() k8s.ResettableRESTMapper { return c.mapper }

// ResetMapper clears the discovery cache so a subsequent mapping picks up newly
// installed CRDs / API groups.
func (c *Cluster) ResetMapper() { c.mapper.Reset() }

// NewForTest builds a Cluster from already-constructed (usually fake) clients.
// It exists so unit tests can inject fakes without contacting an API server.
func NewForTest(name string, typed kubernetes.Interface, dyn dynamic.Interface, mapper k8s.ResettableRESTMapper, readOnly bool, defaultNamespace string) *Cluster {
	return &Cluster{
		Name:             name,
		ReadOnly:         readOnly,
		DefaultNamespace: defaultNamespace,
		Typed:            typed,
		Dynamic:          dyn,
		mapper:           mapper,
	}
}

// Ping reports reachability and the server version. It is used for the
// clusters_list tool and must never be fatal at startup (a cluster being down
// must not crash the server).
func (c *Cluster) Ping(ctx context.Context) (string, error) {
	// ServerVersion doesn't take a context in client-go; the surrounding tool
	// call carries the deadline via the HTTP client transport.
	v, err := c.Discovery.ServerVersion()
	if err != nil {
		return "", err
	}
	return v.GitVersion, nil
}
