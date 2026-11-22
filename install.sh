#!/bin/sh
# capbus installer for Linux and macOS.
#
# Prefers a published release; falls back to building from source, which is the
# state of the project today. DEST lets you install elsewhere (and is how this
# script is tested without touching a real installation).
set -eu

REPO="mskl888/capbus"
DEST="${DEST:-$HOME/.local/bin}"

say()  { printf '  %s\n' "$1"; }
ok()   { printf '  [ok] %s\n' "$1"; }
warn() { printf '  [!]  %s\n' "$1"; }

printf '\ncapbus installer\n----------------\n'

case "$(uname -s)" in
  Linux*)  os=linux ;;
  Darwin*) os=darwin ;;
  *)       echo "unsupported OS: $(uname -s)"; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *)            arch=amd64 ;;
esac

mkdir -p "$DEST"
got=0

# 1. published binary
say "looking for a release build..."
if curl -fsSL --max-time 60 -o "$DEST/capbus" \
     "https://github.com/$REPO/releases/latest/download/capbus-$os-$arch" 2>/dev/null; then
  chmod +x "$DEST/capbus"
  got=1
  ok "downloaded capbus-$os-$arch"
else
  warn "no release build available yet"
fi

# 2. build from source
if [ "$got" -eq 0 ]; then
  if ! command -v go >/dev/null 2>&1; then
    printf '\nneeds Go to build from source: https://go.dev/dl/\n'
    exit 1
  fi
  say "building from source ($(go version | awk '{print $3}'))..."
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  curl -fsSL --max-time 120 -o "$tmp/src.zip" \
    "https://codeload.github.com/$REPO/zip/refs/heads/main"
  unzip -q "$tmp/src.zip" -d "$tmp"
  src="$(find "$tmp" -maxdepth 1 -type d -name 'capbus-*' | head -1)"
  ( cd "$src" && go build -o "$DEST/capbus" ./cmd/capbus )
  chmod +x "$DEST/capbus"
  ok "built $DEST/capbus"
fi

ok "$("$DEST/capbus" version)"

case ":$PATH:" in
  *":$DEST:"*) ok "PATH already contains $DEST" ;;
  *)
    say "add this to your shell profile to use it everywhere:"
    printf '      export PATH="%s:$PATH"\n' "$DEST"
    ;;
esac

printf '\ndone. next:\n'
say "capbus init        # tells you exactly what is left to do"
printf '\n'
