#!/bin/sh
set -eu

fail() { printf 'peer install: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] || fail 'macOS is required'
case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) fail 'unsupported Mac architecture' ;;
esac
for tool in curl tar shasum install mktemp; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done
[ -n "${HOME:-}" ] || fail 'HOME is required'

archive="peer-darwin-$arch.tar.gz"
base='https://github.com/r13v/peer/releases/download/latest'
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
actual="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
[ "$actual" = "$expected" ] || fail 'archive checksum mismatch'

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/peer" ] && [ -f "$tmp/SKILL.md" ] || fail 'release archive is incomplete'

install_dir="${PEER_INSTALL_DIR:-$HOME/.local/bin}"
install -d -m 755 "$install_dir"
[ ! -d "$install_dir/peer" ] || fail 'install target is a directory'
install -d -m 700 "$HOME/.claude/skills/peer" "$HOME/.codex/skills/peer"
install -m 644 "$tmp/SKILL.md" "$HOME/.claude/skills/peer/SKILL.md"
install -m 644 "$tmp/SKILL.md" "$HOME/.codex/skills/peer/SKILL.md"

stage="$(mktemp "$install_dir/.peer.XXXXXX")"
install -m 755 "$tmp/peer" "$stage"
mv "$stage" "$install_dir/peer"
stage=''

printf 'peer installed at %s/peer\n' "$install_dir"
case ":${PATH:-}:" in
  *":$install_dir:"*) ;;
  *) printf 'Add %s to PATH to run peer.\n' "$install_dir" ;;
esac
