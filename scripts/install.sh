#!/usr/bin/env sh
set -eu

# Formatting tokens
if [ -t 1 ]; then
  RESET="\033[0m"
  BOLD="\033[1m"
  DIM="\033[2m"
  WHITE="\033[1;37m"
  GRAY="\033[38;2;140;145;155m"
  MUTED="\033[38;2;100;105;115m"
  ACCENT="\033[38;2;232;184;74m"
  SUCCESS="\033[38;2;100;200;130m"
  ERROR="\033[38;2;240;100;100m"
else
  RESET=""
  BOLD=""
  DIM=""
  WHITE=""
  GRAY=""
  MUTED=""
  ACCENT=""
  SUCCESS=""
  ERROR=""
fi

OWNER="AbhaySingh002"
REPO="supremo"
HOME_DIR="${HOME:-}"

info() {
  printf "  %b·%b %b%s%b\n" "$MUTED" "$RESET" "$GRAY" "$1" "$RESET"
}

step() {
  printf "  %b→%b %b%s%b\n" "$ACCENT" "$RESET" "$WHITE" "$1" "$RESET"
}

success() {
  printf "  %b✓%b %b%s%b\n" "$SUCCESS" "$RESET" "$WHITE" "$1" "$RESET"
}

warn() {
  printf "  %b!%b %b%s%b\n" "$ACCENT" "$RESET" "$GRAY" "$1" "$RESET" >&2
}

fail() {
  printf "\n  %b× %s%b\n\n" "$ERROR" "$1" "$RESET" >&2
  exit 1
}

# Banner
printf "\n"
printf "%b  ░█▀▀░█░█░█▀█░█▀▄░█▀▀░█▄█░█▀█%b\n" "$ACCENT" "$RESET"
printf "%b  ░▀▀█░█░█░█▀▀░█▀▄░█▀▀░█░█░█░█%b\n" "$ACCENT" "$RESET"
printf "%b  ░▀▀▀░▀▀▀░▀░░░▀░▀░▀▀▀░▀░▀░▀▀▀%b\n" "$ACCENT" "$RESET"
printf "\n"
printf "  %b%bSUPREMO%b %b· Agentic coding in your local workspace%b\n" "$BOLD" "$WHITE" "$RESET" "$GRAY" "$RESET"
printf "\n"

[ -n "$HOME_DIR" ] || fail "HOME environment variable is not set"
DEST_DIR="$HOME_DIR/.local/bin"

# 1. Platform Detection
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

info "Detected platform: $os / $arch"

# 2. Version Resolution
info "Resolving latest version..."
VERSION_URL="https://raw.githubusercontent.com/$OWNER/$REPO/main/VERSION"
VERSION=$(curl -fsSL --connect-timeout 10 "$VERSION_URL" | tr -d '\r\n') || fail "Failed to fetch VERSION from $VERSION_URL"
[ -n "$VERSION" ] || fail "Resolved version is empty"

info "Latest version: $VERSION"

ASSET="supremo_${VERSION}_${os}_${arch}.tar.gz"
CHECKSUMS="checksums.txt"
BASE_URL="https://github.com/$OWNER/$REPO/releases/download/$VERSION"

# 3. Temporary Directory
TMP_DIR="${TMPDIR:-/tmp}/supremo.$$"
(umask 077 && mkdir "$TMP_DIR") || fail "Failed to create temporary directory"
trap 'rm -rf "$TMP_DIR"' 0
trap 'exit 1' HUP INT TERM

download_with_progress() {
  url="$1"
  dest="$2"
  label="$3"

  step "Downloading $label..."

  # Get Content-Length if available
  total_bytes=$(curl -sLI "$url" | awk -F': ' 'tolower($1)=="content-length"{val=$2} END{print val}' | tr -d '\r\n')
  case "$total_bytes" in
    ''|*[!0-9]*) total_bytes=0 ;;
  esac

  curl -fsSL --connect-timeout 15 "$url" -o "$dest" &
  curl_pid=$!

  bar_width=24

  if [ -t 1 ]; then
    while kill -0 "$curl_pid" 2>/dev/null; do
      if [ -f "$dest" ]; then
        cur_bytes=$(wc -c < "$dest" 2>/dev/null || printf 0)
      else
        cur_bytes=0
      fi
      cur_bytes=$(printf "%d" "$cur_bytes" 2>/dev/null || printf 0)

      if [ "$total_bytes" -gt 0 ]; then
        pct=$(( cur_bytes * 100 / total_bytes ))
        [ "$pct" -gt 100 ] && pct=100
        filled=$(( pct * bar_width / 100 ))
        empty=$(( bar_width - filled ))

        bar_f=""
        i=0
        while [ "$i" -lt "$filled" ]; do
          bar_f="${bar_f}█"
          i=$(( i + 1 ))
        done
        bar_e=""
        i=0
        while [ "$i" -lt "$empty" ]; do
          bar_e="${bar_e}░"
          i=$(( i + 1 ))
        done

        cur_mb=$(awk -v b="$cur_bytes" 'BEGIN {printf "%.1f", b/1048576}')
        tot_mb=$(awk -v b="$total_bytes" 'BEGIN {printf "%.1f", b/1048576}')

        printf "\r    %b%s%b%s %b%3d%%%b %b(%s MB / %s MB)%b  " \
          "$ACCENT" "$bar_f" "$MUTED" "$bar_e" "$WHITE" "$pct" "$RESET" "$MUTED" "$cur_mb" "$tot_mb" "$RESET"
      else
        cur_mb=$(awk -v b="$cur_bytes" 'BEGIN {printf "%.1f", b/1048576}')
        printf "\r    %b▰▰▰▱▱▱%b %b%s MB downloaded...%b  " \
          "$ACCENT" "$RESET" "$MUTED" "$cur_mb" "$RESET"
      fi
      sleep 0.08
    done
    printf "\r%80s\r" ""
  fi

  wait "$curl_pid" || fail "Download failed for $label"
  success "Downloaded $label"
}

# 4. Download & Verify
download_with_progress "$BASE_URL/$ASSET" "$TMP_DIR/$ASSET" "$ASSET"

info "Fetching checksums..."
curl -fsSL --connect-timeout 10 "$BASE_URL/$CHECKSUMS" -o "$TMP_DIR/$CHECKSUMS" || fail "Download failed for $CHECKSUMS"

checksum_line=$(awk -v asset="$ASSET" '$2 == asset { print; exit }' "$TMP_DIR/$CHECKSUMS")
[ -n "$checksum_line" ] || fail "Checksum entry not found for $ASSET"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && printf "%s\n" "$checksum_line" | sha256sum -c - >/dev/null 2>&1) || fail "Checksum verification failed"
elif command -v shasum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && printf "%s\n" "$checksum_line" | shasum -a 256 -c - >/dev/null 2>&1) || fail "Checksum verification failed"
else
  fail "No SHA256 verifier found (need sha256sum or shasum)"
fi
success "Checksum verified"

# 5. Extract & Install
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR" || fail "Failed to extract archive"
[ -f "$TMP_DIR/supremo" ] || fail "Archive does not contain supremo binary"

mkdir -p "$DEST_DIR" || fail "Failed to create $DEST_DIR"
mv -f "$TMP_DIR/supremo" "$DEST_DIR/supremo" || fail "Failed to install binary"
chmod 0755 "$DEST_DIR/supremo" || fail "Failed to make supremo executable"
success "Installed binary to $DEST_DIR/supremo"

# 6. Configure PATH
in_path="false"
case ":$PATH:" in
  *":$DEST_DIR:"*) in_path="true" ;;
esac

if [ "$in_path" = "false" ]; then
  profile=""
  path_line="export PATH=\"$DEST_DIR:\$PATH\""

  case "${SHELL:-sh}" in
    */zsh|zsh) profile="$HOME_DIR/.zshrc" ;;
    */bash|bash) profile="$HOME_DIR/.bashrc" ;;
    */fish|fish)
      profile="$HOME_DIR/.config/fish/config.fish"
      path_line="fish_add_path \"$DEST_DIR\""
      ;;
    *) profile="$HOME_DIR/.profile" ;;
  esac

  if [ -n "$profile" ]; then
    profile_dir=${profile%/*}
    mkdir -p "$profile_dir" || true
    if [ ! -f "$profile" ] || ! grep -F -q "$DEST_DIR" "$profile"; then
      printf '\n# Added by Supremo installer\n%s\n' "$path_line" >> "$profile" 2>/dev/null || true
      info "Added $DEST_DIR to PATH in $profile"
    fi
  fi
fi

# 7. Summary
printf "\n"
printf "  %b%b✓ Supremo %s is ready!%b\n" "$BOLD" "$SUCCESS" "$VERSION" "$RESET"
printf "\n"
if [ "$in_path" = "true" ]; then
  printf "  %bRun:%b\n" "$GRAY" "$RESET"
  printf "    %b%bsupremo%b\n" "$BOLD" "$WHITE" "$RESET"
else
  printf "  %bOpen a new terminal, or run:%b\n" "$GRAY" "$RESET"
  printf "    %bexport PATH=\"$DEST_DIR:\$PATH\"%b\n" "$WHITE" "$RESET"
  printf "    %b%bsupremo%b\n" "$BOLD" "$WHITE" "$RESET"
fi
printf "\n"
