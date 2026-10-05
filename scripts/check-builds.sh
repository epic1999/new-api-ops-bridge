#!/bin/sh
# Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
set -eu
build_dir=$(mktemp -d)
trap 'rm -f "$build_dir"/bridge-*; rmdir "$build_dir"' EXIT
export build_dir
printf '%s\n' amd64 arm64 386 armv6 armv7 riscv64 ppc64le s390x loong64 mips mipsle mips64 mips64le | xargs -n 1 -P 1 sh -c '
  arch=$1; goarch=$arch; goarm=""
  case "$arch" in armv6) goarch=arm; goarm=6 ;; armv7) goarch=arm; goarm=7 ;; esac
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" GOMIPS=softfloat GOMIPS64=softfloat go build -p 1 -trimpath -o "$build_dir/bridge-$arch" . || exit 1
  printf "PASS linux/%s\n" "$arch"
' sh
