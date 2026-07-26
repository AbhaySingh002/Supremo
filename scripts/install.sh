#!/usr/bin/env sh
set -eu

repo="AbhaySingh002/Supremo"
version="${SUPREMO_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://raw.githubusercontent.com/$repo/main/VERSION")
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

# Add to PATH automatically
setup_path() {
  if printf '%s\n' "$PATH" | grep -qx "$dest"; then
    echo "PATH already configured. You can run: supremo"
    return
  fi

  echo "Adding $dest to PATH..."
  
  # Detect current shell and appropriate profile files
  SHELL_NAME=$(basename "$SHELL")
  PROFILE_FILES=""
  case "$SHELL_NAME" in
    bash)
      PROFILE_FILES="$HOME/.bashrc"
      [ -f "$HOME/.profile" ] && PROFILE_FILES="$PROFILE_FILES $HOME/.profile"
      ;;
    zsh)
      PROFILE_FILES="$HOME/.zshrc"
      ;;
    fish)
      PROFILE_FILES="$HOME/.config/fish/config.fish"
      ;;
    *)
      PROFILE_FILES="$HOME/.profile"
      ;;
  esac

  # Add to each profile file
  for profile in $PROFILE_FILES; do
    if [ -n "$profile" ]; then
      # Create directory if it doesn't exist
      profile_dir=$(dirname "$profile")
      mkdir -p "$profile_dir"
      
      # Create file if it doesn't exist
      if [ ! -f "$profile" ]; then
        touch "$profile"
      fi
      
      # Check if already in the file
      if grep -qF "$dest" "$profile" 2>/dev/null; then
        echo "  Already in $profile"
        continue
      fi
      
      # Ensure file ends with newline
      if [ -s "$profile" ] && [ "$(tail -c1 "$profile" | wc -l)" -eq 0 ]; then
        echo "" >> "$profile"
      fi
      
      # Add PATH export with appropriate syntax
      if [ "$SHELL_NAME" = "fish" ]; then
        echo "" >> "$profile"
        echo "# Added by Supremo installer" >> "$profile"
        echo "fish_add_path $dest" >> "$profile"
      else
        echo "" >> "$profile"
        echo "# Added by Supremo installer" >> "$profile"
        echo "export PATH=\"$dest:\$PATH\"" >> "$profile"
      fi
      echo "  Updated: $profile"
    fi
  done

  # Update current session
  export PATH="$dest:$PATH"
  echo ""
  echo "PATH configured. Restart your terminal or run:"
  echo "  export PATH=\"$dest:\$PATH\""
  echo "Then run: supremo"
}

setup_path
