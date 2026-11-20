#!/bin/bash
set -e
cd "$(dirname "$0")"

export ADDR="${ADDR:-:8082}"

BIN=endpoint-collector-static

# Build only when the binary is missing or not newer than every source file,
# so that a prebuilt binary starts without a Go toolchain.
needs_build() {
  [ -x "$BIN" ] || return 0
  local f
  for f in *.go go.mod go.sum; do
    [ -e "$f" ] || continue
    if ! [ "$BIN" -nt "$f" ]; then
      return 0
    fi
  done
  return 1
}

if needs_build; then
  if [ -x "$BIN" ] && ! command -v go >/dev/null 2>&1; then
    echo "$BIN: sources are not older than the binary, but there is no Go toolchain; starting the existing binary" >&2
  else
    GOTOOLCHAIN=local go build -o "$BIN" .
  fi
fi
exec "./$BIN"
