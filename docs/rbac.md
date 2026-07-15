# RBAC — ServiceAccounts for the MCP to connect to a cluster

The manifests in [`deploy/rbac/`](../deploy/rbac/) create the ServiceAccount +
RBAC in a **target cluster** (any cluster you want kubernetes-mcp to manage —
local or remote). The MCP then authenticates as that ServiceAccount, and
Kubernetes RBAC decides what it can do. There is no auth logic in the MCP itself.

Pick the tier that matches how much you trust the agent:

| Tier | Grants | Use when |
|------|--------|----------|
| [`read-only/`](../deploy/rbac/read-only) | `get/list/watch` cluster-wide, **no secrets** | inspection / triage / dashboards |
| [`full-access/`](../deploy/rbac/full-access) | `cluster-admin` | trusted operator tooling only ⚠️ |
| [`fine-grained/`](../deploy/rbac/fine-grained) | namespaced `Role` (read + targeted writes) | least-privilege, per-team |

## Apply

```bash
# read-only (creates the kubernetes-mcp namespace first)
kubectl create namespace kubernetes-mcp --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/rbac/read-only/

# fine-grained (edit the namespace `team-a` to your own first)
kubectl apply -f deploy/rbac/fine-grained/
```

## Get the credentials for the MCP config

```bash
deploy/rbac/extract-credentials.sh mcp-read-only kubernetes-mcp 24h
```

This prints the `server`, `certificateAuthorityData` and a **short-lived** token
(via the TokenRequest API), plus ready-to-paste `config.yaml` and Helm
`remoteClusters` snippets.

## Notes on security

- Prefer **short-lived tokens** (`kubectl create token … --duration=1h`) over the
  legacy static `kubernetes.io/service-account-token` Secret, which never expires
  and cannot be revoked.
- `read-only` omits `secrets` on purpose. Only add it back scoped to specific
  namespaces/objects if you truly need it.
- `fine-grained` avoids `secrets` read and `roles/rolebindings` write — both are
  privilege-escalation vectors.
- A `full-access` token is cluster root; store it in a secrets manager and rotate.
