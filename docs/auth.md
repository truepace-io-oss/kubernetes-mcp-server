# Agent → MCP authentication

This secures the **client-side** link (AI agent → MCP server). It is independent
of cluster authentication: this decides *who may talk to the MCP*, while the
ServiceAccount token + Kubernetes RBAC decide *what the MCP may do in a cluster*.
The two are separate layers.

> ⚠️ Bearer tokens over plaintext are insecure. Whenever auth is enabled, run the
> MCP behind **TLS** — an internal ingress (cert-manager) or a TLS-terminating
> proxy. Never expose it publicly.

Three modes, usable **independently or together** (a request is accepted if any
enabled verifier accepts it):

| Mode | Who it's for | UI on first use? |
|------|--------------|------------------|
| `static` | machines / CI / break-glass | never |
| `oidc` (Authentik / Keycloak) | humans | **yes** — browser login (once) |
| both | machines *and* humans | only for the OIDC path |

Disabled by default (`auth.enabled: false`) → today's behavior (protect via ingress).

## 1. Static bearer tokens

```yaml
auth:
  enabled: true
  static:
    enabled: true
    tokens:
      - name: ci
        tokenFile: /etc/kmcp/auth/ci-token   # preferred (ESO / rotatable)
      - name: laptop
        token: "s3cr3t"                       # inline, discouraged
```

The agent sends `Authorization: Bearer <token>`. Rotation: overwrite the
`tokenFile` (ESO does this) — it's re-read per request, no restart. No browser,
ever.

Client config (Claude Code):
```json
{ "mcpServers": { "k8s": {
  "type": "http",
  "url": "https://kubernetes-mcp.example.com/mcp",
  "headers": { "Authorization": "Bearer ${KMCP_TOKEN}" }
}}}
```

## 2. OIDC (Authentik / Keycloak)

The MCP becomes an OAuth 2.1 **Resource Server**: it validates JWT access tokens
(signature via the provider's JWKS, issuer, audience, expiry) and — optionally —
required scopes/groups. It advertises the provider via Protected Resource
Metadata so MCP clients discover it and run the browser flow.

```yaml
auth:
  enabled: true
  oidc:
    enabled: true
    # Authentik: https://authentik.example.com/application/o/<slug>/
    # Keycloak:  https://kc.example.com/realms/<realm>
    issuer: "https://authentik.example.com/application/o/kubernetes-mcp/"
    audience: "https://kubernetes-mcp.example.com"   # == token `aud` (RFC 8707)
    requiredScopes: ["mcp.access"]     # optional
    requiredGroups: ["k8s-admins"]     # optional
    groupsClaim: "groups"
    usernameClaim: "preferred_username"
    resourceMetadata: true
```

No secret is needed (JWKS is public) and no `Authorization` header is set in the
client config — the client obtains the token itself. **How much client config is
needed depends on whether your provider supports Dynamic Client Registration
(DCR)** — see [§4 Client configuration](#4-client-configuration-claude-code).
Keycloak supports DCR; **Authentik does not**.

### Does it open a UI on first use? Yes.

On the first connect the **client** (Claude Code / Cursor), not the server,
opens a browser to the Authentik/Keycloak login + consent page. After that the
token is cached and refreshed **silently** — the browser reappears only once the
refresh token is gone/revoked. Headless/cron clients use the **client-credentials**
grant instead → no browser.

```mermaid
sequenceDiagram
    participant U as You (browser)
    participant C as Agent host (Claude Code)
    participant M as kubernetes-mcp (Resource Server)
    participant A as Authentik / Keycloak (Auth Server)
    C->>M: POST /mcp (no token)
    M-->>C: 401 + WWW-Authenticate (resource_metadata URL)
    C->>M: GET /.well-known/oauth-protected-resource
    M-->>C: { authorization_servers: [issuer], resource: audience }
    C->>A: discover AS metadata + get a client (DCR, or a pre-registered client_id)
    C->>U: opens browser → login + consent  (the UI, first use only)
    U->>A: authenticate / approve
    A-->>C: auth code → access token (PKCE)
    C->>M: POST /mcp  Authorization: Bearer <JWT>
    M->>A: verify via JWKS (sig, iss, aud, exp) + scope/group checks
    M-->>C: 200 — tools available
    Note over C,A: later calls reuse the token; refresh is silent until revoked
```

## 3. Provider setup

### Authentik
Authentik has **no Dynamic Client Registration**, so you must pre-register a
**public** client and give its `client_id` to the agent (see §4).
1. Create an **OAuth2/OpenID Provider** (`client_type: public`, RS256 signing key)
   + **Application**. The application **slug** fixes the issuer
   (`https://auth.<domain>/application/o/<slug>/`; JWKS at `.../jwks/`).
2. Set `include_claims_in_access_token: true` — the MCP validates the **access**
   token. Authentik stamps `aud = client_id`, so set the MCP's
   `oidc.audience` to that `client_id` (e.g. `kubernetes-mcp`).
3. Add a `groups` **scope mapping** if you use `requiredGroups`, and a
   **policy binding** to the group(s) allowed to log in.
4. Set **redirect URIs** to regex so any Claude Code callback port matches:
   `http://localhost:\d+/.*` and `http://127.0.0.1:\d+/.*`.

> This whole provider can be created declaratively via an Authentik **blueprint**
> — see the working example in [`docs/environments-integration.md`](./environments-integration.md).

### Keycloak
1. Realm → **Client** (public, PKCE, standard flow). Issuer
   `https://kc.example.com/realms/<realm>`.
2. Add an **Audience mapper** so tokens carry `aud = <MCP url>` == `oidc.audience`.
3. Add a **groups** mapper for `requiredGroups`. Enable Dynamic Client
   Registration or pre-register the redirect URI.

Both are standard OIDC — same `issuer` / `audience` config, only the URLs differ.

## 4. Client configuration (Claude Code)

How the agent connects depends on whether the provider supports **Dynamic Client
Registration (DCR)**:

**Provider supports DCR (e.g. Keycloak):** just the URL — the client registers
itself and discovers everything from the MCP's protected-resource metadata:
```json
{ "mcpServers": { "kubernetes-mcp": { "type": "http", "url": "https://kubernetes-mcp.example.com/mcp" } } }
```

**Provider has no DCR (e.g. Authentik):** the client can't self-register, so give
it the **pre-registered `client_id`** and the **authorization-server metadata
URL** explicitly:
```json
{
  "mcpServers": {
    "kubernetes-mcp": {
      "type": "http",
      "url": "https://kubernetes-mcp.example.com/mcp",
      "oauth": {
        "clientId": "kubernetes-mcp",
        "authServerMetadataUrl": "https://auth.example.com/application/o/kubernetes-mcp/.well-known/openid-configuration"
      }
    }
  }
}
```
Then trigger login: `claude mcp login kubernetes-mcp` (or `/mcp` → *Authenticate*);
reset a bad state with `claude mcp logout kubernetes-mcp`. Requires a recent
Claude Code (pre-registered-client OAuth support, ~v2.1.186+).

> **Why `authServerMetadataUrl` is needed today:** the MCP's `401` currently
> advertises a *relative* `resource_metadata` path in `WWW-Authenticate`. Some
> clients (Claude Code with a manual `client_id`) then default the authorization
> server to the MCP's own origin and hit a non-existent `/authorize`. Pointing
> `authServerMetadataUrl` at the provider's OIDC discovery fixes it. A future
> release will emit an absolute `resource_metadata` URL so this override becomes
> unnecessary.

## 5. Both at once

Enable `static` and `oidc` together: machines present a static bearer token,
humans get the OIDC browser flow. A request passes if either verifier accepts it.

## Note: authentication vs cluster RBAC

OIDC/static authenticates the *caller to the MCP*. It does **not** change cluster
permissions — every authenticated caller shares the instance's ServiceAccount and
its RBAC. Per-user cluster permissions (mapping the OIDC identity to Kubernetes
via impersonation/token-exchange) is a separate, larger feature and not
implemented here.
