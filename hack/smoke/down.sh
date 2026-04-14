#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/common.sh"

destroy_terraform() {
    if ! terraform_state_exists; then
        log "no Terraform state under tmp/smoke; skipping AWS destroy"
        return 0
    fi

    (
        cd "$TF_WORKDIR"
        capture_cmd "${CAPTURE_DIR}/tofu-destroy.log" tofu destroy -auto-approve -input=false
    )
}

delete_cluster() {
    if ! cluster_exists; then
        log "kind cluster ${CLUSTER_NAME} is already absent"
        return 0
    fi

    capture_cmd "${CAPTURE_DIR}/kind-delete.log" kind delete cluster --name "$CLUSTER_NAME"
}

main() {
    ensure_smoke_dirs
    load_down_config
    validate_down_prereqs
    if terraform_state_exists; then
        capture_aws_identity
        destroy_terraform
    else
        log "no Terraform state under tmp/smoke; skipping AWS teardown"
    fi
    delete_cluster
    log "smoke environment is down"
}

main "$@"
