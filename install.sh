#!/usr/bin/env bash
set -euo pipefail

REPO="sara-nl/sram-s3-cli"
DOWNLOAD_BASE="https://github.com/${REPO}/releases/latest/download"
INSTALL_DIR="/usr/local/bin"
BINARY="sram-s3"
SHELL_WRAPPER='srams3() { eval "$(sram-s3 login "$@")"; }'

die() { echo "ERROR: $*" >&2; exit 1; }

detect_platform() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"

  case "${os}" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *)      die "Unsupported OS: ${os}" ;;
  esac

  case "${arch}" in
    x86_64)          arch="amd64" ;;
    aarch64|arm64)   arch="arm64" ;;
    *)               die "Unsupported architecture: ${arch}" ;;
  esac

  echo "${BINARY}-${os}-${arch}"
}

install_binary() {
  local filename="$1"
  local url="${DOWNLOAD_BASE}/${filename}"
  local checksum_url="${DOWNLOAD_BASE}/checksums.sha256"
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir:-}"' EXIT

  echo "Downloading ${url} …"
  curl -fsSL -o "${tmpdir}/${filename}" "${url}"
  curl -fsSL -o "${tmpdir}/checksums.sha256" "${checksum_url}"

  echo "Verifying checksum…"
  ( cd "${tmpdir}" && grep "${filename}" checksums.sha256 | \
      { command -v sha256sum >/dev/null 2>&1 && sha256sum -c - || shasum -a 256 -c -; } )

  chmod +x "${tmpdir}/${filename}"

  if [ -w "${INSTALL_DIR}" ]; then
    mv "${tmpdir}/${filename}" "${INSTALL_DIR}/${BINARY}"
    echo "Installed to ${INSTALL_DIR}/${BINARY}"
  elif command -v sudo >/dev/null 2>&1; then
    sudo mv "${tmpdir}/${filename}" "${INSTALL_DIR}/${BINARY}"
    echo "Installed to ${INSTALL_DIR}/${BINARY} (via sudo)"
  else
    local fallback="${HOME}/.local/bin"
    mkdir -p "${fallback}"
    mv "${tmpdir}/${filename}" "${fallback}/${BINARY}"
    echo "Installed to ${fallback}/${BINARY}"
    echo "Make sure ${fallback} is in your PATH."
  fi
}

add_shell_wrapper() {
  local added=0
  for rc in "${HOME}/.bashrc" "${HOME}/.zshrc"; do
    [ -f "${rc}" ] || continue
    if ! grep -q 'srams3()' "${rc}"; then
      printf '\n%s\n' "${SHELL_WRAPPER}" >> "${rc}"
      echo "Added srams3 shell wrapper to ${rc}"
      added=1
    fi
  done
  if [ "${added}" -eq 0 ]; then
    echo "Shell wrapper already present (or no .bashrc/.zshrc found)."
  fi
}

main() {
  local filename
  filename="$(detect_platform)"
  install_binary "${filename}"
  add_shell_wrapper
  echo ""
  echo "Done. Reload your shell or run:"
  echo "  source ~/.bashrc   # or ~/.zshrc"
  echo ""
  echo "Then log in with:"
  echo "  srams3"
}

main "$@"
