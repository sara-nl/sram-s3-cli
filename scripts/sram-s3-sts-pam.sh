#!/usr/bin/env bash
# Minimal PAM weblogin -> STS credentials via role-broker.
# Works both as:
#   ./scripts/sram-s3-sts-pam.sh
# and:
#   source ./scripts/sram-s3-sts-pam.sh

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
Usage: sram-s3-sts-pam.sh [options]

Options:
  -h                Show help
  -d                Debug output (prints session/payload details)
  -f <dotenv_file>  Load env file (default: .env if present)
  -b <broker_url>   SRAM_BROKER_URL (e.g. https://sram-s3.playground.sdp.surf.nl)
  -p <project>      Optional: <marker>[:members|admin] (must match your entitlements)
  -u <pam_user_id>  PAM_USER_ID used for pam-weblogin/start
  -r <pam_rhost>    PAM_RHOST (default: hostname)

You can provide values via options OR environment variables:
  SRAM_BROKER_URL, SRAM_PROJECT (or SRAM_CLI_TARGET), PAM_USER_ID, PAM_RHOST

On success, exports:
  AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_SESSION_EXPIRATION
and prints the STS Credentials JSON to stdout.
EOF
}

strip_pam_heavy_fields() {
  PAYLOAD="$1" python3 - <<'PY'
import os, re
s = os.environ.get("PAYLOAD", "")
s = re.sub(r',\s*"challenge"\s*:\s*"[^"]*"', '', s, flags=re.S)
s = re.sub(r',\s*"qr_code_ascii"\s*:\s*"[^"]*"', '', s, flags=re.S)
s = re.sub(r',\s*"qr_code_png"\s*:\s*"[^"]*"', '', s, flags=re.S)
print(s, end="")
PY
}

extract_json_string_field_loose() {
  PAYLOAD="$1" FIELD="$2" python3 - <<'PY'
import os, re
s = os.environ.get("PAYLOAD", "")
f = re.escape(os.environ.get("FIELD", ""))
m = re.search(r'"' + f + r'"\s*:\s*"([^"]*)"', s, re.S)
print(m.group(1) if m else "", end="")
PY
}

decode_json_escaped_string() {
  RAW="$1" python3 - <<'PY'
import os
s = os.environ.get("RAW", "")
try:
    print(bytes(s, "utf-8").decode("unicode_escape"), end="")
except Exception:
    print(s, end="")
PY
}

DOTENV_FILE="${DOTENV_FILE-}"
DEBUG_STS_SCRIPT="${DEBUG_STS_SCRIPT:-0}"
NO_EXECUTE_PAM_STS="${NO_EXECUTE_PAM_STS:-0}"
CLI_BROKER_URL=""
CLI_PROJECT=""
CLI_PAM_USER=""
CLI_PAM_RHOST=""
OPTIND=1
while getopts ":hdf:b:p:u:r:" opt; do
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
    u) CLI_PAM_USER="$OPTARG" ;;
    r) CLI_PAM_RHOST="$OPTARG" ;;
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
PAM_UID="${CLI_PAM_USER:-${PAM_USER_ID:-}}"
RHOST="${CLI_PAM_RHOST:-${PAM_RHOST:-$(hostname)}}"

if [ -z "${B}" ]; then
  die "Missing broker URL: set SRAM_BROKER_URL or pass -b"
  return 1 2>/dev/null || exit 1
fi
if [ -z "${PAM_UID}" ]; then
  die "Missing PAM user id: set PAM_USER_ID or pass -u"
  return 1 2>/dev/null || exit 1
fi

if [ "${DEBUG_STS_SCRIPT}" = "1" ]; then
  echo "DEBUG: broker=${B} target=${T} pam_user=${PAM_UID}" >&2
fi

start_payload=$(jq -cn --arg u "${PAM_UID}" --arg r "${RHOST}" '{user_id:$u,rhost:$r}')
start_resp=$(curl --http1.1 --connect-timeout 10 --max-time 60 -sS -f -X POST "${B}/pam-weblogin/start" \
  -H "Connection: close" \
  -H "Content-Type: application/json" \
  -d "${start_payload}") || { die "pam-weblogin/start failed"; return 1 2>/dev/null || exit 1; }

challenge_raw="$(extract_json_string_field_loose "${start_resp}" "challenge")"
challenge="$(decode_json_escaped_string "${challenge_raw}")"
start_resp_sanitized="$(strip_pam_heavy_fields "${start_resp}")"

session_id=$(echo "${start_resp_sanitized}" | jq -r '.session_id // empty' 2>/dev/null || true)
if [ -z "${session_id}" ] || [ "${session_id}" = "null" ]; then
  die "start response missing session_id: ${start_resp}"
  return 1 2>/dev/null || exit 1
fi
if [ "${DEBUG_STS_SCRIPT}" = "1" ]; then
  echo "DEBUG: start session_id=${session_id}" >&2
  echo "DEBUG: start payload=${start_payload}" >&2
fi

url=$(echo "${start_resp_sanitized}" | jq -r '.url // empty' 2>/dev/null || true)
if [ -n "${challenge}" ] && [ "${challenge}" != "null" ]; then
  printf '%s\n' "${challenge}"
elif [ -n "${url}" ] && [ "${url}" != "null" ]; then
  echo "Open: ${url}"
  sram_cli_open_url "${url}"
fi

sram_cli_tty_printf "Verification code (PIN): "
sram_cli_tty_read pin

if [ -z "${pin:-}" ]; then
  die "PIN is required"
  return 1 2>/dev/null || exit 1
fi

if [ -n "${T}" ]; then
  pam_sts_payload="$(jq -cn --arg t "${T}" --arg s "${session_id}" --arg p "${pin}" '{target:$t,session_id:$s,pin:$p}')"
else
  pam_sts_payload="$(jq -cn --arg s "${session_id}" --arg p "${pin}" '{session_id:$s,pin:$p}')"
fi

_sram_post_pam_sts() {
  curl --http1.1 --connect-timeout 10 --max-time 60 -sS -w $'\n%{http_code}' -X POST "${B}/cli/pam-sts" \
    -H "Connection: close" \
    -H "Content-Type: application/json" \
    -d "$1"
}

sts_json=$(_sram_post_pam_sts "${pam_sts_payload}") || { die "POST ${B}/cli/pam-sts failed (connection error)"; return 1 2>/dev/null || exit 1; }
sts_http_code="${sts_json##*$'\n'}"
sts_json="${sts_json%$'\n'*}"
if [ "${DEBUG_STS_SCRIPT}" = "1" ]; then
  echo "DEBUG: pam-sts payload=${pam_sts_payload}" >&2
fi

if [ "${sts_http_code}" -lt 200 ] || [ "${sts_http_code}" -ge 300 ]; then
  sram_cli_print_sts_error "${B}/cli/pam-sts" "${sts_http_code}" "${sts_json}" " (target=${T:-auto})"
  sram_cli_hint_old_broker "${sts_json}" || true
  if [ "${sts_http_code}" = "404" ]; then
    echo "Hint: use a collaboration id prefix, SRAM global_urn path, or collaboration short name." >&2
  fi
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
  cli_session="$(echo "${sts_json}" | jq -r '.cli_session // empty')"
  if [ -z "${cli_session}" ]; then
    die "broker choose response missing cli_session; try again"
    return 1 2>/dev/null || exit 1
  fi
  pam_sts_payload="$(jq -cn --arg t "${T}" --arg cs "${cli_session}" '{target:$t,cli_session:$cs}')"
  sts_json=$(_sram_post_pam_sts "${pam_sts_payload}") || { die "POST ${B}/cli/pam-sts failed (connection error)"; return 1 2>/dev/null || exit 1; }
  sts_http_code="${sts_json##*$'\n'}"
  sts_json="${sts_json%$'\n'*}"
  if [ "${sts_http_code}" -lt 200 ] || [ "${sts_http_code}" -ge 300 ]; then
    sram_cli_print_sts_error "${B}/cli/pam-sts" "${sts_http_code}" "${sts_json}" " (target=${T})"
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
  echo "Session ready."
  echo "${sts_json}" | jq -c '.Credentials'
else
  echo "Done ! these variables are set in your environment:"
  echo "------------------------------------------------"
  echo "AWS_ENDPOINT_URL=${AWS_ENDPOINT_URL}"
  echo "AWS_REGION=${AWS_REGION:-us-east-1}"
  echo "AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}"
  echo "AWS_SECRET_ACCESS_KEY=<redacted>"
  echo "AWS_SESSION_TOKEN=<redacted>"
  echo "AWS_SESSION_EXPIRATION=${AWS_SESSION_EXPIRATION}"
  echo "------------------------------------------------"
  echo "Run AWS CLI via the wrapped aws command (secrets are not exported — avoids zsh prompt errors):"
  echo "  aws s3 ls"
  echo "aws s3 cp local_file.txt s3://your-bucket/your-file.txt"
  echo "aws s3 cp s3://your-bucket/your-file.txt local_file.txt"
  echo "aws s3 rm s3://your-bucket/your-file.txt"
  echo "aws s3 mb s3://your-bucket"
  echo "aws s3 rb s3://your-bucket"
  echo "aws s3 ls s3://your-bucket"
  echo "aws s3 cp s3://your-bucket/your-file.txt local_file.txt"
  echo "aws s3 cp local_file.txt s3://your-bucket/your-file.txt"
fi
