#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/common.sh"

create_cluster() {
    write_kind_config
    if cluster_exists; then
        log "reusing existing kind cluster ${CLUSTER_NAME}"
        export_kubeconfig
    else
        capture_cmd "${CAPTURE_DIR}/kind-create.log" kind create cluster --name "$CLUSTER_NAME" --config "${KIND_DIR}/config.yaml" --kubeconfig "$KUBECONFIG_PATH"
        export KUBECONFIG="$KUBECONFIG_PATH"
    fi

    capture_cmd "${CAPTURE_DIR}/kind-nodes.log" kubectl get nodes -o wide
    capture_cmd "${CAPTURE_DIR}/kind-nodes-ready.log" kubectl wait --for=condition=Ready nodes --all --timeout=180s
}

ensure_namespaces_and_service_account() {
    kubectl create namespace "$OIDC_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    kubectl create namespace "$APP_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    kubectl -n "$APP_NAMESPACE" create serviceaccount "$APP_SERVICE_ACCOUNT" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

create_oauth_secret() {
    kubectl -n "$OIDC_NAMESPACE" create secret generic "$OAUTH_SECRET_NAME" \
        --from-literal=TS_API_CLIENT_ID="$TS_API_CLIENT_ID" \
        --from-literal=TS_API_CLIENT_SECRET="$TS_API_CLIENT_SECRET" \
        --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

prepare_terraform_workdir() {
    ln -snf "${ROOT_DIR}/terraform/modules" "${TMP_ROOT_DIR}/modules"
    cp "${ROOT_DIR}/terraform/examples/basic/"*.tf "$TF_WORKDIR/"
    cat >"${TF_WORKDIR}/smoke.auto.tfvars" <<EOF
issuer_url = "${SMOKE_ISSUER_URL}"
role_name = "${ROLE_NAME}"
kubernetes_namespace = "${APP_NAMESPACE}"
kubernetes_service_account = "${APP_SERVICE_ACCOUNT}"
tags = {
  managed-by = "hack/smoke"
  smoke-name = "${SMOKE_NAME}"
}
EOF
}

apply_terraform() {
    prepare_terraform_workdir
    assert_no_unmanaged_aws_resources
    (
        cd "$TF_WORKDIR"
        capture_cmd "${CAPTURE_DIR}/tofu-init.log" tofu init -input=false
        capture_cmd "${CAPTURE_DIR}/tofu-apply.log" tofu apply -auto-approve -input=false
        tofu output -json >"${CAPTURE_DIR}/tofu-outputs.json"
    )
}

main() {
    ensure_smoke_dirs
    load_up_config
    validate_up_prereqs
    capture_aws_identity
    capture_tailscale_status
    assert_runtime_config_matches
    write_runtime_env

    create_cluster
    ensure_namespaces_and_service_account
    create_oauth_secret
    build_bridge_image
    write_bridge_values "${MANIFEST_DIR}/bridge-values.yaml"
    deploy_bridge "${MANIFEST_DIR}/bridge-values.yaml" 1
    verify_bridge_endpoints
    apply_terraform
    host_side_preflight
    run_in_cluster_proof
    capture_internal_metrics
    capture_kubernetes_state
    log "smoke environment is up"
}

main "$@"
