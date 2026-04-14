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

build_bridge_image() {
    rm -rf "$BUILD_CONTEXT_DIR"
    mkdir -p "$BUILD_CONTEXT_DIR"
    cp "${ROOT_DIR}/go.mod" "${ROOT_DIR}/go.sum" "$BUILD_CONTEXT_DIR/"
    cp -R "${ROOT_DIR}/cmd" "${ROOT_DIR}/internal" "$BUILD_CONTEXT_DIR/"
    capture_cmd "${CAPTURE_DIR}/bridge-image-build.log" docker build -f "${ROOT_DIR}/hack/smoke/Dockerfile" -t "$BRIDGE_IMAGE" "$BUILD_CONTEXT_DIR"
    capture_cmd "${CAPTURE_DIR}/kind-load-image.log" kind load docker-image --name "$CLUSTER_NAME" "$BRIDGE_IMAGE"
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

write_bridge_values() {
    cat >"${MANIFEST_DIR}/bridge-values.yaml" <<EOF
issuerUrl: ${SMOKE_ISSUER_URL}
fullnameOverride: ${BRIDGE_RELEASE_NAME}
logLevel: debug
image:
  repository: ${BRIDGE_IMAGE_REPOSITORY}
  tag: ${BRIDGE_IMAGE_TAG}
tailscale:
  hostname: ${TS_HOSTNAME}
  tag: ${SMOKE_TS_TAG}
  oauthSecret:
    name: ${OAUTH_SECRET_NAME}
  stateSecret:
    name: ${BRIDGE_STATE_SECRET_NAME}
sourceIpAllowlist:
  enabled: false
EOF
}

deploy_bridge() {
    write_bridge_values
    capture_cmd "${CAPTURE_DIR}/helm-upgrade.log" \
        helm upgrade --install "$BRIDGE_RELEASE_NAME" "${ROOT_DIR}/chart" \
        --namespace "$OIDC_NAMESPACE" \
        -f "${MANIFEST_DIR}/bridge-values.yaml"

    local attempt
    for attempt in $(seq 1 90); do
        if kubectl -n "$OIDC_NAMESPACE" get deployment "$BRIDGE_RELEASE_NAME" -o jsonpath='{.status.availableReplicas}' 2>/dev/null | grep -qx '1'; then
            break
        fi
        if kubectl -n "$OIDC_NAMESPACE" logs deployment/"$BRIDGE_RELEASE_NAME" --tail=200 2>/dev/null | tee "${CAPTURE_DIR}/bridge.log" | grep -Fq 'Funnel not available; "funnel" node attribute not set'; then
            fail "bridge cannot enable Funnel for ${SMOKE_TS_TAG}; the tag is missing the funnel node attribute"
        fi
        sleep 2
    done

    capture_cmd "${CAPTURE_DIR}/bridge-rollout.log" kubectl -n "$OIDC_NAMESPACE" rollout status deployment/"$BRIDGE_RELEASE_NAME" --timeout=300s
    kubectl -n "$OIDC_NAMESPACE" logs deployment/"$BRIDGE_RELEASE_NAME" >"${CAPTURE_DIR}/bridge.log"
}

verify_bridge_endpoints() {
    wait_for_http "${SMOKE_ISSUER_URL}/.well-known/openid-configuration" "${CAPTURE_DIR}/discovery-public.json" 60 5 || fail "public discovery endpoint did not become reachable"
    wait_for_http "$(jwks_uri)" "${CAPTURE_DIR}/jwks-public.json" 30 5 || fail "public JWKS endpoint did not become reachable"

    kubectl get --raw /.well-known/openid-configuration >"${CAPTURE_DIR}/discovery-internal.json"
    kubectl get --raw /openid/v1/jwks >"${CAPTURE_DIR}/jwks-internal.json"

    [[ "$(jq -r '.issuer' "${CAPTURE_DIR}/discovery-public.json")" == "$SMOKE_ISSUER_URL" ]] || fail "public discovery issuer does not match SMOKE_ISSUER_URL"
    [[ "$(jq -r '.jwks_uri' "${CAPTURE_DIR}/discovery-public.json")" == "$(jwks_uri)" ]] || fail "public discovery jwks_uri does not match expected value"

    jq -r '.keys[].kid' "${CAPTURE_DIR}/jwks-public.json" | sort >"${CAPTURE_DIR}/jwks-public-kids.txt"
    jq -r '.keys[].kid' "${CAPTURE_DIR}/jwks-internal.json" | sort >"${CAPTURE_DIR}/jwks-internal-kids.txt"
    diff -u "${CAPTURE_DIR}/jwks-internal-kids.txt" "${CAPTURE_DIR}/jwks-public-kids.txt" >"${CAPTURE_DIR}/jwks-kid-diff.txt"
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

host_side_preflight() {
    local token_file="${STATE_DIR}/web-identity-token.jwt"
    local stderr_file="${CAPTURE_DIR}/sts-preflight.err"
    local attempt

    rm -f "$token_file" "$stderr_file"
    trap 'rm -f "$token_file"' RETURN

    kubectl -n "$APP_NAMESPACE" create token "$APP_SERVICE_ACCOUNT" --audience sts.amazonaws.com --duration 10m >"$token_file"

    for attempt in $(seq 1 24); do
        if env -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN -u AWS_PROFILE \
            AWS_ROLE_ARN="$ROLE_ARN" \
            AWS_ROLE_SESSION_NAME="$HOST_PRELIGHT_SESSION_NAME" \
            AWS_WEB_IDENTITY_TOKEN_FILE="$token_file" \
            AWS_REGION="$AWS_REGION" \
            AWS_DEFAULT_REGION="$AWS_REGION" \
            AWS_EC2_METADATA_DISABLED=true \
            aws sts get-caller-identity --output json >"${CAPTURE_DIR}/sts-preflight.json" 2>"$stderr_file"; then
            rm -f "$token_file"
            trap - RETURN
            return 0
        fi
        log "waiting for IAM propagation before STS preflight (attempt ${attempt}/24)"
        sleep 5
    done

    fail "host-side AssumeRoleWithWebIdentity preflight did not succeed; see ${stderr_file}"
}

write_proof_manifest() {
    cat >"${MANIFEST_DIR}/awscli-proof-job.yaml" <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${PROOF_JOB_NAME}
  namespace: ${APP_NAMESPACE}
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: ${APP_SERVICE_ACCOUNT}
      automountServiceAccountToken: false
      containers:
        - name: aws-cli
          image: amazon/aws-cli:latest
          imagePullPolicy: IfNotPresent
          command:
            - sh
            - -ceu
            - |
              caller_arn="\$(aws sts get-caller-identity --query Arn --output text)"
              account_id="\$(aws sts get-caller-identity --query Account --output text)"
              role_arn="\$(aws iam get-role --role-name "${ROLE_NAME}" --query Role.Arn --output text)"
              printf '{"account":"%s","callerArn":"%s","roleArn":"%s"}\n' "\$account_id" "\$caller_arn" "\$role_arn"
          env:
            - name: AWS_ROLE_ARN
              value: "${ROLE_ARN}"
            - name: AWS_ROLE_SESSION_NAME
              value: "${IN_CLUSTER_SESSION_NAME}"
            - name: AWS_WEB_IDENTITY_TOKEN_FILE
              value: /var/run/secrets/oidc/token
            - name: AWS_REGION
              value: "${AWS_REGION}"
            - name: AWS_DEFAULT_REGION
              value: "${AWS_REGION}"
            - name: AWS_EC2_METADATA_DISABLED
              value: "true"
          volumeMounts:
            - name: oidc-token
              mountPath: /var/run/secrets/oidc
              readOnly: true
      volumes:
        - name: oidc-token
          projected:
            sources:
              - serviceAccountToken:
                  audience: sts.amazonaws.com
                  expirationSeconds: 3600
                  path: token
EOF
}

run_in_cluster_proof() {
    write_proof_manifest
    kubectl -n "$APP_NAMESPACE" delete job "$PROOF_JOB_NAME" --ignore-not-found >/dev/null
    kubectl apply -f "${MANIFEST_DIR}/awscli-proof-job.yaml" >/dev/null
    capture_cmd "${CAPTURE_DIR}/awscli-proof-wait.log" kubectl -n "$APP_NAMESPACE" wait --for=condition=complete --timeout=300s job/"$PROOF_JOB_NAME"
    kubectl -n "$APP_NAMESPACE" logs job/"$PROOF_JOB_NAME" >"${CAPTURE_DIR}/awscli-proof.json"

    [[ "$(jq -r '.roleArn' "${CAPTURE_DIR}/awscli-proof.json")" == "$ROLE_ARN" ]] || fail "in-cluster proof returned an unexpected role ARN"
    if ! jq -r '.callerArn' "${CAPTURE_DIR}/awscli-proof.json" | grep -Fq ":assumed-role/${ROLE_NAME}/"; then
        fail "in-cluster proof did not return an assumed-role ARN"
    fi
}

capture_kubernetes_state() {
    kubectl -n "$OIDC_NAMESPACE" get all,secret,serviceaccount,role,rolebinding >"${CAPTURE_DIR}/oidc-system-resources.txt"
    kubectl -n "$APP_NAMESPACE" get all,secret,serviceaccount,job >"${CAPTURE_DIR}/app-resources.txt"
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
    deploy_bridge
    verify_bridge_endpoints
    apply_terraform
    host_side_preflight
    run_in_cluster_proof
    capture_kubernetes_state
    log "smoke environment is up"
}

main "$@"
