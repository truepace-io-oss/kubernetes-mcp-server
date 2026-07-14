#!/usr/bin/env bash
# Creates a throwaway kind cluster for the real-workload E2E test and prints the
# path to its kubeconfig. Tear down with: kind delete cluster --name kmcp-e2e
set -euo pipefail

CLUSTER="${KIND_CLUSTER_NAME:-kmcp-e2e}"
KUBECONFIG_OUT="${KUBECONFIG_OUT:-/tmp/kmcp-e2e.kubeconfig}"

if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  kind create cluster --name "${CLUSTER}" --wait 120s
fi

kind get kubeconfig --name "${CLUSTER}" > "${KUBECONFIG_OUT}"
echo "${KUBECONFIG_OUT}"
