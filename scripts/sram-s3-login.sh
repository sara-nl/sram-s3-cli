#!/usr/bin/env bash
# Log in with SRAM through the SRAM-S3 broker (/cli/v1) and print temporary S3 credentials
# as shell export lines: the bash alternative to the sram-s3 binary. Needs bash, curl, jq.
#
#   eval "$(scripts/sram-s3-login.sh)"                          # device code, in the browser
#   eval "$(scripts/sram-s3-login.sh -p org1:project1:s3admin)"  # a given role
#   eval "$(scripts/sram-s3-login.sh -u <SRAM username>)"       # SRAM PAM weblogin
#
# stdout carries only the export lines; every message goes to stderr.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: sram-s3-login.sh [-b URL] [-p TARGET] [-u SRAM_USERNAME] [-d]

  -b URL            broker base URL (or env SRAM_BROKER_URL)
  -p TARGET         the role's target: its SRAM entitlement (e.g. org1:project1:s3admin),
                    or <id>:<role> as listed (or env SRAM_PROJECT)
  -u SRAM_USERNAME  log in with SRAM PAM weblogin instead of a device code
  -d                debug: the raw broker responses on stderr (secrets redacted)

Use: eval "$(sram-s3-login.sh)"
EOF
}

die() { echo "ERROR: $*" >&2; exit 1; }

broker=${SRAM_BROKER_URL:-}
wanted=${SRAM_PROJECT:-}
pam_user=""
debug=0
while getopts ":b:p:u:dh" opt; do
  case $opt in
    b) broker=$OPTARG ;;
    p) wanted=$OPTARG ;;
    u) pam_user=$OPTARG ;;
    d) debug=1 ;;
    h) usage; exit 0 ;;
    :) die "option -$OPTARG needs a value" ;;
    *) usage; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 0 ] || { usage; exit 2; }
broker=${broker%/}
[ -n "$broker" ] || die "missing broker URL (-b or SRAM_BROKER_URL)"
for cmd in curl jq; do command -v "$cmd" >/dev/null || die "$cmd is required"; done

# post PATH [JSON]: POST to the broker; the answer is left in $body. The JSON goes on stdin,
# so the PIN and codes never show in the process list.
post() {
  local path=$1 data=${2-} out code detail
  local args=(-sS --connect-timeout 10 --max-time 60 -X POST -H 'Accept: application/json' -w '\n%{http_code}')
  [ -z "$data" ] || args+=(-H 'Content-Type: application/json' --data-binary @-)
  out=$(printf '%s' "$data" | curl "${args[@]}" "$broker$path") || die "POST $path failed (connection error)"
  code=${out##*$'\n'}
  body=${out%$'\n'*}
  if [ "$debug" = 1 ]; then
    echo "DEBUG: POST $path: HTTP $code: $(jq -c 'if (.credentials | type) == "object"
      then .credentials.SecretAccessKey = "<redacted>" | .credentials.SessionToken = "<redacted>" else . end' \
      <<<"$body" 2>/dev/null || echo "$body")" >&2
  fi
  if [ "$code" -lt 200 ] || [ "$code" -gt 299 ]; then
    detail=$(jq -r 'if (.detail | type) == "string" then .detail else (.detail | tostring) end' <<<"$body" 2>/dev/null) ||
      detail=${body:-no error message}
    die "$detail (HTTP $code from $path)"
  fi
}

# The role list, grouped by project: the same layout as the sram-s3 binary.
print_roles() {
  local i width=0 project="" n=${#t_target[@]}
  for ((i = 0; i < n; i++)); do [ "${#t_role[i]}" -le "$width" ] || width=${#t_role[i]}; done
  for ((i = 0; i < n; i++)); do
    if [ $i -eq 0 ] || [ "${t_project[i]}" != "$project" ]; then
      project=${t_project[i]}
      echo "  $project"
    fi
    printf "    %${#n}d) %-${width}s   %s\n" $((i + 1)) "${t_role[i]}" "${t_target[i]}"
  done
}

# choose: set $chosen to the -p target if the user holds it, else let the user pick.
choose() {
  local i pick n=${#t_target[@]}
  if [ -n "$wanted" ]; then
    for ((i = 0; i < n; i++)); do [ "${t_target[i]}" != "$wanted" ] || { chosen=$wanted; return; }; done
    echo "You have no role for target \"$wanted\"." >&2
  fi
  if ! (: </dev/tty) 2>/dev/null; then
    { echo "Your roles:"; print_roles; } >&2
    die "several roles: re-run with -p <target>"
  fi
  { echo "Choose a role:"; print_roles; } >/dev/tty
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    printf 'Role (1-%d): ' "$n" >/dev/tty
    read -r pick </dev/tty || break
    if [[ $pick =~ ^[0-9]+$ ]] && [ "$pick" -ge 1 ] && [ "$pick" -le "$n" ]; then
      chosen=${t_target[pick - 1]}
      echo "Using role ${t_iam[pick - 1]} (target $chosen)" >&2
      return
    fi
  done
  die "no role chosen"
}

deadline=0
if [ -n "$pam_user" ]; then
  post /cli/v1/pam "$(jq -cn --arg u "$pam_user" '{user_id: $u}')"
  (: </dev/tty) 2>/dev/null || die "PAM login needs a terminal to read the PIN"
  jq -r .challenge <<<"$body" >&2
  printf 'PIN: ' >/dev/tty
  pin=""
  read -rs pin </dev/tty || true
  echo >/dev/tty
  [ -n "$pin" ] || die "no PIN entered"
  request=$(jq -cn --arg s "$(jq -r .pam_session <<<"$body")" --arg p "$pin" '{pam_session: $s, pin: $p}')
  unset pin
else
  post /cli/v1/device
  url=$(jq -r '.verification_uri_complete // empty' <<<"$body")
  if [ -n "$url" ]; then
    echo "Open: $url" >&2
  else
    url=$(jq -r .verification_uri <<<"$body")
    echo "Open: $url" >&2
    echo "Enter code: $(jq -r .user_code <<<"$body")" >&2
  fi
  for opener in open xdg-open; do
    if command -v "$opener" >/dev/null; then "$opener" "$url" >/dev/null 2>&1 & break; fi
  done
  expires_in=$(jq -r .expires_in <<<"$body")
  echo "Complete the sign-in in the browser (within $((expires_in / 60)) min)." >&2
  deadline=$(($(date +%s) + expires_in))
  request=$(jq -c '{device_code}' <<<"$body")
fi

# One loop for both logins: pending (wait), choose (pick a role), ok.
chose=0
polls=0
while :; do
  post /cli/v1/credentials "$request"
  case $(jq -r .status <<<"$body") in
    ok) break ;;
    pending)
      interval=$(jq -r '.interval // 5' <<<"$body")
      [ $(($(date +%s) + interval)) -le $deadline ] || die "the login expired before sign-in was completed; run login again"
      polls=$((polls + 1))
      if [ $polls -eq 1 ] || [ $((polls % 6)) -eq 0 ]; then
        echo "Waiting for sign-in in the browser… ($((deadline - $(date +%s)))s left)" >&2
      fi
      sleep "$interval"
      ;;
    choose)
      [ $chose -eq 0 ] || die "unexpected choose answer"
      t_project=() t_role=() t_target=() t_iam=()
      while IFS=$'\x1f' read -r p r t i; do
        t_project+=("$p") t_role+=("$r") t_target+=("$t") t_iam+=("$i")
      done < <(jq -r '.targets[] | [.project,
          (if .kind == "members" then "member" elif .kind == "admin" then "project admin (\(.group))" else "group \(.group)" end),
          .target, .iam_role_name] | join("\u001f")' <<<"$body")
      choose
      chose=1
      request=$(jq -cn --arg s "$(jq -r .login_session <<<"$body")" --arg t "$chosen" '{login_session: $s, target: $t}')
      ;;
    *) die "unknown answer from the broker: $(jq -r .status <<<"$body")" ;;
  esac
done

target=$(jq -r .target <<<"$body")
if [ -n "$wanted" ] && [ $chose -eq 0 ] && [ "$target" != "$wanted" ]; then
  die "you have no role for target \"$wanted\"; your only role is $target"
fi
echo "Credentials for $target, valid until $(jq -r .credentials.Expiration <<<"$body")." >&2
jq -r '"export AWS_ENDPOINT_URL=\(.endpoint_url | @sh)",
  "export AWS_REGION=\(.region | @sh)",
  "export AWS_DEFAULT_REGION=\(.region | @sh)",
  "export AWS_ACCESS_KEY_ID=\(.credentials.AccessKeyId | @sh)",
  "export AWS_SECRET_ACCESS_KEY=\(.credentials.SecretAccessKey | @sh)",
  "export AWS_SESSION_TOKEN=\(.credentials.SessionToken | @sh)",
  "export AWS_SESSION_EXPIRATION=\(.credentials.Expiration | @sh)"' <<<"$body"
