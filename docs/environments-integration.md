# Deploying via the `environments` GitOps repo

The `environments` repo (myks + ytt + Helm + Argo CD) is where this image is
actually deployed. This chart is self-contained, but to run it in `prod-averion-tools`
follow the repo's prototype convention. This document is a pointer, not code in
this repo — do the changes in `environments`.

## 1. Add a prototype

```
prototypes/kubernetes-mcp/
├── app-data.ytt.yaml        # schema: image repo/tag, clusters, ESO refs, ingress host
├── vendir/                  # (optional) vendir this chart or a published version
├── helm/kubernetes-mcp.yaml # ytt-templated Helm values (maps env-data → chart values)
└── ytt/                     # or plain ytt manifests if not using Helm
```

Model it on `prototypes/io-averion-alert-bot` (simple Go HTTP service with ESO +
`gitlab-token-auth` image pull) and `prototypes/weaviate` (MCP behind an internal
ingress with streaming annotations).

## 2. Secrets via ESO (Bitwarden)

Reuse the existing `ClusterSecretStore` `bitwarden-secretsmanager`
(`prototypes/external-secrets/ytt/bitwarden-secretstore.yaml`). Map the chart's
`externalSecrets` + `remoteClusters[].eso.{tokenRef,caRef}` and
`imagePullSecret.esoRef` to Bitwarden UUIDs stored in `env-data`.

## 3. Internal ingress

Set `ingress.enabled: true`, `ingress.className: nginx-internal`,
`ingress.host: kubernetes-mcp.intern.tools.averion.zone`. Keep the streaming
annotations (already the chart defaults). The MCP is reachable only via
WireGuard — never public.

## 4. Pin the image version

Set the chart `image.tag` (or the env-data `kubernetes_mcp.version`) to the
`$CI_PIPELINE_ID` produced by this repo's `image-build-and-push` job (registry
`registry.gitlab.com/ai-guard/kubernetes-mcp`). Renovate can track it with a
`# renovate: datasource=docker` annotation.

## 5. Enable in the environment

Add `- proto: kubernetes-mcp` to
`envs/prod-averion-tools/env-data.ytt.yaml` under `environment.applications`,
then render **everything** (not just this app):

```bash
myks render ALL
```

Commit the `rendered/` output; Argo CD syncs from there.
