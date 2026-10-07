# Shared helpers for sram-s3-sts*.sh scripts.
if [ -z "${_SRAM_CLI_DIR:-}" ]; then
  if [ -n "${ZSH_VERSION:-}" ]; then
    _SRAM_CLI_DIR="$(cd "$(dirname "${(%):-%x}")" && pwd)"
  else
    _SRAM_CLI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
  fi
fi
_SRAM_ROOT="$(cd "${_SRAM_CLI_DIR}/.." && pwd)"

# zsh setopt allexport (set -a) exports every assignment — breaks prompts when values contain '%'.
sram_cli_noexport_begin() {
  if [ -n "${ZSH_VERSION:-}" ]; then
    if setopt 2>/dev/null | grep -qx allexport; then
      SRAM_CLI_ALLEXPORT_WAS=1
      setopt no_allexport
    else
      SRAM_CLI_ALLEXPORT_WAS=0
    fi
  elif [ -n "${BASH_VERSION:-}" ]; then
    case $- in
      *a*)
        SRAM_CLI_ALLEXPORT_WAS=1
        set +a
        ;;
      *)
        SRAM_CLI_ALLEXPORT_WAS=0
        ;;
    esac
  fi
}

sram_cli_noexport_end() {
  if [ "${SRAM_CLI_ALLEXPORT_WAS:-0}" -eq 1 ]; then
    if [ -n "${ZSH_VERSION:-}" ]; then
      setopt allexport
    else
      set -a
    fi
    SRAM_CLI_ALLEXPORT_WAS=0
  fi
}

# Load dotenv without failing when CWD != repo root (optional -f path is resolved).
sram_cli_load_dotenv() {
  local explicit="${1:-}"
  local f="" candidate

  if [ -n "${explicit}" ]; then
    case "${explicit}" in
      /*) candidate="${explicit}" ;;
      *) candidate="${_SRAM_ROOT}/${explicit}" ;;
    esac
    for candidate in "${explicit}" "${candidate}" "${_SRAM_CLI_DIR}/${explicit##*/}"; do
      [ -n "${candidate}" ] || continue
      [ -f "${candidate}" ] && [ -r "${candidate}" ] || continue
      f="${candidate}"
      break
    done
  else
    for candidate in "${_SRAM_ROOT}/.env" "${_SRAM_CLI_DIR}/.env"; do
      if [ -f "${candidate}" ] && [ -r "${candidate}" ]; then
        f="${candidate}"
        break
      fi
    done
  fi

  if [ -z "${f}" ]; then
    return 0
  fi

  f="$(cd "$(dirname "${f}")" && pwd)/$(basename "${f}")"
  if [ ! -f "${f}" ] || [ ! -r "${f}" ]; then
    return 0
  fi

  sram_cli_noexport_begin
  # shellcheck disable=SC1090
  . "${f}"
  sram_cli_noexport_end
}

sram_cli_hint_old_broker() {
  local body="$1"
  if echo "${body}" | grep -q 'target, session_id, and pin are required'; then
    echo "Hint: this broker build does not support omitting -p yet. Redeploy the latest image, or run with:" >&2
    echo "  -p surf_demo:project2:members" >&2
    return 0
  fi
  return 1
}

# Prompt/read on /dev/tty when available (sourcing in zsh/Cursor after PIN).
sram_cli_has_tty() {
  { : >/dev/tty; } 2>/dev/null
}

sram_cli_tty_printf() {
  if sram_cli_has_tty; then
    # shellcheck disable=SC2059
    printf '%b' "$@" >/dev/tty
  else
    # shellcheck disable=SC2059
    printf '%b' "$@" >&2
  fi
}

sram_cli_tty_read() {
  local _var="$1"
  if sram_cli_has_tty && { read -r "${_var}" </dev/tty; } 2>/dev/null; then
    return 0
  fi
  read -r "${_var}"
}

sram_cli_open_url() {
  local url="$1"
  if command -v open >/dev/null 2>&1; then
    open "$url" 2>/dev/null &
  elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$url" 2>/dev/null &
  elif command -v wslview >/dev/null 2>&1; then
    wslview "$url" 2>/dev/null &
  fi
}

sram_cli_pick_target() {
  local resp="$1"
  local picker="${SRAM_CLI_PICKER:-shell}"

  if [ "${picker}" = "python" ] || [ "${picker}" = "curses" ]; then
    SRAM_CLI_CHOOSE_JSON="${resp}" python3 "${_SRAM_CLI_DIR}/sram-s3-select-target.py"
    return $?
  fi

  local choose_status count choice i target
  choose_status="$(echo "${resp}" | jq -r '.status // empty')"
  if [ "${choose_status}" != "choose" ]; then
    echo "ERROR: expected broker response with status=choose" >&2
    return 1
  fi

  count="$(echo "${resp}" | jq -r '.targets | length')"
  if [ -z "${count}" ] || [ "${count}" = "0" ] || [ "${count}" = "null" ]; then
    echo "ERROR: broker returned no targets to choose" >&2
    return 1
  fi
  if [ "${count}" -eq 1 ]; then
    echo "${resp}" | jq -r '.targets[0].target'
    return 0
  fi

  if ! sram_cli_has_tty && ! [ -t 0 ]; then
    echo "ERROR: multiple roles matched; re-run with -p <target>:" >&2
    echo "${resp}" | jq -r '.targets[] | "  \(.target)  \(.label)"' >&2
    return 1
  fi

  sram_cli_tty_printf "\nSelect IAM role (enter number, then Enter):\n"
  i=1
  while [ "${i}" -le "${count}" ]; do
    echo "${resp}" | jq -r --argjson n "${i}" \
      '.targets[$n - 1] | "  \($n)) \(.label // .target)  (\(.target))"' >&2
    i=$((i + 1))
  done

  choice=""
  i=0
  while [ "${i}" -lt 10 ]; do
    sram_cli_tty_printf "Choice: "
    if ! sram_cli_tty_read choice; then
      echo "ERROR: could not read choice" >&2
      return 1
    fi
    case "${choice}" in
      ''|*[!0-9]*)
        sram_cli_tty_printf "Enter a number from 1 to ${count}.\n"
        i=$((i + 1))
        continue
        ;;
    esac
    if [ "${choice}" -ge 1 ] && [ "${choice}" -le "${count}" ]; then
      break
    fi
    sram_cli_tty_printf "Enter a number from 1 to ${count}.\n"
    i=$((i + 1))
  done
  if [ -z "${choice}" ] || [ "${choice}" -lt 1 ] || [ "${choice}" -gt "${count}" ]; then
    echo "ERROR: role selection cancelled or invalid" >&2
    return 1
  fi

  target="$(echo "${resp}" | jq -r --argjson n "${choice}" '.targets[$n - 1].target')"
  if [ -z "${target}" ] || [ "${target}" = "null" ]; then
    echo "ERROR: could not resolve target for choice ${choice}" >&2
    return 1
  fi
  printf '%s\n' "${target}"
}

# Exported AWS_* values may contain '%' and break zsh/oh-my-zsh (print -P).
sram_cli_unexport_aws_secrets() {
  if [ -n "${ZSH_VERSION:-}" ]; then
    typeset +x AWS_ENDPOINT_URL AWS_REGION AWS_DEFAULT_REGION AWS_ACCESS_KEY_ID \
      AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_SESSION_EXPIRATION 2>/dev/null || true
  else
    export -n AWS_ENDPOINT_URL AWS_REGION AWS_DEFAULT_REGION AWS_ACCESS_KEY_ID \
      AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_SESSION_EXPIRATION 2>/dev/null || true
  fi
}

sram_cli_aws_region() {
  printf '%s' "${AWS_REGION:-${AWS_DEFAULT_REGION:-${SRAM_AWS_REGION:-us-east-1}}}"
}

sram_cli_install_aws_wrapper() {
  if [ "${SRAM_CLI_AWS_WRAPPER:-0}" = "1" ]; then
    return 0
  fi
  SRAM_CLI_AWS_WRAPPER=1
  sram_cli_aws_invoke() {
    local region extra=() a has_endpoint=0
    region="$(sram_cli_aws_region)"
    if [ -n "${AWS_ENDPOINT_URL:-}" ]; then
      for a in "$@"; do
        if [ "${a}" = "--endpoint-url" ]; then
          has_endpoint=1
          break
        fi
      done
      if [ "${has_endpoint}" -eq 0 ]; then
        extra=(--endpoint-url "${AWS_ENDPOINT_URL}")
      fi
    fi
    command env \
      "AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}" \
      "AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}" \
      "AWS_SESSION_TOKEN=${AWS_SESSION_TOKEN}" \
      "AWS_SESSION_EXPIRATION=${AWS_SESSION_EXPIRATION}" \
      "AWS_ENDPOINT_URL=${AWS_ENDPOINT_URL}" \
      "AWS_DEFAULT_REGION=${region}" \
      "AWS_REGION=${region}" \
      aws "${extra[@]}" "$@"
  }
  aws() {
    sram_cli_aws_invoke "$@"
  }
}

sram_cli_export_aws_env() {
  local sts_json="$1"
  local use_wrapper=0

  if [ -n "${ZSH_VERSION:-}" ] || [ "${IS_SOURCED:-0}" -eq 1 ]; then
    use_wrapper=1
  fi

  sram_cli_noexport_begin
  AWS_ENDPOINT_URL="$(echo "${sts_json}" | jq -r '.S3EndpointUrl // empty')"
  AWS_REGION="$(echo "${sts_json}" | jq -r '.AwsRegion // empty')"
  if [ -z "${AWS_REGION}" ] || [ "${AWS_REGION}" = "null" ]; then
    AWS_REGION="${SRAM_AWS_REGION:-us-east-1}"
  fi
  AWS_DEFAULT_REGION="${AWS_REGION}"
  AWS_ACCESS_KEY_ID="$(echo "${sts_json}" | jq -r '.Credentials.AccessKeyId')"
  AWS_SECRET_ACCESS_KEY="$(echo "${sts_json}" | jq -r '.Credentials.SecretAccessKey')"
  AWS_SESSION_TOKEN="$(echo "${sts_json}" | jq -r '.Credentials.SessionToken')"
  AWS_SESSION_EXPIRATION="$(echo "${sts_json}" | jq -r '.Credentials.Expiration')"
  sram_cli_noexport_end

  if [ "${use_wrapper}" -eq 1 ]; then
    sram_cli_unexport_aws_secrets
    sram_cli_install_aws_wrapper
  else
    export AWS_ENDPOINT_URL AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY \
      AWS_SESSION_TOKEN AWS_SESSION_EXPIRATION
  fi
}

sram_cli_print_sts_error() {
  local url="$1"
  local http_code="$2"
  local body="$3"
  local target_hint="${4:-}"
  echo "ERROR: POST ${url} returned HTTP ${http_code}${target_hint}:" >&2
  echo "${body}" | jq -c '.' 2>/dev/null || echo "${body}" >&2
}
