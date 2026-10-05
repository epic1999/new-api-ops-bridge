#!/bin/sh
# Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
# Run inside a disposable root Linux environment, never on a production server.
set -eu
[ "$(id -u)" = 0 ] || { echo 'Use a disposable root container'; exit 1; }
account=ops-bridge-test
id "$account" >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/ops-bridge-test --shell /usr/sbin/nologin "$account"
work_dir=$(mktemp -d)
trap 'rm -f "$work_dir/bridge" "$work_dir/tests"; rmdir "$work_dir"' EXIT
CGO_ENABLED=0 go build -o "$work_dir/bridge" .
go test -race -c -o "$work_dir/tests" .
BRIDGE_TEST_BINARY="$work_dir/bridge" BRIDGE_TEST_USER="$account" "$work_dir/tests" -test.v
go vet ./...
sh -n install.sh
