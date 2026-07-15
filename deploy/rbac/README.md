# RBAC examples

ServiceAccount + RBAC manifests to create in a **target cluster** so the MCP can
connect to it, in three tiers: `read-only/`, `full-access/`, `fine-grained/`.
`extract-credentials.sh` prints the `server`/CA/token for the MCP config.

See [`docs/rbac.md`](../../docs/rbac.md) for how to apply them and the security
notes.
