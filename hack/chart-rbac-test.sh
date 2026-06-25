#!/usr/bin/env bash

# Renders the chart RBAC and asserts that leader-election Lease permissions are
# granted exactly when leaderElection.enabled=true, while the tsnet state-Secret
# RBAC is always present and the Role binds the workload ServiceAccount. This
# guards the leader-election RBAC fix (v1.1.1): without leases get/create/update
# the bridge ServiceAccount cannot create its Lease and every replica crash-loops.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="${ROOT_DIR}/chart"
VALUES="${CHART_DIR}/ci-values.yaml"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

render() {
    helm template test "${CHART_DIR}" -f "${VALUES}" "$@"
}

# --- leaderElection.enabled=true: Lease RBAC must be granted ---
ha_role="$(render --set leaderElection.enabled=true --set replicaCount=2 \
    --show-only templates/role.yaml)"

grep -q 'coordination.k8s.io' <<<"${ha_role}" \
    || fail "HA Role is missing the coordination.k8s.io/leases rule"

# Isolate the leases rule (the last rule) and confirm it grants get/create/update
# and is not name-scoped, since RBAC ignores resourceNames for create.
leases_rule="$(awk '/coordination.k8s.io/{f=1} f' <<<"${ha_role}")"
for verb in get create update; do
    grep -qE "^[[:space:]]*-[[:space:]]*${verb}$" <<<"${leases_rule}" \
        || fail "HA leases rule is missing verb: ${verb}"
done
if grep -q 'resourceNames' <<<"${leases_rule}"; then
    fail "HA leases rule must not be resourceNames-scoped (RBAC ignores it for create)"
fi

# The state-Secret (kubestore) RBAC must remain alongside the leases rule.
grep -q 'secrets' <<<"${ha_role}" || fail "HA Role is missing the state-Secret rule"

# The Role must bind the workload ServiceAccount.
sa="$(render --set leaderElection.enabled=true --set replicaCount=2 \
    --show-only templates/serviceaccount.yaml | awk '/^  name:/{print $2; exit}')"
render --set leaderElection.enabled=true --set replicaCount=2 \
    --show-only templates/rolebinding.yaml | grep -q "name: ${sa}" \
    || fail "RoleBinding does not bind the workload ServiceAccount (${sa})"

# --- leaderElection.enabled=false: no Lease RBAC, state-Secret RBAC stays ---
solo_role="$(render --set leaderElection.enabled=false --show-only templates/role.yaml)"
if grep -qE 'coordination.k8s.io|leases' <<<"${solo_role}"; then
    fail "non-HA Role must not grant any leases RBAC"
fi
grep -q 'secrets' <<<"${solo_role}" || fail "non-HA Role is missing the state-Secret rule"

printf 'OK: leases RBAC present only when leaderElection.enabled; state-Secret RBAC always present\n'
