package clusters

import (
	"encoding/base64"
	"fmt"

	"gitlab.com/ai-guard/kubernetes-mcp/internal/config"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// restConfigFor builds a *rest.Config for one cluster purely from ServiceAccount
// credentials. No custom authentication is performed: the resulting client sends
// a bearer token and the Kubernetes API server + RBAC authorize every request.
//
// File-based forms (BearerTokenFile / CAFile) are preferred because client-go
// re-reads them on each use, so rotating projected / ESO-managed tokens are
// picked up without restarting the process.
func restConfigFor(c config.ClusterConfig) (*rest.Config, error) {
	if _, err := c.Mode(); err != nil {
		return nil, err
	}

	var (
		cfg *rest.Config
		err error
	)
	switch {
	case c.InCluster:
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("cluster %q: in-cluster config: %w", c.Name, err)
		}

	case c.KubeconfigFile != "":
		loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: c.KubeconfigFile}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: c.Context}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("cluster %q: kubeconfig context %q: %w", c.Name, c.Context, err)
		}

	default: // explicit remote: server + CA + token
		cfg = &rest.Config{Host: c.Server}
		switch { // token: file wins over inline (rotation-friendly)
		case c.TokenFile != "":
			cfg.BearerTokenFile = c.TokenFile
		case c.Token != "":
			cfg.BearerToken = c.Token
		}
		switch { // CA: file wins over inline base64 data
		case c.CertificateAuthorityFile != "":
			cfg.TLSClientConfig.CAFile = c.CertificateAuthorityFile
		case c.CertificateAuthorityData != "":
			caData, derr := base64.StdEncoding.DecodeString(c.CertificateAuthorityData)
			if derr != nil {
				return nil, fmt.Errorf("cluster %q: certificateAuthorityData is not valid base64: %w", c.Name, derr)
			}
			cfg.TLSClientConfig.CAData = caData
		}
		if c.InsecureSkipTLSVerify {
			cfg.TLSClientConfig.Insecure = true
		}
	}

	// Reasonable client-side throughput; RBAC/apiserver remain the real limit.
	cfg.QPS = 50
	cfg.Burst = 100
	if cfg.UserAgent == "" {
		cfg.UserAgent = "kubernetes-mcp"
	}
	return cfg, nil
}
