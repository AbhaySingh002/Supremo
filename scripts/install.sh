#!/usr/bin/env sh
set -eu

repo="AbhaySingh002/Supremo"
api="https://api.github.com/repos/$repo/releases/latest"
version="${SUPREMO_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "$api" | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p')
fi
[ -n "$version" ] || { echo "Could not find the latest Supremo release." >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "Use the Windows release archive on this platform." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="supremo_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
checksum=$(grep "  $asset$" "$tmp/checksums.txt" || true)
[ -n "$checksum" ] || { echo "Checksum for $asset is missing." >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && printf '%s\n' "$checksum" | sha256sum -c -)
else
  (cd "$tmp" && printf '%s\n' "$checksum" | shasum -a 256 -c -)
fi

dest="${SUPREMO_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
tar -xzf "$tmp/$asset" -C "$tmp"
install -m 0755 "$tmp/supremo" "$dest/supremo"
echo "Installed Supremo to $dest/supremo"
echo "Add $dest to PATH if needed, then run: supremo"
