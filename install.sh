#!/usr/bin/env bash
# Install the sram-s3 CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/sara-nl/sram-s3-cli/main/install.sh | bash
#
# Options (environment variables):
#   SRAM_S3_INSTALL_DIR=DIR    install directory (default: ~/.local/bin, no sudo needed).
#                              sudo is used only if DIR is not writable,
#                              e.g. SRAM_S3_INSTALL_DIR=/usr/local/bin
#   SRAM_S3_VERSION=vX.Y.Z     install a specific release (default: latest)
#   SRAM_S3_NO_MODIFY_PATH=1   do not modify your shell rc file

# Text for rc files is single-quoted on purpose ($PATH etc. written literally).
# shellcheck disable=SC2016

# When piped into sh instead of bash, stop before any bash-only syntax.
if [ -z "${BASH_VERSION:-}" ]; then
  echo "ERROR: this installer requires bash: curl -fsSL <url> | bash" >&2
  exit 1
fi

set -euo pipefail

REPO="sara-nl/sram-s3-cli"
INSTALL_DIR="${SRAM_S3_INSTALL_DIR:-${HOME}/.local/bin}"
INSTALL_DIR="${INSTALL_DIR%/}"

die() { echo "ERROR: $*" >&2; exit 1; }

# --- Which binary: sram-s3-<os>-<arch> -------------------------------------

case "$(uname -s)" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *)             die "unsupported architecture: $(uname -m)" ;;
esac

filename="sram-s3-${os}-${arch}"

# --- Where to download from --------------------------------------------------

if [ -n "${SRAM_S3_VERSION:-}" ]; then
  version="v${SRAM_S3_VERSION#v}"   # accept both 0.2.0 and v0.2.0
  base_url="https://github.com/${REPO}/releases/download/${version}"
else
  base_url="https://github.com/${REPO}/releases/latest/download"
fi
base_url="${SRAM_S3_DOWNLOAD_BASE:-${base_url}}"   # for testing against a mirror

# --- Download and verify -----------------------------------------------------

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

echo "Downloading ${base_url}/${filename} …"
curl -fsSL -o "${tmpdir}/${filename}" "${base_url}/${filename}"
curl -fsSL -o "${tmpdir}/checksums.sha256" "${base_url}/checksums.sha256"

expected="$(awk -v f="${filename}" '$2 == f { print $1 }' "${tmpdir}/checksums.sha256")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmpdir}/${filename}" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "${tmpdir}/${filename}" | awk '{ print $1 }')"
fi
[ -n "${expected}" ] && [ "${expected}" = "${actual}" ] || die "checksum mismatch for ${filename}"
chmod 755 "${tmpdir}/${filename}"

# --- Install -----------------------------------------------------------------

if mkdir -p "${INSTALL_DIR}" 2>/dev/null && [ -w "${INSTALL_DIR}" ]; then
  mv -f "${tmpdir}/${filename}" "${INSTALL_DIR}/sram-s3"
else
  echo "${INSTALL_DIR} is not writable; using sudo…"
  if ! sudo mkdir -p "${INSTALL_DIR}" || ! sudo mv -f "${tmpdir}/${filename}" "${INSTALL_DIR}/sram-s3"; then
    die "could not install to ${INSTALL_DIR}. Leave SRAM_S3_INSTALL_DIR unset to install to ~/.local/bin without sudo."
  fi
fi
echo "Installed ${INSTALL_DIR}/sram-s3"

# --- Shell setup: PATH and the srams3 wrapper ---------------------------------

# Lines for the rc file. Single quotes: $HOME, $PATH, $@ are written literally.
setup_lines=()
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;   # already on PATH
  *)
    if [ "${INSTALL_DIR#"${HOME}"/}" != "${INSTALL_DIR}" ]; then
      setup_lines+=('export PATH="$HOME/'"${INSTALL_DIR#"${HOME}"/}"':$PATH"')
    else
      setup_lines+=('export PATH="'"${INSTALL_DIR}"':$PATH"')
    fi
    ;;
esac
setup_lines+=('srams3() { eval "$(sram-s3 login "$@")"; }')

case "$(basename "${SHELL:-}")" in
  zsh)  rc_file="${ZDOTDIR:-${HOME}}/.zshrc" ;;
  bash) if [ "${os}" = "darwin" ]; then rc_file="${HOME}/.bash_profile"; else rc_file="${HOME}/.bashrc"; fi ;;
  *)    rc_file="" ;;
esac

if [ "${SRAM_S3_NO_MODIFY_PATH:-0}" = "1" ] || [ -z "${rc_file}" ]; then
  rc_file=""
  echo ""
  echo "Add the following to your shell rc file:"
  printf '  %s\n' "${setup_lines[@]}"
elif grep -qF "# >>> sram-s3 >>>" "${rc_file}" 2>/dev/null; then
  echo "${rc_file} already set up."
else
  {
    echo ""
    echo "# >>> sram-s3 >>>"
    printf '%s\n' "${setup_lines[@]}"
    echo "# <<< sram-s3 <<<"
  } >> "${rc_file}"
  echo "Updated ${rc_file}"
fi

# --- Done --------------------------------------------------------------------

echo ""
echo "Done. Open a new terminal${rc_file:+ (or run: source ${rc_file})}, then log in with:"
echo "  srams3"
echo ""
echo "For ~/.aws/config, use the absolute path:"
echo "  credential_process = ${INSTALL_DIR}/sram-s3 login --credential-process -p <target>"
