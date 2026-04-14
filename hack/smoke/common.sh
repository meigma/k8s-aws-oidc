#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT_DIR="${ROOT_DIR}/tmp"
SMOKE_DIR="${ROOT_DIR}/tmp/smoke"
CAPTURE_DIR="${SMOKE_DIR}/captures"
MANIFEST_DIR="${SMOKE_DIR}/manifests"
KIND_DIR="${SMOKE_DIR}/kind"
STATE_DIR="${SMOKE_DIR}/state"
TF_WORKDIR="${SMOKE_DIR}/terraform"
BUILD_CONTEXT_DIR="${STATE_DIR}/build-context"
KUBECONFIG_PATH="${STATE_DIR}/kubeconfig"
RUNTIME_ENV_FILE="${STATE_DIR}/run.env"

export AWS_PAGER=""

log() {
    printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

fail() {
    log "ERROR: $*"
    exit 1
}

require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        fail "missing required command: ${cmd}"
    fi
}

capture_cmd() {
    local output="$1"
    shift
    log "running: $*"
    "$@" 2>&1 | tee "$output"
}

capture_artifact_path() {
    local base="$1"
    local suffix="${2:-}"
    local stem="${base%.*}"
    local ext=""

    if [[ "$base" == *.* ]]; then
        ext=".${base##*.}"
    else
        stem="$base"
    fi

    if [[ -n "$suffix" ]]; then
        printf '%s/%s-%s%s' "$CAPTURE_DIR" "$stem" "$suffix" "$ext"
        return
    fi
    printf '%s/%s' "$CAPTURE_DIR" "$base"
}

ensure_smoke_dirs() {
    mkdir -p "$CAPTURE_DIR" "$MANIFEST_DIR" "$KIND_DIR" "$STATE_DIR" "$TF_WORKDIR"
}

load_repo_env() {
    local existing_ts_api_client_id="${TS_API_CLIENT_ID-__unset__}"
    local existing_ts_api_client_secret="${TS_API_CLIENT_SECRET-__unset__}"
    local existing_smoke_issuer_url="${SMOKE_ISSUER_URL-__unset__}"
    local existing_smoke_ts_tag="${SMOKE_TS_TAG-__unset__}"
    local existing_smoke_name="${SMOKE_NAME-__unset__}"
    local existing_aws_region="${AWS_REGION-__unset__}"

    if [[ -f "${ROOT_DIR}/.env" ]]; then
        # shellcheck disable=SC1091
        set -a
        source "${ROOT_DIR}/.env"
        set +a
    fi

    if [[ "$existing_ts_api_client_id" != "__unset__" ]]; then
        export TS_API_CLIENT_ID="$existing_ts_api_client_id"
    fi
    if [[ "$existing_ts_api_client_secret" != "__unset__" ]]; then
        export TS_API_CLIENT_SECRET="$existing_ts_api_client_secret"
    fi
    if [[ "$existing_smoke_issuer_url" != "__unset__" ]]; then
        export SMOKE_ISSUER_URL="$existing_smoke_issuer_url"
    fi
    if [[ "$existing_smoke_ts_tag" != "__unset__" ]]; then
        export SMOKE_TS_TAG="$existing_smoke_ts_tag"
    fi
    if [[ "$existing_smoke_name" != "__unset__" ]]; then
        export SMOKE_NAME="$existing_smoke_name"
    fi
    if [[ "$existing_aws_region" != "__unset__" ]]; then
        export AWS_REGION="$existing_aws_region"
    fi

}

parse_issuer_url() {
    local remainder
    [[ -n "${SMOKE_ISSUER_URL:-}" ]] || fail "SMOKE_ISSUER_URL is required"
    [[ "$SMOKE_ISSUER_URL" == https://* ]] || fail "SMOKE_ISSUER_URL must start with https://"

    remainder="${SMOKE_ISSUER_URL#https://}"
    [[ -n "$remainder" ]] || fail "SMOKE_ISSUER_URL must include a host"
    [[ "$remainder" != */* ]] || fail "SMOKE_ISSUER_URL must be host-only with no path"
    [[ "$remainder" != *\?* ]] || fail "SMOKE_ISSUER_URL must not include a query string"
    [[ "$remainder" != *\#* ]] || fail "SMOKE_ISSUER_URL must not include a fragment"
    [[ "$remainder" != *@* ]] || fail "SMOKE_ISSUER_URL must not include userinfo"
    [[ "$remainder" != *:* ]] || fail "SMOKE_ISSUER_URL must not include an explicit port"

    ISSUER_HOST="$remainder"
}

sanitize_aws_session_name() {
    local raw="$1"
    printf '%s' "$raw" | tr -c 'A-Za-z0-9+=,.@-' '-' | cut -c1-64
}

set_smoke_defaults() {
    SMOKE_NAME="${SMOKE_NAME:-oidc-smoke}"
    AWS_REGION="${AWS_REGION:-us-east-1}"

    CLUSTER_NAME="$SMOKE_NAME"
    OIDC_NAMESPACE="${SMOKE_NAME}-system"
    APP_NAMESPACE="$SMOKE_NAME"
    APP_SERVICE_ACCOUNT="${SMOKE_NAME}-app"
    OAUTH_SECRET_NAME="${SMOKE_NAME}-tailscale-oauth"
    BRIDGE_RELEASE_NAME="${SMOKE_NAME}-bridge"
    BRIDGE_STATE_SECRET_NAME="${BRIDGE_RELEASE_NAME}-state"
    BRIDGE_LEASE_NAME="${BRIDGE_RELEASE_NAME}"
    PROOF_JOB_NAME="${SMOKE_NAME}-awscli"
    ROLE_NAME="${SMOKE_NAME}-role"
    BRIDGE_IMAGE_REPOSITORY="k8s-aws-oidc-smoke"
    BRIDGE_IMAGE_TAG="smoke-$(date -u +%Y%m%d%H%M%S)"
    BRIDGE_IMAGE="${BRIDGE_IMAGE_REPOSITORY}:${BRIDGE_IMAGE_TAG}"
    SUBJECT="system:serviceaccount:${APP_NAMESPACE}:${APP_SERVICE_ACCOUNT}"
    HOST_PRELIGHT_SESSION_NAME="$(sanitize_aws_session_name "${SMOKE_NAME}-preflight")"
    IN_CLUSTER_SESSION_NAME="$(sanitize_aws_session_name "${SMOKE_NAME}-job")"

    if ((${#ROLE_NAME} > 64)); then
        fail "derived IAM role name ${ROLE_NAME} exceeds AWS limit of 64 characters; shorten SMOKE_NAME"
    fi
}

write_runtime_env() {
    cat >"$RUNTIME_ENV_FILE" <<EOF
SMOKE_NAME=${SMOKE_NAME}
SMOKE_ISSUER_URL=${SMOKE_ISSUER_URL}
SMOKE_TS_TAG=${SMOKE_TS_TAG:-}
ISSUER_HOST=${ISSUER_HOST}
TS_HOSTNAME=${TS_HOSTNAME:-}
AWS_REGION=${AWS_REGION}
CLUSTER_NAME=${CLUSTER_NAME}
OIDC_NAMESPACE=${OIDC_NAMESPACE}
APP_NAMESPACE=${APP_NAMESPACE}
APP_SERVICE_ACCOUNT=${APP_SERVICE_ACCOUNT}
OAUTH_SECRET_NAME=${OAUTH_SECRET_NAME}
BRIDGE_RELEASE_NAME=${BRIDGE_RELEASE_NAME}
BRIDGE_STATE_SECRET_NAME=${BRIDGE_STATE_SECRET_NAME}
BRIDGE_LEASE_NAME=${BRIDGE_LEASE_NAME}
PROOF_JOB_NAME=${PROOF_JOB_NAME}
ROLE_NAME=${ROLE_NAME}
SUBJECT=${SUBJECT}
EOF
}

load_runtime_env_if_present() {
    if [[ -f "$RUNTIME_ENV_FILE" ]]; then
        # shellcheck disable=SC1090
        source "$RUNTIME_ENV_FILE"
    fi
}

assert_runtime_config_matches() {
    if [[ ! -f "$RUNTIME_ENV_FILE" ]]; then
        return 0
    fi

    local saved_smoke_name saved_smoke_issuer_url saved_smoke_ts_tag
    saved_smoke_name="$(grep '^SMOKE_NAME=' "$RUNTIME_ENV_FILE" | cut -d= -f2- || true)"
    saved_smoke_issuer_url="$(grep '^SMOKE_ISSUER_URL=' "$RUNTIME_ENV_FILE" | cut -d= -f2- || true)"
    saved_smoke_ts_tag="$(grep '^SMOKE_TS_TAG=' "$RUNTIME_ENV_FILE" | cut -d= -f2- || true)"

    if [[ "$saved_smoke_name" != "$SMOKE_NAME" || "$saved_smoke_issuer_url" != "$SMOKE_ISSUER_URL" || "$saved_smoke_ts_tag" != "$SMOKE_TS_TAG" ]]; then
        fail "tmp/smoke is already bound to SMOKE_NAME=${saved_smoke_name}, SMOKE_ISSUER_URL=${saved_smoke_issuer_url}, and SMOKE_TS_TAG=${saved_smoke_ts_tag}; run 'just down' before changing smoke config"
    fi
}

require_smoke_env_for_up() {
    [[ -n "${TS_API_CLIENT_ID:-}" ]] || fail "TS_API_CLIENT_ID is required; set it in the shell or repo-root .env"
    [[ -n "${TS_API_CLIENT_SECRET:-}" ]] || fail "TS_API_CLIENT_SECRET is required; set it in the shell or repo-root .env"
    [[ -n "${SMOKE_TS_TAG:-}" ]] || fail "SMOKE_TS_TAG is required; set it in the shell or repo-root .env"
}

load_up_config() {
    load_repo_env
    parse_issuer_url
    set_smoke_defaults
    require_smoke_env_for_up
}

load_down_config() {
    load_repo_env
    if [[ -f "$RUNTIME_ENV_FILE" ]]; then
        load_runtime_env_if_present
        parse_issuer_url
        SMOKE_NAME="${SMOKE_NAME:-oidc-smoke}"
        AWS_REGION="${AWS_REGION:-us-east-1}"
        CLUSTER_NAME="${CLUSTER_NAME:-$SMOKE_NAME}"
        OIDC_NAMESPACE="${OIDC_NAMESPACE:-${SMOKE_NAME}-system}"
        APP_NAMESPACE="${APP_NAMESPACE:-$SMOKE_NAME}"
        APP_SERVICE_ACCOUNT="${APP_SERVICE_ACCOUNT:-${SMOKE_NAME}-app}"
        OAUTH_SECRET_NAME="${OAUTH_SECRET_NAME:-${SMOKE_NAME}-tailscale-oauth}"
        BRIDGE_RELEASE_NAME="${BRIDGE_RELEASE_NAME:-${SMOKE_NAME}-bridge}"
        BRIDGE_STATE_SECRET_NAME="${BRIDGE_STATE_SECRET_NAME:-${BRIDGE_RELEASE_NAME}-state}"
        BRIDGE_LEASE_NAME="${BRIDGE_LEASE_NAME:-${BRIDGE_RELEASE_NAME}}"
        PROOF_JOB_NAME="${PROOF_JOB_NAME:-${SMOKE_NAME}-awscli}"
        ROLE_NAME="${ROLE_NAME:-${SMOKE_NAME}-role}"
        SUBJECT="${SUBJECT:-system:serviceaccount:${APP_NAMESPACE}:${APP_SERVICE_ACCOUNT}}"
        HOST_PRELIGHT_SESSION_NAME="$(sanitize_aws_session_name "${SMOKE_NAME}-preflight")"
        IN_CLUSTER_SESSION_NAME="$(sanitize_aws_session_name "${SMOKE_NAME}-job")"
        return
    fi

    parse_issuer_url
    set_smoke_defaults
}

validate_up_prereqs() {
    require_cmd aws
    require_cmd curl
    require_cmd docker
    require_cmd helm
    require_cmd jq
    require_cmd kind
    require_cmd kubectl
    require_cmd tailscale
    require_cmd tofu
}

validate_down_prereqs() {
    require_cmd kind
    if terraform_state_exists; then
        require_cmd aws
        require_cmd tofu
    fi
}

validate_failover_prereqs() {
    require_cmd aws
    require_cmd curl
    require_cmd docker
    require_cmd helm
    require_cmd jq
    require_cmd kind
    require_cmd kubectl
}

capture_aws_identity() {
    capture_cmd "${CAPTURE_DIR}/aws-caller-identity.json" aws sts get-caller-identity --output json
    ACCOUNT_ID="$(jq -r '.Account' "${CAPTURE_DIR}/aws-caller-identity.json")"
    [[ -n "$ACCOUNT_ID" && "$ACCOUNT_ID" != "null" ]] || fail "unable to determine AWS account ID from ambient credentials"
    PROVIDER_ARN="arn:aws:iam::${ACCOUNT_ID}:oidc-provider/${ISSUER_HOST}"
    ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"
}

capture_tailscale_status() {
    capture_cmd "${CAPTURE_DIR}/tailscale-status.json" tailscale status --json
    MAGIC_DNS_SUFFIX="$(jq -r '.MagicDNSSuffix // empty' "${CAPTURE_DIR}/tailscale-status.json")"
    [[ -n "$MAGIC_DNS_SUFFIX" ]] || fail "tailscale status did not report MagicDNSSuffix; confirm the local client is logged in"
    [[ "$ISSUER_HOST" == *".${MAGIC_DNS_SUFFIX}" ]] || fail "SMOKE_ISSUER_URL host ${ISSUER_HOST} must end with .${MAGIC_DNS_SUFFIX}"
    TS_HOSTNAME="${ISSUER_HOST%."${MAGIC_DNS_SUFFIX}"}"
    [[ -n "$TS_HOSTNAME" ]] || fail "unable to derive TS hostname from ${ISSUER_HOST}"
    [[ "$TS_HOSTNAME" != *.* ]] || fail "derived TS hostname ${TS_HOSTNAME} is invalid; use a single-label Tailscale hostname"
}

cluster_exists() {
    kind get clusters | grep -Fxq "$CLUSTER_NAME"
}

terraform_state_exists() {
    [[ -f "${TF_WORKDIR}/terraform.tfstate" || -f "${TF_WORKDIR}/terraform.tfstate.backup" ]]
}

assert_no_unmanaged_aws_resources() {
    if terraform_state_exists; then
        return 0
    fi

    if aws iam get-open-id-connect-provider --open-id-connect-provider-arn "$PROVIDER_ARN" >/dev/null 2>&1; then
        fail "AWS IAM OIDC provider ${PROVIDER_ARN} already exists but tmp/smoke has no Terraform state; remove it manually or restore the old tmp/smoke state before running just up"
    fi
    if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
        fail "AWS IAM role ${ROLE_NAME} already exists but tmp/smoke has no Terraform state; remove it manually or restore the old tmp/smoke state before running just up"
    fi
}

export_kubeconfig() {
    if cluster_exists; then
        kind export kubeconfig --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG_PATH" >/dev/null
        export KUBECONFIG="$KUBECONFIG_PATH"
    fi
}

write_kind_config() {
    cat >"${KIND_DIR}/config.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        service-account-issuer: ${SMOKE_ISSUER_URL}
        api-audiences: https://kubernetes.default.svc.cluster.local,sts.amazonaws.com
EOF
}

wait_for_http() {
    local url="$1"
    local output="$2"
    local attempts="$3"
    local sleep_seconds="$4"
    local attempt

    for attempt in $(seq 1 "$attempts"); do
        if curl -fsS "$url" -o "$output"; then
            return 0
        fi
        log "waiting for ${url} (attempt ${attempt}/${attempts})"
        sleep "$sleep_seconds"
    done

    return 1
}

jwks_uri() {
    printf '%s/openid/v1/jwks' "$SMOKE_ISSUER_URL"
}

bridge_selector() {
    printf 'app.kubernetes.io/name=k8s-aws-oidc-chart,app.kubernetes.io/instance=%s' "$BRIDGE_RELEASE_NAME"
}

bridge_pod_names() {
    kubectl -n "$OIDC_NAMESPACE" get pods -l "$(bridge_selector)" -o json | \
        jq -r '.items[] | select(.metadata.deletionTimestamp == null) | .metadata.name'
}

fetch_http_with_status() {
    local url="$1"
    local output="$2"
    local status_output="$3"
    local status

    status="$(curl -sS -o "$output" -w '%{http_code}' "$url" || true)"
    printf '%s\n' "$status" >"$status_output"
    [[ "$status" != "000" ]]
}

wait_for_available_replicas() {
    local expected_replicas="$1"
    local attempts="${2:-90}"
    local sleep_seconds="${3:-2}"
    local attempt

    for attempt in $(seq 1 "$attempts"); do
        if kubectl -n "$OIDC_NAMESPACE" get deployment "$BRIDGE_RELEASE_NAME" -o jsonpath='{.status.availableReplicas}' 2>/dev/null | grep -qx "$expected_replicas"; then
            return 0
        fi
        sleep "$sleep_seconds"
    done

    return 1
}

check_funnel_tag_error() {
    local needle='Funnel not available; "funnel" node attribute not set'
    local pod

    while read -r pod; do
        [[ -n "$pod" ]] || continue
        if kubectl -n "$OIDC_NAMESPACE" logs "$pod" --tail=200 2>/dev/null | grep -Fq "$needle"; then
            fail "bridge cannot enable Funnel for ${SMOKE_TS_TAG}; the tag is missing the funnel node attribute"
        fi
    done < <(bridge_pod_names)
}

build_bridge_image() {
    rm -rf "$BUILD_CONTEXT_DIR"
    mkdir -p "$BUILD_CONTEXT_DIR"
    cp "${ROOT_DIR}/go.mod" "${ROOT_DIR}/go.sum" "$BUILD_CONTEXT_DIR/"
    cp -R "${ROOT_DIR}/cmd" "${ROOT_DIR}/internal" "$BUILD_CONTEXT_DIR/"
    capture_cmd "$(capture_artifact_path bridge-image-build.log)" docker build -f "${ROOT_DIR}/hack/smoke/Dockerfile" -t "$BRIDGE_IMAGE" "$BUILD_CONTEXT_DIR"
    capture_cmd "$(capture_artifact_path kind-load-image.log)" kind load docker-image --name "$CLUSTER_NAME" "$BRIDGE_IMAGE"
}

write_bridge_values() {
    local output_path="$1"
    local replica_count="${2:-1}"
    local leader_election_enabled="${3:-false}"
    local pdb_enabled="${4:-false}"

    cat >"${output_path}" <<EOF
issuerUrl: ${SMOKE_ISSUER_URL}
fullnameOverride: ${BRIDGE_RELEASE_NAME}
replicaCount: ${replica_count}
logLevel: debug
image:
  repository: ${BRIDGE_IMAGE_REPOSITORY}
  tag: ${BRIDGE_IMAGE_TAG}
leaderElection:
  enabled: ${leader_election_enabled}
podDisruptionBudget:
  enabled: ${pdb_enabled}
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
    local values_file="$1"
    local expected_replicas="${2:-1}"
    local suffix="${3:-}"
    local helm_log rollout_log

    helm_log="$(capture_artifact_path helm-upgrade.log "$suffix")"
    rollout_log="$(capture_artifact_path bridge-rollout.log "$suffix")"

    capture_cmd "$helm_log" \
        helm upgrade --install "$BRIDGE_RELEASE_NAME" "${ROOT_DIR}/chart" \
        --namespace "$OIDC_NAMESPACE" \
        -f "$values_file"

    local attempt
    for attempt in $(seq 1 90); do
        if kubectl -n "$OIDC_NAMESPACE" get deployment "$BRIDGE_RELEASE_NAME" -o jsonpath='{.status.availableReplicas}' 2>/dev/null | grep -qx "$expected_replicas"; then
            break
        fi
        check_funnel_tag_error
        sleep 2
    done

    capture_cmd "$rollout_log" kubectl -n "$OIDC_NAMESPACE" rollout status deployment/"$BRIDGE_RELEASE_NAME" --timeout=300s
    capture_bridge_logs "$suffix"
}

verify_bridge_endpoints() {
    local suffix="${1:-}"
    local discovery_public discovery_internal jwks_public jwks_internal
    local public_kids internal_kids kid_diff

    discovery_public="$(capture_artifact_path discovery-public.json "$suffix")"
    discovery_internal="$(capture_artifact_path discovery-internal.json "$suffix")"
    jwks_public="$(capture_artifact_path jwks-public.json "$suffix")"
    jwks_internal="$(capture_artifact_path jwks-internal.json "$suffix")"
    public_kids="$(capture_artifact_path jwks-public-kids.txt "$suffix")"
    internal_kids="$(capture_artifact_path jwks-internal-kids.txt "$suffix")"
    kid_diff="$(capture_artifact_path jwks-kid-diff.txt "$suffix")"

    wait_for_http "${SMOKE_ISSUER_URL}/.well-known/openid-configuration" "$discovery_public" 60 5 || fail "public discovery endpoint did not become reachable"
    wait_for_http "$(jwks_uri)" "$jwks_public" 30 5 || fail "public JWKS endpoint did not become reachable"

    kubectl get --raw /.well-known/openid-configuration >"$discovery_internal"
    kubectl get --raw /openid/v1/jwks >"$jwks_internal"

    [[ "$(jq -r '.issuer' "$discovery_public")" == "$SMOKE_ISSUER_URL" ]] || fail "public discovery issuer does not match SMOKE_ISSUER_URL"
    [[ "$(jq -r '.jwks_uri' "$discovery_public")" == "$(jwks_uri)" ]] || fail "public discovery jwks_uri does not match expected value"

    jq -r '.keys[].kid' "$jwks_public" | sort >"$public_kids"
    jq -r '.keys[].kid' "$jwks_internal" | sort >"$internal_kids"
    diff -u "$internal_kids" "$public_kids" >"$kid_diff"
}

host_side_preflight() {
    local suffix="${1:-}"
    local token_file="${STATE_DIR}/web-identity-token.jwt"
    local stderr_file output_file
    local attempt

    stderr_file="$(capture_artifact_path sts-preflight.err "$suffix")"
    output_file="$(capture_artifact_path sts-preflight.json "$suffix")"

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
            aws sts get-caller-identity --output json >"$output_file" 2>"$stderr_file"; then
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
    local suffix="${1:-}"
    local attempts="${2:-3}"
    local sleep_seconds="${3:-5}"
    local wait_log output_file error_file
    local attempt

    wait_log="$(capture_artifact_path awscli-proof-wait.log "$suffix")"
    output_file="$(capture_artifact_path awscli-proof.json "$suffix")"
    error_file="$(capture_artifact_path awscli-proof.err "$suffix")"

    write_proof_manifest

    for attempt in $(seq 1 "$attempts"); do
        kubectl -n "$APP_NAMESPACE" delete job "$PROOF_JOB_NAME" --ignore-not-found >/dev/null
        kubectl apply -f "${MANIFEST_DIR}/awscli-proof-job.yaml" >/dev/null

        if capture_cmd "$wait_log" kubectl -n "$APP_NAMESPACE" wait --for=condition=complete --timeout=300s job/"$PROOF_JOB_NAME"; then
            kubectl -n "$APP_NAMESPACE" logs job/"$PROOF_JOB_NAME" >"$output_file"
            if [[ "$(jq -r '.roleArn' "$output_file")" == "$ROLE_ARN" ]] && \
                jq -r '.callerArn' "$output_file" | grep -Fq ":assumed-role/${ROLE_NAME}/"; then
                return 0
            fi
        else
            kubectl -n "$APP_NAMESPACE" logs job/"$PROOF_JOB_NAME" >"$error_file" 2>/dev/null || true
        fi

        if [[ "$attempt" -lt "$attempts" ]]; then
            log "retrying in-cluster proof (attempt ${attempt}/${attempts})"
            sleep "$sleep_seconds"
            continue
        fi
    done

    if [[ -f "$output_file" ]]; then
        [[ "$(jq -r '.roleArn' "$output_file")" == "$ROLE_ARN" ]] || fail "in-cluster proof returned an unexpected role ARN"
        jq -r '.callerArn' "$output_file" | grep -Fq ":assumed-role/${ROLE_NAME}/" || fail "in-cluster proof did not return an assumed-role ARN"
    fi
    fail "in-cluster proof did not succeed; see ${wait_log} and ${error_file}"
}

capture_internal_metrics() {
    local suffix="${1:-}"
    local local_port=38080
    local proxy_log metrics_output
    local proxy_pid=""
    local metrics_url="http://127.0.0.1:${local_port}/api/v1/namespaces/${OIDC_NAMESPACE}/services/http:${BRIDGE_RELEASE_NAME}:metrics/proxy/metrics"

    proxy_log="$(capture_artifact_path metrics-service-proxy.log "$suffix")"
    metrics_output="$(capture_artifact_path metrics-internal.prom "$suffix")"

    rm -f "$proxy_log"
    kubectl proxy --port="${local_port}" >"$proxy_log" 2>&1 &
    proxy_pid=$!

    cleanup() {
        if [[ -n "${proxy_pid:-}" ]]; then
            kill "$proxy_pid" >/dev/null 2>&1 || true
            wait "$proxy_pid" 2>/dev/null || true
        fi
    }
    trap cleanup RETURN

    wait_for_http "$metrics_url" "$metrics_output" 30 1 || fail "internal metrics endpoint did not become reachable"

    for family in \
        oidc_proxy_build_info \
        oidc_proxy_jwks_prime_total \
        oidc_proxy_process_start_time_seconds \
        oidc_proxy_leader \
        oidc_proxy_public_ready
    do
        grep -Fq "$family" "$metrics_output" || fail "metrics scrape is missing ${family}"
    done

    trap - RETURN
    cleanup
}

capture_kubernetes_state() {
    local suffix="${1:-}"
    kubectl -n "$OIDC_NAMESPACE" get all,secret,serviceaccount,role,rolebinding,lease,poddisruptionbudget >"$(capture_artifact_path oidc-system-resources.txt "$suffix")"
    kubectl -n "$APP_NAMESPACE" get all,secret,serviceaccount,job >"$(capture_artifact_path app-resources.txt "$suffix")"
}

capture_oidc_events() {
    local suffix="${1:-}"
    kubectl -n "$OIDC_NAMESPACE" get events --sort-by=.lastTimestamp >"$(capture_artifact_path oidc-events.txt "$suffix")"
}

capture_bridge_logs() {
    local suffix="${1:-}"
    local pod

    while read -r pod; do
        [[ -n "$pod" ]] || continue
        kubectl -n "$OIDC_NAMESPACE" logs "$pod" >"$(capture_artifact_path "bridge-${pod}.log" "$suffix")" || true
        kubectl -n "$OIDC_NAMESPACE" describe pod "$pod" >"$(capture_artifact_path "bridge-${pod}-describe.txt" "$suffix")" || true
    done < <(bridge_pod_names)
}

capture_bridge_pod_endpoints() {
    local suffix="$1"
    local pod
    local index=0

    while read -r pod; do
        [[ -n "$pod" ]] || continue
        local port=$((38100 + index))
        local pf_log pf_pid=""
        local livez_body livez_status readyz_body readyz_status leaderz_body leaderz_status metrics_output

        pf_log="$(capture_artifact_path "bridge-${pod}-port-forward.log" "$suffix")"
        livez_body="$(capture_artifact_path "bridge-${pod}-livez.txt" "$suffix")"
        livez_status="$(capture_artifact_path "bridge-${pod}-livez.status" "$suffix")"
        readyz_body="$(capture_artifact_path "bridge-${pod}-readyz.txt" "$suffix")"
        readyz_status="$(capture_artifact_path "bridge-${pod}-readyz.status" "$suffix")"
        leaderz_body="$(capture_artifact_path "bridge-${pod}-leaderz.txt" "$suffix")"
        leaderz_status="$(capture_artifact_path "bridge-${pod}-leaderz.status" "$suffix")"
        metrics_output="$(capture_artifact_path "bridge-${pod}-metrics.prom" "$suffix")"

        rm -f "$pf_log"
        kubectl -n "$OIDC_NAMESPACE" port-forward "pod/${pod}" --address 127.0.0.1 "${port}:8080" >"$pf_log" 2>&1 &
        pf_pid=$!

        if ! wait_for_http "http://127.0.0.1:${port}/livez" "$livez_body" 30 1; then
            kill "$pf_pid" >/dev/null 2>&1 || true
            wait "$pf_pid" 2>/dev/null || true
            fail "pod ${pod} internal health listener did not become reachable"
        fi
        printf '200\n' >"$livez_status"
        fetch_http_with_status "http://127.0.0.1:${port}/readyz" "$readyz_body" "$readyz_status" || fail "unable to query /readyz for pod ${pod}"
        fetch_http_with_status "http://127.0.0.1:${port}/leaderz" "$leaderz_body" "$leaderz_status" || fail "unable to query /leaderz for pod ${pod}"
        wait_for_http "http://127.0.0.1:${port}/metrics" "$metrics_output" 30 1 || fail "metrics endpoint for pod ${pod} did not become reachable"

        kill "$pf_pid" >/dev/null 2>&1 || true
        wait "$pf_pid" 2>/dev/null || true
        index=$((index + 1))
    done < <(bridge_pod_names)
}

current_leader_identity() {
    kubectl -n "$OIDC_NAMESPACE" get lease "$BRIDGE_LEASE_NAME" -o jsonpath='{.spec.holderIdentity}'
}

wait_for_new_leader() {
    local previous_leader="$1"
    local attempts="${2:-60}"
    local sleep_seconds="${3:-2}"
    local attempt
    local current

    for attempt in $(seq 1 "$attempts"); do
        current="$(current_leader_identity 2>/dev/null || true)"
        if [[ -n "$current" && "$current" != "$previous_leader" ]]; then
            printf '%s\n' "$current"
            return 0
        fi
        printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "waiting for new leader (attempt ${attempt}/${attempts})" >&2
        sleep "$sleep_seconds"
    done

    return 1
}

capture_bridge_debug_state() {
    local suffix="$1"

    printf '%s\n' "$(current_leader_identity)" >"$(capture_artifact_path leader.txt "$suffix")"
    kubectl -n "$OIDC_NAMESPACE" get lease "$BRIDGE_LEASE_NAME" -o json >"$(capture_artifact_path lease.json "$suffix")"
    kubectl -n "$OIDC_NAMESPACE" get pods -l "$(bridge_selector)" -o wide >"$(capture_artifact_path bridge-pods.txt "$suffix")"
    capture_bridge_logs "$suffix"
    capture_bridge_pod_endpoints "$suffix"
    capture_kubernetes_state "$suffix"
    capture_oidc_events "$suffix"
}

extract_pod_name_from_status_file() {
    local file="$1"
    local suffix="$2"
    local name

    name="$(basename "$file")"
    name="${name#bridge-}"
    name="${name%-leaderz-${suffix}.status}"
    printf '%s\n' "$name"
}

assert_single_leader_snapshot() {
    local suffix="$1"
    local expected_leader="$2"
    local ok_count=0
    local ok_pod=""
    local file leader_metrics

    for file in "$CAPTURE_DIR"/bridge-*-leaderz-"$suffix".status; do
        [[ -e "$file" ]] || fail "no leaderz status captures found for snapshot ${suffix}"
        if [[ "$(tr -d '\n' <"$file")" == "200" ]]; then
            ok_count=$((ok_count + 1))
            ok_pod="$(extract_pod_name_from_status_file "$file" "$suffix")"
        fi
    done

    [[ "$ok_count" -eq 1 ]] || fail "expected exactly one leader in snapshot ${suffix}, found ${ok_count}"
    [[ "$ok_pod" == "$expected_leader" ]] || fail "expected leader ${expected_leader} in snapshot ${suffix}, got ${ok_pod}"

    leader_metrics="$(capture_artifact_path "bridge-${expected_leader}-metrics.prom" "$suffix")"
    grep -Fxq 'oidc_proxy_leader 1' "$leader_metrics" || fail "leader metrics for ${expected_leader} do not report oidc_proxy_leader 1"
    grep -Fxq 'oidc_proxy_public_ready 1' "$leader_metrics" || fail "leader metrics for ${expected_leader} do not report oidc_proxy_public_ready 1"

    for file in "$CAPTURE_DIR"/bridge-*-metrics-"$suffix".prom; do
        [[ -e "$file" ]] || continue
        if [[ "$file" == "$leader_metrics" ]]; then
            continue
        fi
        grep -Fxq 'oidc_proxy_leader 0' "$file" || fail "follower metrics in $(basename "$file") do not report oidc_proxy_leader 0"
    done
}
