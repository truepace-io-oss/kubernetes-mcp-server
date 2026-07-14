# Kubernetes MCP — Implementation Process Log

> **Purpose:** A running record of what was actually done, filled in **during** implementation — one entry per completed step from [`kubernetes-mcp-plan.md`](./kubernetes-mcp-plan.md). Keep it detailed enough that anyone can **pause and resume** work from here without re-reading the whole codebase.
>
> **How to use:** Before starting a step, set it to 🟡 in the tracker. After finishing, fill its entry (below) and flip it to ✅. Always paste **real command output** for anything you ran (tests, `helm template`, `docker build`).

---

## Resume-here summary (keep this current)

- **Last completed step:** Step 15 (final pass). **Implementation complete — all 15 steps done.**
- **Currently in progress:** none.
- **Next action:** none required. Optional follow-ups: connect to the GitLab remote + push; add the prototype to the `environments` repo (see `docs/environments-integration.md`); add a `/metrics` endpoint if `serviceMonitor` is to be used (currently the chart exposes a ServiceMonitor toggle but the server does not yet serve Prometheus metrics).
- **Pinned versions (decided):** Go 1.25.1; `modelcontextprotocol/go-sdk v1.6.1`; `k8s.io/client-go v0.34.1` (+ api/apimachinery v0.34.1); envtest K8s `1.34.0` (Makefile `ENVTEST_K8S`).
- **Module path:** `gitlab.com/ai-guard/kubernetes-mcp`. Registry: `registry.gitlab.com/ai-guard/kubernetes-mcp`.
- **Known open items:**
  - Steps 8–15 still to do (E2E envtest/kind, Helm chart, RBAC examples, Dockerfile, CI, README/docs, final pass).
- **How to resume:** run `make test` (should be green), then continue with the "Next action".

---

## Progress tracker

| Step | Title | Status |
|------|-------|--------|
| 1  | Repo scaffolding & module            | ✅ done |
| 2  | Config package                       | ✅ done |
| 3  | Cluster registry & credentials       | ✅ done |
| 4  | Generic resource layer + read tools  | ✅ done |
| 5  | Mutating tools (guarded)             | ✅ done |
| 6  | Entrypoint wiring                    | ✅ done |
| 7  | Unit tests                           | ✅ done |
| 8  | E2E envtest (local API, primary)     | ✅ done |
| 9  | E2E RBAC tier assertions             | ✅ done |
| 10 | E2E kind (real workloads)            | ✅ done |
| 11 | Helm chart                           | ✅ done |
| 12 | RBAC example manifests               | ✅ done |
| 13 | GitLab CI + Dockerfile               | ✅ done |
| 14 | README + docs (Mermaid)              | ✅ done |
| 15 | Final pass                           | ✅ done |

Legend: ⬜ not started · 🟡 in progress · ✅ done · ⚠️ done with caveats (note them).

---

## Step entries

> Copy this template for each step as you complete it.

### Template
```
### Step N — <title>  [status]
**Date:**
**What was done:** <concrete summary — files created/changed and why>
**Files touched:** <paths>
**Key decisions / deviations from plan:** <anything that differs from kubernetes-mcp-plan.md and why>
**Commands run + output:**
    <command>
    <pasted real output — especially test results>
**Acceptance check:** <the plan's acceptance criterion + PASS/FAIL>
**Follow-ups / TODO for later steps:** <anything deferred>
```

### Step 1 — Repo scaffolding & module  [✅]
**What was done:** `go mod init gitlab.com/ai-guard/kubernetes-mcp` (go 1.25); added `.gitignore`, `.dockerignore`, `Makefile` (build/test/test-e2e/test-e2e-kind/lint/helm-lint/run/tidy), README stub deferred to Step 14.
**Key decisions:** module path `gitlab.com/ai-guard/kubernetes-mcp`; `ENVTEST_K8S=1.34.0` in the Makefile.
**Acceptance:** `go build ./...` PASS.

### Step 2 — Config package  [✅]
**What was done:** `internal/config/config.go` — `Config`/`ClusterConfig` structs, `Load()` (YAML + `KMCP_*` env overrides + defaults), `Validate()` (one auth mode per cluster, unique DNS-label names, default-cluster presence, CA/token/insecure rules), `Warnings()` (inline-token/insecure advisories). `examples/config.yaml` documents all three auth modes.
**Files:** `internal/config/config.go`, `internal/config/config_test.go`, `examples/config.yaml`.
**Acceptance:** `go test ./internal/config/...` PASS (valid multi-cluster, single-cluster default inference, 11 validation-error cases, env overrides, warnings).

### Step 3 — Cluster registry & credentials  [✅]
**What was done:** `credentials.go` builds `*rest.Config` from in-cluster / explicit(server+CA+token) / kubeconfig-context, preferring file forms (rotation), QPS/Burst set, never inline-insecure by default. `cluster.go` builds typed+dynamic+discovery clients and a `DeferredDiscoveryRESTMapper`; `Ping()` returns server version. `registry.go` is a `sync.RWMutex`-guarded `map[name]*Cluster` with `Get/Default/Names/All`.
**Files:** `internal/clusters/{credentials,cluster,registry}.go` + `registry_test.go` + `testhelpers_test.go`.
**Key decisions:** mapper stored as the `k8s.ResettableRESTMapper` interface (added in Step 4 refactor) so tests can inject fakes; added `NewForTest`/`NewRegistryForTest` seams.
**Acceptance:** `go test ./internal/clusters/...` PASS — builds a registry against an httptest TLS server, verifies accessors + `Ping` (`v1.34.0-test`) + unknown-cluster error.

### Step 4 — Generic resource layer + read tools  [✅]
**What was done:** `internal/k8s/resources.go` — GVK→GVR via REST mapper (reset+retry on NoMatch), `List/Get/Delete/Apply(SSA)/Patch` on the dynamic client, namespaced-vs-cluster scope handled. `internal/mcpserver/` — `params.go` (shared input structs w/ jsonschema docs + cluster/namespace resolution), `format.go` (list/object/events rendering, Secret redaction, HTML-escape off), `server.go` (Server + `MCPServer()`), `tools_read.go` (clusters_list, namespaces_list, resources_list, resources_get, pods_list, pods_log, events_list, nodes_list).
**Acceptance:** `go build ./...` + `go vet ./...` PASS.

### Step 5 — Mutating tools (guarded)  [✅]
**What was done:** `tools_write.go` — resources_apply (SSA), resources_delete, deployment_scale (merge patch), rollout_restart (strategic-merge annotation). Central `assertWritable()` blocks writes on global `readOnly` or per-cluster `readOnly` before any API call; RBAC remains the ultimate gate.
**Acceptance:** covered by unit tests in Step 7 (guard + allowed-write).

### Step 6 — Entrypoint wiring  [✅]
**What was done:** `main.go` — flags/env → `config.Load` → `clusters.Build` → `mcpserver.New` → `mcp.NewStreamableHTTPHandler` at `/mcp`; `/healthz` + `/readyz` (probes default cluster); slog JSON logging; graceful shutdown on SIGINT/SIGTERM; `--version`. Security note in package doc: transport has no built-in auth → internal ingress only.
**Acceptance:** `go build ./...` PASS.

### Step 7 — Unit tests  [✅]
**What was done:** `internal/mcpserver/tools_test.go` — fake typed (`kubernetes/fake`) + fake dynamic (`dynamic/fake` with `scheme.Scheme`) + `DeferredDiscoveryRESTMapper` over a fake discovery doc. Covers namespaces_list, pods_list, resources_list (ConfigMap), Secret redaction, unknown-cluster error, global + per-cluster write guards, allowed delete, and `MCPServer()` registration. Fixed `objectJSON` to disable HTML escaping so `<redacted>` renders cleanly.
**Commands run + output:**
    go test ./internal/... -race -count=1
    ok  gitlab.com/ai-guard/kubernetes-mcp/internal/clusters   2.039s
    ok  gitlab.com/ai-guard/kubernetes-mcp/internal/config     1.468s
    ?   gitlab.com/ai-guard/kubernetes-mcp/internal/k8s        [no test files]
    ok  gitlab.com/ai-guard/kubernetes-mcp/internal/mcpserver  1.813s
**Acceptance:** PASS (all unit tests green with `-race`).

### Step 8 — E2E envtest (local API, primary)  [✅]
**What was done:** `test/e2e/harness_test.go` starts a real kube-apiserver+etcd via controller-runtime envtest (`TestMain`), mints short-lived SA tokens via the TokenRequest API, builds a real kubernetes-mcp instance (config→registry→mcpserver→streamable-HTTP `httptest` server) pointed at envtest with `{server, CAData, token}`, and connects the official MCP go-sdk client. `e2e_read_test.go` seeds a ConfigMap as admin, provisions a self-contained read-only ClusterRole+SA+binding, then drives `clusters_list`/`namespaces_list`/`resources_list`/`resources_get` through the MCP and asserts the seeded data comes back ("hello-from-e2e") — the required "local API + fetch data through the MCP end-to-end" proof.
**Key decisions/deviations:** added test dep `sigs.k8s.io/controller-runtime v0.22.3` (compatible with Go 1.25 + k8s 1.34); envtest binaries via `setup-envtest use 1.34.0`; envtest's default apiserver already supports TokenRequest+SA-token authn, so no extra flags were needed.

### Step 9 — E2E RBAC tier assertions  [✅]
**What was done:** `e2e_rbac_test.go` — three tests proving behavior differs *only* by RBAC on identical MCP code: (1) read-only token → `resources_apply` returns Kubernetes **Forbidden** as a tool error; (2) writer token → same tool succeeds and the object is confirmed present via the admin client; (3) per-cluster `readOnly=true` guard blocks the write *before* the API call even with a write-capable token.
**Fix during dev:** test ConfigMap value `x: y` was parsed by YAML 1.1 as boolean `true` (rejected by ConfigMap string data) → changed to single-quoted `x: 'val'`.
**Commands run + output:**
    export KUBEBUILDER_ASSETS="$(setup-envtest use 1.34.0 -p path)"
    go test ./test/e2e/... -count=1 -v
    --- PASS: TestE2ERBACReadOnlyTokenCannotWrite (0.02s)
    --- PASS: TestE2ERBACWriterTokenCanWrite (0.01s)
    --- PASS: TestE2EPerClusterReadOnlyGuard (0.01s)
    --- PASS: TestE2EReadOnlyFlow (0.08s)
    ok  gitlab.com/ai-guard/kubernetes-mcp/test/e2e  5.062s
**Acceptance:** PASS.

### Step 10 — E2E kind (real workloads)  [✅]
**What was done:** `test/e2e/kind/e2e_kind_test.go` (build tag `e2e_kind`) + `setup-kind.sh`. The test deploys a busybox pod emitting a known marker line, waits for Running, mints a reader SA token, runs the MCP against the kind cluster, and asserts `pods_list` shows the pod and `pods_log` returns the marker — the running-workload coverage envtest can't provide. Ran it against a real kind cluster and tore the cluster down afterwards.
**Commands run + output:**
    export KUBECONFIG="$(test/e2e/kind/setup-kind.sh)"
    go test -tags e2e_kind ./test/e2e/kind/... -count=1 -v
    --- PASS: TestE2EKindPodLogs (4.18s)
    ok  gitlab.com/ai-guard/kubernetes-mcp/test/e2e/kind  4.821s
**Acceptance:** PASS.

### Step 11 — Helm chart  [✅]
**What was done:** `deploy/helm/kubernetes-mcp/` — Chart.yaml, values.yaml, _helpers.tpl, serviceaccount, **rbac.yaml (configurable tier: read-only|edit|cluster-admin|fine-grained|none)**, configmap (renders server `config.yaml`: local in-cluster + remote clusters with `/etc/kmcp/clusters/<name>/{token,ca.crt}` file paths), **externalsecret.yaml (ESO: per-remote-cluster token+ca.crt, image-pull dockerconfigjson, optional basic-auth htpasswd)**, deployment (mounts config + per-cluster secrets, nonroot/read-only-rootfs/dropped-caps, `/healthz`+`/readyz` probes), service, ingress (nginx-internal + streaming annotations, default off), servicemonitor (toggle), NOTES.txt, chart README.
**Key decisions:** ESO default `ClusterSecretStore: bitwarden-secretsmanager` (matches `environments`); non-ESO fallback via `existingSecret`.
**Commands run + output:**
    helm lint deploy/helm/kubernetes-mcp        → 1 chart(s) linted, 0 failed
    helm template (defaults)                    → 6 resources, valid YAML
    helm template (ESO + 2 remote clusters + ingress + basicAuth) → +4 ExternalSecrets +Ingress, valid YAML
    helm template (fine-grained, ns team-a/team-b) → per-namespace Role+RoleBinding, valid YAML
    python3 yaml.safe_load_all on all three renders → valid
**Acceptance:** PASS.

### Step 12 — RBAC example manifests  [✅]
**What was done:** `deploy/rbac/{read-only,full-access,fine-grained}/` (SA + role(s) + binding each), `extract-credentials.sh` (prints server/CA/short-lived token + ready-to-paste config.yaml & Helm snippets), `deploy/rbac/README.md`. read-only excludes secrets; fine-grained avoids secrets-read & rbac-write.
**Commands run:** `yaml.safe_load_all` on all 8 manifests → valid; `bash -n extract-credentials.sh` → OK.
**Acceptance:** PASS.

### Step 13 — GitLab CI + Dockerfile  [✅]
**What was done:** multi-arch `Dockerfile` (`--platform=$BUILDPLATFORM` builder, cross-compile via `TARGETOS/TARGETARCH`, distroless nonroot); `.gitlab-ci.yml` stages test→build: unit, e2e-envtest (setup-envtest), helm-validate, e2e-kind (dind+kind, default branch or `e2e-kind` label), image-build-and-push (buildx `linux/amd64,linux/arm64` + binfmt, tags `$CI_PIPELINE_ID`+`latest`, default branch).
**Commands run + output:**
    docker build (native arm64)                         → image built OK
    docker buildx build --platform linux/amd64,linux/arm64 (docker-container driver) → both arches compiled OK
**Acceptance:** PASS (Dockerfile builds both arches; CI YAML well-formed).

### Step 14 — README + docs (Mermaid)  [✅]
**What was done:** `README.md` (topology / central-auth sequence / registry / ESO-deployment Mermaid diagrams, tool catalog, quick start, multi-instance client config, Helm+ESO, security model, testing), `docs/architecture.md` (request-flow, auth-modes, testing-strategy diagrams + package table), `docs/environments-integration.md` (GitOps prototype steps), `examples/mcp.claude.json` + `examples/mcp.cursor.json` (multiple MCP instances per agent).
**Key decision:** used `<br/>` (not `\n`) in Mermaid node labels and removed `<...>` from sequence message text for portable rendering (no mermaid CLI available; validated by inspection — standard flowchart/sequence constructs).
**Acceptance:** PASS.

### Step 15 — Final pass  [✅]
**What was done:** `gofmt -w` on e2e files; `go vet ./...` clean; `go mod tidy`; full test run; binary smoke test.
**Commands run + output:**
    go test ./internal/... -race        → clusters/config/mcpserver all ok
    KUBEBUILDER_ASSETS=... go test ./test/e2e/...  → ok (4.474s)
    go build -ldflags -X main.version=smoke; ./bin/kubernetes-mcp --version → smoke
    helm lint deploy/helm/kubernetes-mcp → 0 failed
    runtime smoke: /healthz → 200 ; POST /mcp initialize → HTTP 200 + Mcp-Session-Id
**Known caveats/open items:**
  - Chart exposes a `serviceMonitor` toggle but the server does not yet serve `/metrics` (Prometheus). Add an endpoint before enabling it.
  - kind E2E requires Docker; it is opt-in (build tag `e2e_kind`) and a separate CI job.
  - Repo has no git remote yet; not pushed. Deploying via `environments` is documented but not executed.
**Acceptance:** PASS — `make test` green, `make test-e2e` green, `helm template` valid, multi-arch `docker buildx build` succeeds.
