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
