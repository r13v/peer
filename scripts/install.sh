#!/bin/sh
set -eu

fail() { printf 'peer install: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail 'macOS or Linux is required' ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac
for tool in curl tar awk install mktemp; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1"; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1"; }
else
  fail 'sha256sum or shasum is required'
fi
[ -n "${HOME:-}" ] || fail 'HOME is required'

archive="peer_${os}_$arch.tar.gz"
base='https://github.com/r13v/peer/releases/latest/download'
tmp="$(mktemp -d)"
stage=''
cleanup() {
  [ -z "$stage" ] || { [ ! -f "$stage" ] || rm "$stage"; }
  rm -r "$tmp"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
expected="$(awk -v name="$archive" '$2 == name { print $1; exit }' "$tmp/checksums.txt")"
case "$expected" in
  ''|*[!0-9a-fA-F]*) fail 'invalid archive checksum' ;;
esac
[ "${#expected}" -eq 64 ] || fail 'invalid archive checksum'
actual="$(sha256 "$tmp/$archive" | awk '{ print $1 }')"
[ "$actual" = "$expected" ] || fail 'archive checksum mismatch'

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/peer" ] || fail 'release archive is incomplete'

install_dir="${PEER_INSTALL_DIR:-$HOME/.local/bin}"
install -d -m 755 "$install_dir"
[ ! -d "$install_dir/peer" ] || fail 'install target is a directory'
stage="$(mktemp "$install_dir/.peer.XXXXXX")"
install -m 755 "$tmp/peer" "$stage"
mv "$stage" "$install_dir/peer"
stage=''

printf 'peer installed at %s/peer\n' "$install_dir"
case ":${PATH:-}:" in
  *":$install_dir:"*) ;;
  *) printf 'Add %s to PATH to run peer.\n' "$install_dir" ;;
esac
