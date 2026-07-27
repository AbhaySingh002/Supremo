#!/usr/bin/env sh
set -eu

PRIMARY="\033[32m"
BOLD_PRIMARY="\033[1;32m"
ERROR="\033[31m"
RESET="\033[0m"

OWNER="AbhaySingh002"
REPO="supremo"
HOME_DIR="${HOME:-}"

step() {
  printf "%b==> %s%b\n" "$PRIMARY" "$1" "$RESET"
}

success() {
  printf "%b==> %s%b\n" "$BOLD_PRIMARY" "$1" "$RESET"
}

warn() {
  printf "%b==> %s%b\n" "$ERROR" "$1" "$RESET" >&2
}

fail() {
  warn "$1"
  exit 1
}

[ -n "$HOME_DIR" ] || fail "HOME is not set"
DEST_DIR="$HOME_DIR/.local/bin"

configure_path() {
  path_line=

  case ":$PATH:" in
    *":$DEST_DIR:"*)
      success "$DEST_DIR is already in PATH"
      success "Run: supremo --version"
      return
      ;;
  esac

  case "${SHELL:-sh}" in
    */zsh|zsh) profile="$HOME_DIR/.zshrc" ;;
    */bash|bash) profile="$HOME_DIR/.bashrc" ;;
    */fish|fish)
      profile="$HOME_DIR/.config/fish/config.fish"
      path_line="fish_add_path \"$DEST_DIR\""
      ;;
    *) profile="$HOME_DIR/.profile" ;;
  esac

  if [ "${path_line:-}" = "" ]; then
    path_line="export PATH=\"$DEST_DIR:\$PATH\""
  fi

  profile_dir=${profile%/*}
  mkdir -p "$profile_dir" || fail "Failed to create $profile_dir"
  if [ ! -f "$profile" ] || ! grep -F -q "$DEST_DIR" "$profile"; then
    printf '\n# Added by Supremo installer\n%s\n' "$path_line" >> "$profile" || fail "Failed to update $profile"
    success "Added $DEST_DIR to PATH in $profile"
  fi

  warn "$DEST_DIR is not in the current shell's PATH"
  warn "Open a new terminal, or run: export PATH=\"$DEST_DIR:\$PATH\""
}

step "Detecting platform"
kernel=$(uname -s 2>/dev/null || printf 'unknown')
machine=$(uname -m 2>/dev/null || printf 'unknown')

case "$kernel" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) fail "Unsupported operating system: $kernel (supported: Linux, Darwin)" ;;
esac

case "$machine" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "Unsupported architecture: $machine (supported: amd64, arm64)" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required to install supremo"

step "Fetching latest version from VERSION file"
VERSION_URL="https://raw.githubusercontent.com/$OWNER/$REPO/main/VERSION"
VERSION=$(curl -fsSL "$VERSION_URL" | tr -d '\r\n') || fail "Failed to fetch VERSION from $VERSION_URL"
[ -n "$VERSION" ] || fail "Resolved version is empty"

ASSET="supremo_${VERSION}_${os}_${arch}.tar.gz"
CHECKSUMS="checksums.txt"
BASE_URL="https://github.com/$OWNER/$REPO/releases/download/$VERSION"

step "Preparing temporary directory"
TMP_DIR="${TMPDIR:-/tmp}/supremo.$$"
(umask 077 && mkdir "$TMP_DIR") || fail "Failed to create temporary directory"
trap 'rm -rf "$TMP_DIR"' 0
trap 'exit 1' HUP INT TERM

step "Downloading $ASSET"
curl -fL -# "$BASE_URL/$ASSET" -o "$TMP_DIR/$ASSET" || fail "Download failed for $ASSET"

step "Downloading $CHECKSUMS"
curl -fL -# "$BASE_URL/$CHECKSUMS" -o "$TMP_DIR/$CHECKSUMS" || fail "Download failed for $CHECKSUMS"

step "Verifying checksum"
checksum_line=$(awk -v asset="$ASSET" '$2 == asset { print; exit }' "$TMP_DIR/$CHECKSUMS")
[ -n "$checksum_line" ] || fail "Checksum entry not found for $ASSET"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && printf "%s\n" "$checksum_line" | sha256sum -c - >/dev/null) || fail "Checksum verification failed"
elif command -v shasum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && printf "%s\n" "$checksum_line" | shasum -a 256 -c - >/dev/null) || fail "Checksum verification failed"
else
  fail "No SHA256 verifier found (need sha256sum or shasum)"
fi
success "Checksum verified"

step "Extracting archive"
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR" || fail "Failed to extract archive"
[ -f "$TMP_DIR/supremo" ] || fail "Archive does not contain supremo binary"

step "Installing to $DEST_DIR"
mkdir -p "$DEST_DIR" || fail "Failed to create $DEST_DIR"
mv -f "$TMP_DIR/supremo" "$DEST_DIR/supremo" || fail "Failed to install binary"
chmod 0755 "$DEST_DIR/supremo" || fail "Failed to make supremo executable"

success "supremo installed to $DEST_DIR/supremo"
configure_path
