#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/common.sh"

require_existing_smoke_env() {
    [[ -f "$RUNTIME_ENV_FILE" ]] || fail "just failover requires an existing tmp/smoke environment; run 'just up' first"
}

load_failover_config() {
    require_existing_smoke_env
    load_down_config
    set_smoke_defaults
}

validate_existing_smoke_env() {
    cluster_exists || fail "kind cluster ${CLUSTER_NAME} does not exist; run 'just up' first"
    export_kubeconfig
    terraform_state_exists || fail "tmp/smoke has no Terraform state; run 'just up' first"
    kubectl -n "$OIDC_NAMESPACE" get deployment "$BRIDGE_RELEASE_NAME" >/dev/null 2>&1 || fail "bridge deployment ${BRIDGE_RELEASE_NAME} is missing in namespace ${OIDC_NAMESPACE}; run 'just up' first"
}

reset_failover_captures() {
    rm -f \
        "${CAPTURE_DIR}"/*-before.* \
        "${CAPTURE_DIR}"/*-after.* \
        "${CAPTURE_DIR}"/*-ha-baseline.* \
        "${CAPTURE_DIR}"/*-after-failover.* \
        "${CAPTURE_DIR}"/leader-before.txt \
        "${CAPTURE_DIR}"/leader-after-wait.txt \
        "${CAPTURE_DIR}"/leader-delete.log \
        "${CAPTURE_DIR}"/lease-before.json
}

upgrade_bridge_to_ha() {
    local values_file="${MANIFEST_DIR}/bridge-failover-values.yaml"

    write_bridge_values "$values_file" 2 true true
    deploy_bridge "$values_file" 2 failover-ha
    wait_for_available_replicas 2 90 2 || fail "bridge deployment did not reach 2 available replicas in HA mode"
}

run_failover_baseline() {
    verify_bridge_endpoints ha-baseline
    host_side_preflight ha-baseline
    run_in_cluster_proof ha-baseline
    capture_internal_metrics ha-baseline
    capture_bridge_debug_state before
}

delete_current_leader() {
    local leader_before="$1"
    capture_cmd "$(capture_artifact_path leader-delete.log)" kubectl -n "$OIDC_NAMESPACE" delete pod "$leader_before" --wait=false
}

verify_failover_recovery() {
    local previous_leader="$1"
    local new_leader

    new_leader="$(wait_for_new_leader "$previous_leader" 60 2)" || fail "leader Lease did not change after deleting ${previous_leader}"
    printf '%s\n' "$new_leader" >"$(capture_artifact_path leader-after-wait.txt)"
    [[ "$new_leader" != "$previous_leader" ]] || fail "new leader matches deleted leader ${previous_leader}"

    wait_for_available_replicas 2 90 2 || fail "bridge deployment did not recover to 2 available replicas after deleting ${previous_leader}"

    verify_bridge_endpoints after-failover
    host_side_preflight after-failover
    run_in_cluster_proof after-failover
    capture_internal_metrics after-failover
    capture_bridge_debug_state after

    local leader_after
    leader_after="$(<"$(capture_artifact_path leader.txt after)")"
    [[ "$leader_after" == "$new_leader" ]] || fail "post-failover leader snapshot reports ${leader_after}, expected ${new_leader}"
    assert_single_leader_snapshot before "$previous_leader"
    assert_single_leader_snapshot after "$new_leader"
}

main() {
    trap 'status=$?; if [[ $status -ne 0 ]]; then log "failover failed; inspect artifacts under ${CAPTURE_DIR}"; fi' EXIT

    ensure_smoke_dirs
    load_failover_config
    validate_failover_prereqs
    validate_existing_smoke_env
    reset_failover_captures
    capture_aws_identity

    build_bridge_image
    upgrade_bridge_to_ha
    run_failover_baseline

    local leader_before
    leader_before="$(<"$(capture_artifact_path leader.txt before)")"
    [[ -n "$leader_before" ]] || fail "unable to determine current leader from Lease"
    delete_current_leader "$leader_before"
    verify_failover_recovery "$leader_before"

    log "smoke failover succeeded"
}

main "$@"
