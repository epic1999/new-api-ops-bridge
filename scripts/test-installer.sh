#!/bin/sh
# Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
# Mock Release transport/systemd only in a fresh disposable container.
set -eu
[ -f /.dockerenv ] && [ "$(id -u)" = 0 ] || { echo 'Use a disposable root Docker container'; exit 1; }
[ ! -e /etc/new-api-ops-bridge ] || { echo 'Use a fresh container'; exit 1; }
test_dir=$(mktemp -d)
export test_dir
mkdir "$test_dir/bin" /run/systemd/system -p
cat > "$test_dir/bridge" <<'BRIDGE'
#!/bin/sh
case "$1" in
 init) mkdir -p /etc/new-api-ops-bridge; printf 'fixture-config\n' > /etc/new-api-ops-bridge/config.json ;;
 credentials) printf 'Fixture bridge credentials (test only)\n' ;;
 *) exit 1 ;;
esac
BRIDGE
cat > "$test_dir/bin/uname" <<'UNAME'
#!/bin/sh
case "$1" in -s) echo Linux ;; -m) echo "$TEST_MACHINE" ;; *) exit 1 ;; esac
UNAME
cat > "$test_dir/bin/systemctl" <<'SYSTEMCTL'
#!/bin/sh
exit 0
SYSTEMCTL
cat > "$test_dir/bin/curl" <<'CURL'
#!/bin/sh
set -eu
url='';output=''
while [ "$#" -gt 0 ];do
 case "$1" in -o) output=$2;shift 2 ;; https://*) url=$1;shift ;; *) shift ;; esac
done
case "$url" in
 https://github.com/test-owner/bridge/releases/download/v0.1.0/checksums.txt) cp "$test_dir/checksums.txt" "$output" ;;
 https://github.com/test-owner/bridge/releases/download/v0.1.0/new-api-ops-bridge-linux-*)
  [ "${url##*/}" = "new-api-ops-bridge-linux-$TEST_ASSET" ] || exit 1
  cp "$test_dir/bridge" "$output" ;;
 *) exit 1 ;;
esac
CURL
chmod 700 "$test_dir/bin/"* "$test_dir/bridge"
PATH="$test_dir/bin:$PATH";export PATH
sed -e 's|__REPOSITORY__|test-owner/bridge|g' -e 's|__RELEASE__|v0.1.0|g' install.sh > "$test_dir/install.sh"
digest=$(sha256sum "$test_dir/bridge" | cut -d ' ' -f 1)
for pair in x86_64:amd64 aarch64:arm64 i686:386 armv6l:armv6 armv7l:armv7 riscv64:riscv64 ppc64le:ppc64le s390x:s390x loongarch64:loong64 mips:mips mipsel:mipsle mips64:mips64 mips64el:mips64le;do
 TEST_MACHINE=${pair%:*};TEST_ASSET=${pair#*:};export TEST_MACHINE TEST_ASSET
 printf '%s  new-api-ops-bridge-linux-%s\n' "$digest" "$TEST_ASSET" > "$test_dir/checksums.txt"
 sh "$test_dir/install.sh" --origin https://ai.example --host server.example --lang zh > "$test_dir/output"
 grep -q '安装完成' "$test_dir/output"
 printf 'PASS installer CPU %s -> %s\n' "$TEST_MACHINE" "$TEST_ASSET"
done
for language in zh en fr ru ja vi;do
 sh "$test_dir/install.sh" --origin https://ai.example --host server.example --lang "$language" > "$test_dir/output"
 [ -s "$test_dir/output" ];printf 'PASS installer language %s\n' "$language"
done
# Reinstall preserves existing generated settings.
[ "$(cat /etc/new-api-ops-bridge/config.json)" = fixture-config ]
printf '%s  new-api-ops-bridge-linux-%s\n' "$(printf '0%.0s' 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60 61 62 63 64)" "$TEST_ASSET" > "$test_dir/checksums.txt"
if sh "$test_dir/install.sh" --origin https://ai.example --host server.example >/dev/null 2>&1;then echo 'Bad checksum accepted';exit 1;fi
if sh "$test_dir/install.sh" --password chosen >/dev/null 2>&1;then echo 'Custom password accepted';exit 1;fi
printf 'PASS installer rejects corrupted assets and custom passwords\n'
