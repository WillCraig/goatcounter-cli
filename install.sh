#!/bin/sh
# Install gcli on macOS or Linux. Does not use sudo.
# Usage: curl -fsSL https://raw.githubusercontent.com/WillCraig/goatcounter-cli/main/install.sh | sh
# Optional: GCLI_VERSION=v0.1.0  GCLI_INSTALL_DIR=$HOME/.local/bin
set -eu

repo="WillCraig/goatcounter-cli"

say() { printf '%s\n' "$*" >&2; }
die() { say "gcli install: $*"; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

os=$(uname -s)
case "$os" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "this script supports macOS and Linux only (uname reported $os). On Windows, use Scoop." ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

tag="${GCLI_VERSION:-}"
if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
    head -n 1)
  [ -n "$tag" ] || die "could not read the latest release tag"
fi
case "$tag" in
  v*) ver=${tag#v} ;;
  *) ver=$tag; tag="v${tag}" ;;
esac

archive="gcli_${ver}_${os}_${arch}.tar.gz"
base="https://github.com/${repo}/releases/download/${tag}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "downloading ${archive}"
curl -fsSL "${base}/${archive}" -o "${tmp}/${archive}"
curl -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f || $NF == f { print $1; exit }' "${tmp}/checksums.txt")
[ -n "$expected" ] || die "checksums.txt has no entry for ${archive}"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "${tmp}/${archive}" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for ${archive}"

tar -xzf "${tmp}/${archive}" -C "$tmp" gcli
[ -f "${tmp}/gcli" ] || die "archive did not contain gcli"

dest="${GCLI_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}"
mkdir -p "$dest" || die "cannot create ${dest}"
cp "${tmp}/gcli" "${dest}/gcli"
chmod 0755 "${dest}/gcli"

case ":${PATH}:" in
  *":${dest}:"*) ;;
  *) say "add ${dest} to PATH to run gcli" ;;
esac
say "installed ${dest}/gcli"
