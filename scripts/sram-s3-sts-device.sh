#!/usr/bin/env bash
# OIDC Device Code Flow -> broker JWT -> STS credentials (WebIdentity).
# Uses broker proxies: POST /oidc/device, POST /oidc/token, POST /cli/device-sts
#
# NOTE: prefer the Go binary (sram-s3) for the device code flow. This script
# is kept as a legacy/dev alternative for environments where Go is not available.

IS_SOURCED=0
if [ -n "${ZSH_VERSION:-}" ]; then
  case ${ZSH_EVAL_CONTEXT:-} in *:file) IS_SOURCED=1 ;; esac
elif [ -n "${BASH_VERSION:-}" ]; then
  if [ "${BASH_SOURCE[0]}" != "$0" ]; then
    IS_SOURCED=1
  fi
fi

die() {
  echo "ERROR: $*" >&2
  return 1
}

usage() {
  cat <<'EOF'
Usage: sram-s3-sts-device.sh [options]

OIDC Device Authorization Grant (RFC 8628) via the role broker, then STS credentials.

Options:
  -h                Show help
  -d                Debug output
  -f <dotenv_file>  Load env file (default: .env if present)
  -b <broker_url>   SRAM_BROKER_URL
  -p <project>      Optional: <marker>[:members|admin] (must match your entitlements)

Environment:
  SRAM_BROKER_URL (required), SRAM_PROJECT (optional), OIDC_ENTITLEMENTS_CLAIM (optional)

The broker injects upstream OIDC client_id when proxying /oidc/device and /oidc/token.
Upstream IdP must advertise device_authorization_endpoint (broker proxies /oidc/device).
EOF
}

DOTENV_FILE="${DOTENV_FILE-}"
DEBUG_STS_SCRIPT="${DEBUG_STS_SCRIPT:-0}"
CLI_BROKER_URL=""
CLI_PROJECT=""
OPTIND=1
while getopts ":hdf:b:p:c:s:" opt; do
  case "$opt" in
    h)
      usage
      if [ "$IS_SOURCED" -eq 1 ]; then return 0; fi
      exit 0
      ;;
    d) DEBUG_STS_SCRIPT=1 ;;
    f) DOTENV_FILE="$OPTARG" ;;
    b) CLI_BROKER_URL="$OPTARG" ;;
    p) CLI_PROJECT="$OPTARG" ;;
    :)
      die "Option -$OPTARG requires a value"
      return 1 2>/dev/null || exit 1
      ;;
    \?)
      die "Unknown option: -$OPTARG"
      return 1 2>/dev/null || exit 1
      ;;
  esac
done
shift $((OPTIND - 1))

if [ -n "${ZSH_VERSION:-}" ]; then
  _SRAM_CLI_DIR="$(cd "$(dirname "${(%):-%x}")" && pwd)"
else
  _SRAM_CLI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
fi
# shellcheck source=scripts/cli-common.sh
. "${_SRAM_CLI_DIR}/cli-common.sh"

sram_cli_load_dotenv "${DOTENV_FILE}"

B="${CLI_BROKER_URL:-${SRAM_BROKER_URL:-${OIDC_ISSUER:-}}}"
B="${B%/}"
T="${CLI_PROJECT:-${SRAM_PROJECT:-${SRAM_CLI_TARGET:-}}}"
ENT_CLAIM="${OIDC_ENTITLEMENTS_CLAIM:-eduperson_entitlement}"
SCOPE="openid profile email groups ${ENT_CLAIM}"

if [ -z "${B}" ]; then
  die "Missing broker URL: set SRAM_BROKER_URL or pass -b"
  return 1 2>/dev/null || exit 1
fi

if [ "${DEBUG_STS_SCRIPT}" = "1" ]; then
  echo "DEBUG: broker=${B} target=${T} scope=${SCOPE}" >&2
fi

device_body="scope=$(printf '%s' "$SCOPE" | jq -sRr @uri)"
device_json=$(curl --http1.1 --connect-timeout 10 --max-time 60 -sS -w $'\n%{http_code}' -X POST "${B}/oidc/device" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "${device_body}") || { die "POST ${B}/oidc/device failed (connection error)"; return 1 2>/dev/null || exit 1; }
device_http="${device_json##*$'\n'}"
device_json="${device_json%$'\n'*}"

if [ "${device_http}" -lt 200 ] || [ "${device_http}" -ge 300 ]; then
  echo "ERROR: POST ${B}/oidc/device returned HTTP ${device_http}:" >&2
  echo "${device_json}" | jq -c '.' 2>/dev/null || echo "${device_json}" >&2
  echo "Hint: upstream IdP must support RFC 8628 device authorization (device_authorization_endpoint in discovery)." >&2
  if [ "$IS_SOURCED" -eq 1 ]; then return 1; fi
  exit 1
fi

device_code=$(echo "${device_json}" | jq -r '.device_code // empty')
user_code=$(echo "${device_json}" | jq -r '.user_code // empty')
verification_uri=$(echo "${device_json}" | jq -r '.verification_uri // empty')
verification_uri_complete=$(echo "${device_json}" | jq -r '.verification_uri_complete // empty')
interval=$(echo "${device_json}" | jq -r '.interval // 5')
expires_in=$(echo "${device_json}" | jq -r '.expires_in // 600')

if [ -z "${device_code}" ]; then
  die "Device authorization response missing device_code"
  return 1 2>/dev/null || exit 1
fi

if [ -n "${verification_uri_complete}" ] && [ "${verification_uri_complete}" != "null" ]; then
  echo "Open: ${verification_uri_complete}" >&2
  sram_cli_open_url "${verification_uri_complete}"
elif [ -n "${verification_uri}" ] && [ "${verification_uri}" != "null" ]; then
  echo "Open: ${verification_uri}" >&2
  sram_cli_open_url "${verification_uri}"
  echo "Enter code: ${user_code}" >&2
else
  echo "Device code: ${user_code}" >&2
fi
echo "Complete sign-in in the browser, then this script will poll for tokens (expires in ${expires_in}s)." >&2

poll_interval="${interval}"
if ! [[ "${poll_interval}" =~ ^[0-9]+$ ]]; then
  poll_interval=5
fi
deadline=$(( $(date +%s) + expires_in ))
access_token=""
poll_count=0

while [ "$(date +%s)" -lt "${deadline}" ]; do
  poll_count=$(( poll_count + 1 ))
  if [ "${poll_count}" -eq 1 ] || [ $(( poll_count % 6 )) -eq 0 ]; then
    remaining=$(( deadline - $(date +%s) ))
    echo "Waiting for browser sign-in… (${remaining}s left, poll #${poll_count})" >&2
  fi
  token_body="grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code&device_code=$(printf '%s' "$device_code" | jq -sRr @uri)"
  token_json=$(curl --http1.1 --connect-timeout 10 --max-time 60 -sS -w $'\n%{http_code}' -X POST "${B}/oidc/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "${token_body}") || { die "POST ${B}/oidc/token failed (connection error)"; return 1 2>/dev/null || exit 1; }
  token_http="${token_json##*$'\n'}"
  token_json="${token_json%$'\n'*}"

  if [ "${token_http}" -ge 200 ] && [ "${token_http}" -lt 300 ]; then
    access_token=$(echo "${token_json}" | jq -r '.access_token // .id_token // empty')
    [ -n "${access_token}" ] && break
    die "Token response missing access_token"
    return 1 2>/dev/null || exit 1
  fi

  err=$(echo "${token_json}" | jq -r '.error // empty' 2>/dev/null || true)
  case "${err}" in
    authorization_pending)
      sleep "${poll_interval}"
      ;;
    slow_down)
      poll_interval=$(( poll_interval + 2 ))
      sleep "${poll_interval}"
      ;;
    access_denied|expired_token)
      echo "ERROR: device flow ended: ${err}" >&2
      echo "${token_json}" | jq -c '.' 2>/dev/null || echo "${token_json}" >&2
      if [ "$IS_SOURCED" -eq 1 ]; then return 1; fi
      exit 1
      ;;
    *)
      echo "ERROR: POST ${B}/oidc/token returned HTTP ${token_http}:" >&2
      echo "${token_json}" | jq -c '.' 2>/dev/null || echo "${token_json}" >&2
      if [ "$IS_SOURCED" -eq 1 ]; then return 1; fi
      exit 1
      ;;
  esac
done

if [ -z "${access_token}" ]; then
  die "Device authorization timed out before tokens were issued"
  return 1 2>/dev/null || exit 1
fi

_sram_post_device_sts() {
  curl --http1.1 --connect-timeout 10 --max-time 60 -sS -w $'\n%{http_code}' -X POST "${B}/cli/device-sts" \
    -H "Content-Type: application/json" \
    -d "$1"
}

if [ -n "${T}" ]; then
  sts_payload="$(jq -cn --arg t "${T}" --arg tok "${access_token}" '{target:$t,access_token:$tok}')"
else
  sts_payload="$(jq -cn --arg tok "${access_token}" '{access_token:$tok}')"
fi

sts_json=$(_sram_post_device_sts "${sts_payload}") || { die "POST ${B}/cli/device-sts failed (connection error)"; return 1 2>/dev/null || exit 1; }
sts_http="${sts_json##*$'\n'}"
sts_json="${sts_json%$'\n'*}"

if [ "${sts_http}" -lt 200 ] || [ "${sts_http}" -ge 300 ]; then
  sram_cli_print_sts_error "${B}/cli/device-sts" "${sts_http}" "${sts_json}" " (target=${T:-auto})"
  sram_cli_hint_old_broker "${sts_json}" || true
  if [ "$IS_SOURCED" -eq 1 ]; then return 1; fi
  exit 1
fi

if echo "${sts_json}" | jq -e '.status == "choose"' >/dev/null 2>&1; then
  if ! T="$(sram_cli_pick_target "${sts_json}")"; then
    die "role selection failed"
    return 1 2>/dev/null || exit 1
  fi
  if [ -z "${T}" ]; then
    die "role selection failed (no target chosen)"
    return 1 2>/dev/null || exit 1
  fi
  sts_payload="$(jq -cn --arg t "${T}" --arg tok "${access_token}" '{target:$t,access_token:$tok}')"
  sts_json=$(_sram_post_device_sts "${sts_payload}") || { die "POST ${B}/cli/device-sts failed (connection error)"; return 1 2>/dev/null || exit 1; }
  sts_http="${sts_json##*$'\n'}"
  sts_json="${sts_json%$'\n'*}"
  if [ "${sts_http}" -lt 200 ] || [ "${sts_http}" -ge 300 ]; then
    sram_cli_print_sts_error "${B}/cli/device-sts" "${sts_http}" "${sts_json}" " (target=${T})"
    if [ "$IS_SOURCED" -eq 1 ]; then return 1; fi
    exit 1
  fi
fi

if ! echo "${sts_json}" | jq -e '.Credentials.AccessKeyId' >/dev/null 2>&1; then
  echo "ERROR: broker did not return STS credentials:" >&2
  echo "${sts_json}" | jq -c '.' 2>/dev/null || echo "${sts_json}" >&2
  if [ "$IS_SOURCED" -eq 1 ]; then return 1; fi
  exit 1
fi

sram_cli_export_aws_env "${sts_json}"
sram_cli_unexport_aws_secrets

if [ "${IS_SOURCED}" != "1" ]; then
  echo "Session ready (device code flow)."
  echo "${sts_json}" | jq -c '.Credentials'
else
  echo "Done — session credentials are set (device code flow). Use: aws s3 ls"
  echo "  (aws is wrapped to pass credentials without exporting secrets to zsh.)"
fi
