#!/bin/sh
# Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
# Mock Release transport/systemd only in a fresh disposable container.
set -eu
[ -f /.dockerenv ] && [ "$(id -u)" = 0 ] || { echo 'Use a disposable root Docker container'; exit 1; }
[ ! -e /etc/new-api-ops-bridge ] || { echo 'Use a fresh container'; exit 1; }
test_dir=$(mktemp -d)
export test_dir
mkdir "$test_dir/bin" /run/systemd/system -p
# 假程序按真实格式写配置，安装脚本读端口、网址、地址的逻辑才测得到
cat > "$test_dir/bridge" <<'BRIDGE'
#!/bin/sh
case "$1" in
 init)
  shift;o='';h=''
  while [ "$#" -gt 0 ];do case "$1" in --origin) o=$2;shift 2 ;; --public-host) h=$2;shift 2 ;; *) shift ;; esac;done
  mkdir -p /etc/new-api-ops-bridge
  printf '{"password":"fixture","port":23456,"origin":"%s","public_host":"%s","user":"new-api-ops","home":"/var/lib/new-api-ops-bridge"}\n' "$o" "$h" > /etc/new-api-ops-bridge/config.json ;;
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
# 假装 ufw 已启用，检查放行命令里带上实际端口
cat > "$test_dir/bin/ufw" <<'UFW'
#!/bin/sh
[ "$1" = status ] && echo 'Status: active'
UFW
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
config=/etc/new-api-ops-bridge/config.json
TEST_MACHINE=x86_64;TEST_ASSET=amd64;export TEST_MACHINE TEST_ASSET
printf '%s  new-api-ops-bridge-linux-%s\n' "$digest" "$TEST_ASSET" > "$test_dir/checksums.txt"
# 用不了的地址要在下载前当场拦下，并且不能留下任何配置
for item in 'ai.example:playground site itself' 'https://AI.example:8443/:playground site itself' '127.0.0.1:cannot be used' 'localhost:cannot be used' 'bad host:cannot be used' '999.1.1.1:cannot be used'; do
 bad=${item%:*};reason=${item##*:}
 if sh "$test_dir/install.sh" --origin https://ai.example --host "$bad" --lang en >/dev/null 2>"$test_dir/err";then echo "Accepted unusable host $bad";exit 1;fi
 grep -q "$reason" "$test_dir/err" || { echo "Wrong rejection for $bad";cat "$test_dir/err";exit 1; }
 [ ! -e /etc/new-api-ops-bridge ] || { echo "Rejected host $bad left state";exit 1; }
done
if sh "$test_dir/install.sh" --origin 'ftp://ai.example' --host server.example --lang en >/dev/null 2>"$test_dir/err";then echo 'Accepted invalid origin';exit 1;fi
grep -q 'Invalid playground address' "$test_dir/err"
printf 'PASS installer rejects unusable hosts and origins before downloading\n'
# 没有终端又测不到公网 IP：提示加 --host 后退出，不能卡住等输入
if sh "$test_dir/install.sh" --origin https://ai.example --lang en </dev/null >/dev/null 2>"$test_dir/err";then echo 'Installed without a host';exit 1;fi
grep -q -- '--host' "$test_dir/err"
printf 'PASS installer explains a missing host without a terminal\n'
# 随手填的地址自动纠正；装完给出带端口的下一步清单和防火墙命令
sh "$test_dir/install.sh" --origin 'https://AI.example/' --host ' HTTPS://Server.Example:8443/path ' --lang en > "$test_dir/output"
grep -q '"public_host":"server.example"' "$config"
grep -q '"origin":"https://ai.example"' "$config"
grep -q 'Using address: server.example' "$test_dir/output"
grep -q 'Allow TCP port 23456' "$test_dir/output"
grep -q 'sudo ufw allow from YOUR_IP to any port 23456 proto tcp' "$test_dir/output"
grep -q 'exactly https://ai.example,' "$test_dir/output"
if grep -q 'private network address' "$test_dir/output";then echo 'Public host reported as private';exit 1;fi
first_config=$(cat "$config")
printf 'PASS installer normalizes the host and prints next steps\n'
for pair in x86_64:amd64 aarch64:arm64 i686:386 armv6l:armv6 armv7l:armv7 riscv64:riscv64 ppc64le:ppc64le s390x:s390x loongarch64:loong64 mips:mips mipsel:mipsle mips64:mips64 mips64el:mips64le;do
 TEST_MACHINE=${pair%:*};TEST_ASSET=${pair#*:};export TEST_MACHINE TEST_ASSET
 printf '%s  new-api-ops-bridge-linux-%s\n' "$digest" "$TEST_ASSET" > "$test_dir/checksums.txt"
 sh "$test_dir/install.sh" --origin https://ai.example --host server.example --lang zh > "$test_dir/output"
 grep -q '安装完成' "$test_dir/output"
 printf 'PASS installer CPU %s -> %s\n' "$TEST_MACHINE" "$TEST_ASSET"
done
for language in zh en fr ru ja vi;do
 sh "$test_dir/install.sh" --origin https://ai.example --host server.example --lang "$language" > "$test_dir/output"
 grep -q '23456' "$test_dir/output";printf 'PASS installer language %s\n' "$language"
done
# 重装只升级程序：配置原样保留，换了训练场网址要明确提醒
sh "$test_dir/install.sh" --origin https://other.example --host other.example --lang en > "$test_dir/output"
[ "$(cat "$config")" = "$first_config" ]
grep -q 'Existing installation found' "$test_dir/output"
grep -q 'bound to the playground at https://ai.example' "$test_dir/output"
printf 'PASS reinstall keeps settings and warns about another playground\n'
printf '%s  new-api-ops-bridge-linux-%s\n' "$(printf '0%.0s' 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60 61 62 63 64)" "$TEST_ASSET" > "$test_dir/checksums.txt"
if sh "$test_dir/install.sh" --origin https://ai.example --host server.example >/dev/null 2>&1;then echo 'Bad checksum accepted';exit 1;fi
if sh "$test_dir/install.sh" --password chosen >/dev/null 2>&1;then echo 'Custom password accepted';exit 1;fi
printf 'PASS installer rejects corrupted assets and custom passwords\n'
# 启动时要写证书目录续证，unit 必须单独放开它
grep -qx 'ReadWritePaths=/etc/new-api-ops-bridge' /etc/systemd/system/new-api-ops-bridge.service
printf 'PASS service unit allows certificate renewal\n'
# 内网地址要提醒浏览器的本地网络权限
rm -rf /etc/new-api-ops-bridge
printf '%s  new-api-ops-bridge-linux-%s\n' "$digest" "$TEST_ASSET" > "$test_dir/checksums.txt"
sh "$test_dir/install.sh" --origin https://ai.example --host 192.168.1.10 --lang en > "$test_dir/output"
grep -q 'Note: 192.168.1.10 is a private network address' "$test_dir/output"
printf 'PASS installer explains local network access for private hosts\n'
