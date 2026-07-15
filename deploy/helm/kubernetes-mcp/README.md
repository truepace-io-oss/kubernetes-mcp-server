# kubernetes-mcp Helm chart

Deploys the [kubernetes-mcp](../../../README.md) server. The MCP authenticates to
every cluster with a ServiceAccount token; **Kubernetes RBAC is the only
authorization gate**. Remote-cluster credentials are provided via the External
Secrets Operator (ESO).

## Install

```bash
helm install my-mcp deploy/helm/kubernetes-mcp \
  --namespace kubernetes-mcp --create-namespace
```

## What gets created

| Resource | When |
|----------|------|
| `ServiceAccount` | always — the in-cluster identity for the `local` cluster |
| `ClusterRole`/`ClusterRoleBinding` or `Role`/`RoleBinding` | `localCluster.rbac.tier` ≠ `none` |
| `ConfigMap` (`config.yaml`) | always — server + cluster registry (non-secret) |
| `ExternalSecret` (per remote cluster) | `externalSecrets.enabled` + `remoteClusters[].eso` |
| `ExternalSecret` (image pull) | `externalSecrets.enabled` + `imagePullSecret.esoRef` |
| `Deployment`, `Service` | always |
| `Ingress` | `ingress.enabled` |
| `ServiceMonitor` | `serviceMonitor.enabled` |

## Local-cluster RBAC tiers (`localCluster.rbac.tier`)

- `read-only` (default) — self-contained `get/list/watch` ClusterRole, **excludes secrets**.
- `edit` — binds the built-in `edit` ClusterRole.
- `cluster-admin` — binds `cluster-admin` (treat the pod as cluster root).
- `fine-grained` — namespaced `Role`+`RoleBinding` per `localCluster.rbac.fineGrained.namespaces` with `.rules`.
- `none` — no RBAC created (bring your own).

## Managing remote clusters via ESO

```yaml
externalSecrets:
  enabled: true
  secretStore:
    kind: ClusterSecretStore
    name: bitwarden-secretsmanager

remoteClusters:
  - name: customer-a
    server: https://api.customer-a.example.com:6443
    readOnly: true
    eso:
      tokenRef: <backend-uuid-for-token>   # → secret key `token`
      caRef:    <backend-uuid-for-ca.crt>  # → secret key `ca.crt`
```

Each remote cluster's Secret is mounted at `/etc/kmcp/clusters/<name>/{token,ca.crt}`
and referenced from the generated `config.yaml` via `tokenFile`/`certificateAuthorityFile`
(so ESO rotation is picked up without a restart). Without ESO, set
`remoteClusters[].existingSecret` to a Secret you manage (keys `token`, `ca.crt`).

Create those ServiceAccounts/tokens in each target cluster with the manifests in
[`deploy/rbac/`](../../rbac/).

## Agent → MCP authentication

Optional client-side auth (independent of cluster auth). Disabled by default.
See [`docs/auth.md`](../../../docs/auth.md) for the full guide.

Static bearer tokens via ESO (one Secret per token, mounted at
`/etc/kmcp/auth/<name>/token`):
```yaml
auth:
  enabled: true
  static:
    enabled: true
    tokens:
      - name: ci
        eso: { ref: <backend-uuid> }   # → secret key `token`
      # - name: legacy
      #   existingSecret: my-secret     # must contain key `token`
```

OIDC (Authentik / Keycloak — no secret needed, JWKS is public):
```yaml
auth:
  enabled: true
  oidc:
    enabled: true
    issuer: https://authentik.example.com/application/o/kubernetes-mcp/
    audience: https://kubernetes-mcp.intern.tools.averion.zone
    requiredGroups: ["k8s-admins"]     # optional
```
Both can be enabled together. Always pair auth with TLS (the ingress below).

## Ingress & security

The transport has **no built-in auth**. `ingress.enabled` defaults to `false`.
When enabled, it uses `ingressClassName: nginx-internal` with streaming-friendly
annotations and optional ESO-backed basic-auth. Never expose publicly.
