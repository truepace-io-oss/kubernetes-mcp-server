#!/usr/bin/env bash
# Extract the {apiserver URL, CA, token} an operator needs to register a target
# cluster with kubernetes-mcp, from the ServiceAccount you created with the
# manifests in this directory. Run this against the TARGET cluster (the one the
# MCP will manage), with kubectl pointed at it.
#
# Usage:
#   ./extract-credentials.sh <serviceaccount> <namespace> [token-duration]
# Example:
#   ./extract-credentials.sh mcp-read-only kubernetes-mcp 24h
set -euo pipefail

SA="${1:?serviceaccount name required}"
NS="${2:?namespace required}"
DURATION="${3:-24h}"

SERVER="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
CA_B64="$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
if [[ -z "${CA_B64}" ]]; then
  # Fall back to a CA file path if the kubeconfig references one.
  CA_FILE="$(kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority}')"
  CA_B64="$(base64 < "${CA_FILE}" | tr -d '\n')"
fi

# Short-lived, bound token (recommended). Requires Kubernetes >= 1.24.
TOKEN="$(kubectl create token "${SA}" -n "${NS}" --duration="${DURATION}")"

cat <<EOF

# ----------------------------------------------------------------------------
# Add this to the kubernetes-mcp server config.yaml under `clusters:`
# (write the token and ca.crt to files, or wire them via ESO — see deploy/helm).
# ----------------------------------------------------------------------------
  - name: CHANGE_ME
    server: ${SERVER}
    certificateAuthorityData: ${CA_B64}
    token: ${TOKEN}          # prefer tokenFile / ESO in production
    readOnly: true

# Helm values snippet (ESO variant) for deploy/helm/kubernetes-mcp:
#   remoteClusters:
#     - name: CHANGE_ME
#       server: ${SERVER}
#       readOnly: true
#       eso:
#         tokenRef: <store the token above in your secrets backend>
#         caRef:    <store the ca.crt (decoded) in your secrets backend>
EOF
