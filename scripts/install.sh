#!/usr/bin/env sh
# Install the Popin CLI daemon from the latest GitHub Release.
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/anyuan-chen/popin/main/scripts/install.sh | sh
#
# Or from a local clone:
#   sh scripts/install.sh [version]
#
# By default installs the latest release. Pass a version tag (e.g. v0.1.0) to
# pin. Note: the released CLI is built against the hosted backend
# (api.popin.andrewchen.uk), so `popin login` works out of the box.

set -eu

REPO="anyuan-chen/popin"
VERSION="${1:-}"
INSTALL_DIR_BIN="/usr/local/bin"

err() { printf 'install: %s\n' "$*" >&2; }

# --- detect OS / arch -------------------------------------------------------

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch_raw="$(uname -m)"

case "$arch_raw" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  i386|i486|i586|i686) arch="386" ;;
  *) err "unsupported architecture: $arch_raw"; exit 1 ;;
esac

case "$os" in
  darwin|linux) ;;
  *) err "unsupported OS: $os (this installer supports macOS and Linux)"; exit 1 ;;
esac

# --- resolve download URL --------------------------------------------------

if [ -n "$VERSION" ]; then
  # strip leading 'v' for the path then re-add for the tag
  version_path="$(printf '%s' "$VERSION" | sed 's/^v//')"
  base_url="https://github.com/${REPO}/releases/download/${VERSION}"
else
  # Resolve the latest release tag via the redirecting URL.
  tmp_loc="$(mktemp)"
  if command -v curl >/dev/null 2>&1; then
    latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
      "https://github.com/${REPO}/releases/latest" || true)
  elif command -v wget >/dev/null 2>&1; then
    latest_url=$(wget -qSO- --max-redirect=0 "https://github.com/${REPO}/releases/latest" 2>&1 \
      | sed -n 's/.*Location: \([^[:space:]]*\).*/\1/p' || true)
  else
    err "need curl or wget"; exit 1
  fi
  VERSION="${latest_url##*/}"
  [ -n "$VERSION" ] || { err "could not determine latest release"; exit 1; }
  base_url="https://github.com/${REPO}/releases/download/${VERSION}"
  rm -f "$tmp_loc"
fi

archive="popin_${os}_${arch}.tar.gz"
checksums_file="checksums.txt"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf 'Popin %s for %s/%s\n' "$VERSION" "$os" "$arch"

# --- download --------------------------------------------------------------

download() {
  url="$1"; out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$out" "$url"
  else
    wget -qO "$out" "$url"
  fi
}

download "${base_url}/${archive}"        "${tmp}/${archive}"
download "${base_url}/${checksums_file}" "${tmp}/${checksums_file}" || {
  printf 'warning: could not fetch checksums; skipping verification\n' >&2
}

# --- verify ---------------------------------------------------------------

if [ -s "${tmp}/${checksums_file}" ]; then
  expected="$(grep " ${archive}\\$" "${tmp}/${checksums_file}" | awk '{print $1}')"
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "${tmp}/${archive}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
      actual="$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')"
    else
      printf 'warning: no sha256 tool found; skipping verification\n' >&2
      actual=""
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      err "checksum mismatch (got $actual, want $expected)"; exit 1
    fi
  fi
fi

# --- extract + install -----------------------------------------------------

tar -xzf "${tmp}/${archive}" -C "$tmp"
bin="${tmp}/popin"

if [ ! -f "$bin" ]; then
  err "release archive did not contain a 'popin' binary"; exit 1
fi

install_to="$INSTALL_DIR_BIN/popin"
if [ -w "$INSTALL_DIR_BIN" ] || [ "$(id -u)" -eq 0 ]; then
  mkdir -p "$INSTALL_DIR_BIN"
  cp "$bin" "$install_to"
  chmod 0755 "$install_to"
else
  install_dir="${HOME}/.local/bin"
  mkdir -p "$install_dir"
  cp "$bin" "${install_dir}/popin"
  chmod 0755 "${install_dir}/popin"
  printf 'installed to %s/popin\n' "$install_dir"
  printf 'make sure %s is on your PATH, e.g.:\n  export PATH="%s:$PATH"\n' \
    "$install_dir" "$install_dir"
  printf 'then run: popin login\n'
  exit 0
fi

printf 'installed popin to %s\n' "$install_to"
printf 'run: popin login\n'