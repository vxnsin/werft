#!/bin/sh
# Installs or updates werft in /usr/local/bin.
#
#   curl -fsSL https://raw.githubusercontent.com/vxnsin/werft/main/install.sh | sudo sh
#
# A specific version: ... | sudo WERFT_VERSION=v0.1.0 sh
set -eu

repo="vxnsin/werft"
dest="${WERFT_BIN:-/usr/local/bin}"

fail() { echo "werft-install: $*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || fail "werft only runs on Linux"
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "architecture $(uname -m) is not supported" ;;
esac
command -v curl >/dev/null || fail "curl is missing"
command -v git >/dev/null || echo "note: werft needs git, please install it (apt install git)." >&2

version="${WERFT_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
    sed -n "s/.*\"tag_name\": *\"\([^\"]*\)\".*/\1/p" | head -n 1)
  [ -n "$version" ] || fail "could not find the latest version"
fi

file="werft_${version#v}_linux_${arch}.tar.gz"
url="https://github.com/$repo/releases/download/$version"
tmp=$(mktemp -d)
trap "rm -rf \"$tmp\"" EXIT

echo "downloading werft $version ($arch)"
curl -fsSL -o "$tmp/$file" "$url/$file"
curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt"
(cd "$tmp" && grep " $file\$" checksums.txt | sha256sum -c --quiet -) || fail "checksum does not match"

tar -xzf "$tmp/$file" -C "$tmp" werft
install -m 0755 "$tmp/werft" "$dest/werft"
echo "werft is in $dest/werft"
"$dest/werft" version
