package clusters

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gitlab.com/ai-guard/kubernetes-mcp/internal/config"
)

// Registry is a thread-safe collection of the clusters this MCP instance manages.
type Registry struct {
	mu             sync.RWMutex
	clusters       map[string]*Cluster
	order          []string
	defaultCluster string
}

// Build constructs a Registry from config. Building a cluster's clients does not
// contact the API server, so an unreachable remote cluster does not fail Build;
// reachability is reported later via Cluster.Ping / the clusters_list tool.
func Build(cfg *config.Config) (*Registry, error) {
	r := &Registry{
		clusters:       make(map[string]*Cluster, len(cfg.Clusters)),
		defaultCluster: cfg.DefaultCluster,
	}
	for _, cc := range cfg.Clusters {
		cl, err := newCluster(cc)
		if err != nil {
			return nil, err
		}
		r.clusters[cc.Name] = cl
		r.order = append(r.order, cc.Name)
	}
	if _, ok := r.clusters[r.defaultCluster]; !ok {
		return nil, fmt.Errorf("defaultCluster %q not found after build", r.defaultCluster)
	}
	return r, nil
}

// NewRegistryForTest assembles a Registry from pre-built clusters (used with
// clusters.NewForTest in unit tests).
func NewRegistryForTest(defaultName string, cls ...*Cluster) *Registry {
	r := &Registry{clusters: make(map[string]*Cluster, len(cls)), defaultCluster: defaultName}
	for _, c := range cls {
		r.clusters[c.Name] = c
		r.order = append(r.order, c.Name)
	}
	return r
}

// Get returns the named cluster, or the default cluster when name is empty.
// It returns a descriptive error (listing valid names) on a miss, so the LLM
// gets an actionable message.
func (r *Registry) Get(name string) (*Cluster, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name == "" {
		name = r.defaultCluster
	}
	cl, ok := r.clusters[name]
	if !ok {
		return nil, fmt.Errorf("unknown cluster %q; configured clusters: %s", name, strings.Join(r.namesLocked(), ", "))
	}
	return cl, nil
}

// Default returns the default cluster.
func (r *Registry) Default() *Cluster {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clusters[r.defaultCluster]
}

// DefaultName returns the configured default cluster name.
func (r *Registry) DefaultName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultCluster
}

// Names returns the cluster names in configuration order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.namesLocked()
}

func (r *Registry) namesLocked() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// All returns every cluster sorted by name (stable output for clusters_list).
func (r *Registry) All() []*Cluster {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := r.namesLocked()
	sort.Strings(names)
	out := make([]*Cluster, 0, len(names))
	for _, n := range names {
		out = append(out, r.clusters[n])
	}
	return out
}
