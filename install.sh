#!/bin/sh
# Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
# Release workflow replaces the repository/version, so forks need no code edits.
set -eu
umask 077
repo='__REPOSITORY__'
release='__RELEASE__'
state=/etc/new-api-ops-bridge
origin=''
lang=zh
host=''
msg() {
  case "$lang" in
    zh) text=$2 ;; en) text=$3 ;; fr) text=$4 ;; ru) text=$5 ;; ja) text=$6 ;; vi) text=$7 ;; *) text=$2 ;;
  esac
  printf '%s\n' "$text"
}
die() { msg error "$1" "$2" "$3" "$4" "$5" "$6" >&2; exit 1; }
# 只从终端读输入；非交互执行时没有终端，返回失败让调用方兜底
ask() { answer=''; { IFS= read -r answer </dev/tty; } 2>/dev/null; }
# 训练场网址统一成浏览器 Origin 头的样子：小写、不带结尾斜杠，只能是 https（本机调试除外）
clean_origin() {
  o=$(printf '%s' "$1" | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')
  o=${o%/}
  # 只填了域名就默认补上 https://
  case "$o" in ''|*://*) ;; *) o="https://$o" ;; esac
  case "$o" in
    http://localhost|http://localhost:*|http://127.0.0.1|http://127.0.0.1:*) printf '%s' "$o"; return 0 ;;
    https://?*) ;;
    *) return 1 ;;
  esac
  case "${o#https://}" in */*|*\?*|*#*|*@*) return 1 ;; esac
  printf '%s' "$o"
}
# 用户随手填的地址收拾成浏览器 Host 头里的写法：去掉协议、路径、端口和方括号，域名转小写
clean_host() {
  h=$(printf '%s' "$1" | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')
  h=${h#http://}
  h=${h#https://}
  h=${h%%/*}
  case "$h" in
    \[*\]*) h=${h#\[}; h=${h%%\]*} ;;
    *:*:*) ;;
    *:*) h=${h%%:*} ;;
  esac
  printf '%s' "${h%.}"
}
# 浏览器得从外面访问得到：回环、未指定地址、单段主机名都不行，IPv6 细节交给程序校验
valid_host() {
  case "$1" in
    ''|localhost|*.localhost|::|::1|*[!a-z0-9.:-]*|*..*|.*|-*|*-) return 1 ;;
    *:*) return 0 ;;
    *[!0-9.]*) case "$1" in *.*) return 0 ;; esac; return 1 ;;
  esac
  # 纯数字按 IPv4 校验：四段、每段不超过 255，0 和 127 开头的本机地址不行
  printf '%s\n' "$1" | awk -F. 'NF == 4 && $1 != 0 && $1 != 127 { for (i = 1; i <= 4; i++) if ($i == "" || $i > 255) exit 1; exit 0 } { exit 1 }'
}
# 内网地址会触发 Chrome / Edge 的本地网络权限弹窗；域名解析到哪不归这里管
is_private() {
  case "$1" in
    *:*) case "$1" in f[cd]*|fe[89ab]*) return 0 ;; esac; return 1 ;;
    *[!0-9.]*) return 1 ;;
    10.*|192.168.*|169.254.*|172.1[6-9].*|172.2[0-9].*|172.3[01].*) return 0 ;;
    100.6[4-9].*|100.[7-9][0-9].*|100.1[01][0-9].*|100.12[0-7].*) return 0 ;;
  esac
  return 1
}
say_invalid_host() { msg error '无法使用这个地址。请填写浏览器能访问到的公网 IP 或域名，例如 203.0.113.10 或 bridge.example.com。' 'This address cannot be used. Enter a public IP or domain your browser can reach, such as 203.0.113.10 or bridge.example.com.' 'Cette adresse est inutilisable. Saisissez une IP publique ou un domaine accessible depuis votre navigateur, par exemple 203.0.113.10 ou bridge.example.com.' 'Этот адрес нельзя использовать. Укажите публичный IP или домен, доступный из браузера, например 203.0.113.10 или bridge.example.com.' 'このアドレスは使用できません。ブラウザからアクセスできるグローバル IP またはドメインを入力してください（例：203.0.113.10、bridge.example.com）。' 'Không thể dùng địa chỉ này. Hãy nhập IP công khai hoặc tên miền mà trình duyệt truy cập được, ví dụ 203.0.113.10 hoặc bridge.example.com.' >&2; }
say_same_site() { msg error '这是训练场网站自身的地址，浏览器不会把桥密码发送给网站本身。请改填服务器 IP，或为桥单独准备一个子域名。' 'This is the playground site itself. Browsers will not send the bridge password to the site, so enter the server IP or a separate subdomain for the bridge.' "C'est l'adresse du playground lui-même : le navigateur n'enverra pas le mot de passe du pont au site. Saisissez l'IP du serveur ou un sous-domaine dédié au pont." 'Это адрес самого playground: браузер не отправит пароль моста этому сайту. Укажите IP сервера или отдельный поддомен для моста.' 'これは訓練場サイト自身のアドレスです。ブラウザはブリッジのパスワードをサイト自身には送信しません。サーバーの IP か、ブリッジ専用のサブドメインを入力してください。' 'Đây là địa chỉ của chính trang playground; trình duyệt sẽ không gửi mật khẩu cầu nối đến trang này. Hãy nhập IP máy chủ hoặc một tên miền con riêng cho cầu nối.' >&2; }
say_invalid_origin() { msg error '训练场网址格式不正确，请填写完整网址，例如 https://ai.example.com。' 'Invalid playground address. Enter the full address, such as https://ai.example.com.' "Adresse du playground invalide. Saisissez l'adresse complète, par exemple https://ai.example.com." 'Неверный адрес playground. Укажите полный адрес, например https://ai.example.com.' '訓練場のアドレスが正しくありません。https://ai.example.com のように完全なアドレスを入力してください。' 'Địa chỉ playground không hợp lệ. Hãy nhập đầy đủ, ví dụ https://ai.example.com.' >&2; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --repo|--origin|--lang|--host)
      [ "$#" -ge 2 ] || exit 2
      case "$1" in --repo) repo=$2 ;; --origin) origin=$2 ;; --lang) lang=$2 ;; --host) host=$2 ;; esac
      shift 2 ;;
    *) printf 'Unknown option: %s (custom passwords/ports are forbidden)\n' "$1" >&2; exit 2 ;;
  esac
done
case "$lang" in zh|en|fr|ru|ja|vi) ;; *) lang=zh ;; esac
case "$repo" in *[!a-zA-Z0-9_./-]*|''|*..*) die '仓库地址无效。请输入 owner/repository。' 'Invalid repository. Use owner/repository.' 'Dépôt invalide. Utilisez owner/repository.' 'Неверный репозиторий: owner/repository.' 'リポジトリは owner/repository の形式で入力してください。' 'Kho không hợp lệ. Dùng owner/repository.' ;; esac
[ "${repo#*/}" != "$repo" ] && [ "${repo#*/}" = "${repo##*/}" ] || exit 2
printf '%s\n' "$release" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$' || die '请从 GitHub Releases 下载已发布的安装脚本，或按 README 从源码构建。' 'Download the published installer from GitHub Releases or follow the source-build README.' 'Téléchargez le script publié dans GitHub Releases ou suivez le README.' 'Загрузите опубликованный скрипт из GitHub Releases или следуйте README.' 'GitHub Releases の公開済みスクリプトを使用するか README に従ってください。' 'Tải trình cài đặt đã phát hành từ GitHub Releases hoặc làm theo README.'
if [ "$(id -u)" != 0 ]; then
  command -v sudo >/dev/null 2>&1 || die '需要管理员权限，请以 root 运行此脚本。桥会自动切换为低权限账户。' 'Run as root. The bridge drops to its unprivileged account.' 'Exécutez en root. Le pont utilisera un compte non privilégié.' 'Запустите от root. Мост использует непривилегированную учетную запись.' 'root で実行してください。ブリッジは低権限アカウントで動作します。' 'Chạy với root. Cầu nối sẽ chuyển sang tài khoản ít quyền.'
  exec sudo sh "$0" --repo "$repo" --origin "$origin" --lang "$lang" --host "$host"
fi
[ "$(uname -s)" = Linux ] || die '服务器桥支持 Linux。桌面环境请使用本地桥。' 'The server bridge supports Linux. Use the local bridge for desktops.' 'Le pont serveur prend en charge Linux.' 'Серверный мост поддерживает Linux.' 'サーバーブリッジは Linux に対応しています。' 'Cầu nối máy chủ hỗ trợ Linux.'
command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] || die '需要 systemd。其他环境请按 README 手动运行。' 'systemd is required. For other environments follow the README.' 'systemd est requis. Sinon, suivez le README.' 'Требуется systemd. Для других сред следуйте README.' 'systemd が必要です。その他の環境は README を参照してください。' 'Cần systemd. Với môi trường khác, xem README.'
for cmd in curl sha256sum mktemp install; do
  command -v "$cmd" >/dev/null 2>&1 || die "缺少 $cmd，请用系统包管理器安装后重新运行。" "Missing $cmd. Install it with your package manager and retry." "$cmd manquant. Installez-le puis réessayez." "Нет $cmd. Установите и повторите." "$cmd をインストールして再実行してください。" "Thiếu $cmd. Hãy cài đặt rồi thử lại."
done
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;; i386|i486|i586|i686) arch=386 ;;
  aarch64|arm64) arch=arm64 ;; armv7*) arch=armv7 ;; armv6*) arch=armv6 ;;
  riscv64) arch=riscv64 ;; ppc64le) arch=ppc64le ;; s390x) arch=s390x ;; loongarch64) arch=loong64 ;;
  mips64) arch=mips64 ;; mips64el) arch=mips64le ;; mips) arch=mips ;; mipsel) arch=mipsle ;;
  *) die '暂不支持此 CPU，请提交 Issue 并附上 uname -m 的结果。' 'Unsupported CPU. Open an issue with your uname -m output.' 'CPU non pris en charge. Signalez le résultat de uname -m.' 'CPU не поддерживается. Создайте issue с выводом uname -m.' '未対応 CPU です。uname -m の出力を Issue に添付してください。' 'CPU chưa hỗ trợ. Tạo issue kèm kết quả uname -m.' ;;
esac
if [ -f "$state/config.json" ]; then
  # 装过就只升级程序，地址、端口、密码都不动，免得用户以为重填能改
  msg upgrade '检测到已安装：保留原有地址、端口和密码，只升级程序。如需更换地址，请先删除 /etc/new-api-ops-bridge 再重新安装，届时会生成新的端口和密码。' 'Existing installation found: keeping its address, port and password and upgrading the program only. To change the address, delete /etc/new-api-ops-bridge and install again; a new port and password will be generated.' "Installation existante détectée : adresse, port et mot de passe conservés, seul le programme est mis à jour. Pour changer d'adresse, supprimez /etc/new-api-ops-bridge puis réinstallez ; un nouveau port et un nouveau mot de passe seront générés." 'Обнаружена установка: адрес, порт и пароль сохраняются, обновляется только программа. Чтобы сменить адрес, удалите /etc/new-api-ops-bridge и установите заново; будут созданы новые порт и пароль.' '既存のインストールを検出しました。アドレス、ポート、パスワードはそのままでプログラムのみ更新します。アドレスを変更するには /etc/new-api-ops-bridge を削除して再インストールしてください。新しいポートとパスワードが生成されます。' 'Đã phát hiện bản cài đặt: giữ nguyên địa chỉ, cổng và mật khẩu, chỉ nâng cấp chương trình. Để đổi địa chỉ, hãy xóa /etc/new-api-ops-bridge rồi cài lại; cổng và mật khẩu mới sẽ được tạo.'
  io=$(sed -n 's/.*"origin":"\([^"]*\)".*/\1/p' "$state/config.json")
  requested=$(clean_origin "$origin" || true)
  if [ -n "$io" ] && [ -n "$requested" ] && [ "$requested" != "$io" ]; then
    msg mismatch "注意：这台服务器绑定的训练场是 $io，本次命令中的网址不会生效。请用 $io 打开训练场；如需改绑，请先删除 /etc/new-api-ops-bridge 再重新安装。" "Note: this server is bound to the playground at $io; the address in this command is ignored. Open the playground at $io, or delete /etc/new-api-ops-bridge and install again to bind another one." "Remarque : ce serveur est lié au playground $io ; l'adresse de cette commande est ignorée. Ouvrez le playground à l'adresse $io, ou supprimez /etc/new-api-ops-bridge et réinstallez pour en lier un autre." "Примечание: сервер привязан к playground $io; адрес из этой команды не применяется. Откройте playground по адресу $io или удалите /etc/new-api-ops-bridge и установите заново для другой привязки." "注意：このサーバーは訓練場 $io に紐付けられています。今回のコマンドのアドレスは反映されません。$io で訓練場を開くか、別の訓練場に紐付ける場合は /etc/new-api-ops-bridge を削除して再インストールしてください。" "Lưu ý: máy chủ này đã gắn với playground $io; địa chỉ trong lệnh này sẽ bị bỏ qua. Hãy mở playground tại $io, hoặc xóa /etc/new-api-ops-bridge rồi cài lại để gắn playground khác."
  fi
else
  if [ -n "$origin" ]; then
    origin=$(clean_origin "$origin") || { say_invalid_origin; exit 1; }
  else
    while :; do
      msg prompt '请输入训练场网址（例如 https://ai.example.com）：' 'Enter your playground address (e.g. https://ai.example.com):' 'Adresse du playground (ex. https://ai.example.com) :' 'Адрес playground (например https://ai.example.com):' '訓練場のアドレスを入力してください（例 https://ai.example.com）：' 'Nhập địa chỉ playground (ví dụ https://ai.example.com):'
      ask || die '缺少训练场网址。请使用训练场页面生成的安装命令，或在命令末尾加上 --origin 训练场网址。' 'The playground address is missing. Use the install command generated by the playground, or add --origin followed by the playground address.' "Adresse du playground manquante. Utilisez la commande générée par le playground ou ajoutez --origin suivi de son adresse." 'Не указан адрес playground. Используйте команду установки из playground или добавьте --origin с адресом playground.' '訓練場のアドレスがありません。訓練場ページで生成されたインストールコマンドを使用するか、--origin と訓練場のアドレスを追加してください。' 'Thiếu địa chỉ playground. Hãy dùng lệnh cài đặt do playground tạo, hoặc thêm --origin kèm địa chỉ playground.'
      if origin=$(clean_origin "$answer"); then break; fi
      say_invalid_origin
    done
  fi
  origin_host=$(clean_host "$origin")
  if [ -n "$host" ]; then
    raw=$host
    host=$(clean_host "$raw")
    valid_host "$host" || { say_invalid_host; exit 1; }
    [ "$host" != "$origin_host" ] || { say_same_site; exit 1; }
  else
    detected=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 8 --proto '=https' https://api.ipify.org 2>/dev/null || true)
    detected=$(clean_host "$detected")
    valid_host "$detected" || detected=''
    msg hint '请填写浏览器访问这台服务器使用的公网 IP 或域名。安装后无法修改；服务器 IP 会变化时请填写域名。' 'Enter the public IP or domain your browser will use to reach this server. It cannot be changed after installation; use a domain if the server IP may change.' "Saisissez l'IP publique ou le domaine que votre navigateur utilisera pour joindre ce serveur. Impossible à modifier après l'installation ; utilisez un domaine si l'IP du serveur peut changer." 'Укажите публичный IP или домен, по которому браузер будет обращаться к серверу. После установки его нельзя изменить; если IP сервера может меняться, укажите домен.' 'ブラウザからこのサーバーにアクセスするためのグローバル IP またはドメインを入力してください。インストール後は変更できません。サーバーの IP が変わる可能性がある場合はドメインを使用してください。' 'Nhập IP công khai hoặc tên miền mà trình duyệt dùng để truy cập máy chủ này. Không thể thay đổi sau khi cài đặt; nếu IP máy chủ có thể thay đổi, hãy dùng tên miền.'
    interactive=1
    while :; do
      if [ -n "$detected" ]; then
        msg prompt "公网 IP 或域名（直接回车使用 $detected）：" "Public IP or domain (press Enter to use $detected):" "IP publique ou domaine (Entrée pour utiliser $detected) :" "Публичный IP или домен (Enter — использовать $detected):" "グローバル IP またはドメイン（Enter で $detected を使用）：" "IP công khai hoặc tên miền (nhấn Enter để dùng $detected):"
      else
        msg prompt '公网 IP 或域名：' 'Public IP or domain:' 'IP publique ou domaine :' 'Публичный IP или домен:' 'グローバル IP またはドメイン：' 'IP công khai hoặc tên miền:'
      fi
      if ask; then
        raw=${answer:-$detected}
      else
        # 没有终端时能检测到公网 IP 就直接用，检测不到只能请用户加参数
        [ -n "$detected" ] || die '无法读取键盘输入，也未检测到公网 IP。请在安装命令末尾加上 --host 服务器IP或域名 后重新运行。' 'Cannot read keyboard input and no public IP was detected. Add --host followed by the server IP or domain to the install command and run it again.' "Impossible de lire la saisie et aucune IP publique détectée. Ajoutez --host suivi de l'IP ou du domaine du serveur à la commande, puis relancez-la." 'Невозможно прочитать ввод с клавиатуры, публичный IP не определён. Добавьте к команде --host с IP или доменом сервера и запустите снова.' 'キーボード入力を読み取れず、グローバル IP も検出できませんでした。インストールコマンドの末尾に --host とサーバーの IP またはドメインを追加して再実行してください。' 'Không đọc được dữ liệu nhập và không phát hiện được IP công khai. Hãy thêm --host kèm IP hoặc tên miền máy chủ vào lệnh cài đặt rồi chạy lại.'
        raw=$detected
        interactive=0
      fi
      host=$(clean_host "$raw")
      if ! valid_host "$host"; then
        say_invalid_host
      elif [ "$host" = "$origin_host" ]; then
        say_same_site
      else
        break
      fi
      [ "$interactive" = 1 ] || exit 1
    done
  fi
  # 地址被自动纠正过就告诉用户最终用的是哪个
  if [ "$host" != "${raw:-}" ]; then
    msg using "将使用地址：$host" "Using address: $host" "Adresse utilisée : $host" "Будет использован адрес: $host" "使用するアドレス：$host" "Sẽ dùng địa chỉ: $host"
  fi
fi
temp_dir=$(mktemp -d)
cleanup() { rm -f -- "$temp_dir/bridge" "$temp_dir/checksums.txt" "$temp_dir/expected.txt"; rmdir -- "$temp_dir"; }
trap cleanup EXIT HUP INT TERM
base="https://github.com/$repo/releases/download/$release"
asset="new-api-ops-bridge-linux-$arch"
msg start '正在下载并验证程序，请稍候…' 'Downloading and verifying the bridge…' 'Téléchargement et vérification…' 'Загрузка и проверка…' 'ダウンロードと検証中…' 'Đang tải và xác minh…'
curl --fail --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 "$base/$asset" -o "$temp_dir/bridge" || die '下载失败。请确认仓库已发布对应 CPU 的 Release，并检查网络。' 'Download failed. Check the release, CPU asset and network.' 'Échec du téléchargement. Vérifiez la release et le réseau.' 'Ошибка загрузки. Проверьте release, CPU и сеть.' 'ダウンロードに失敗しました。Release とネットワークを確認してください。' 'Tải thất bại. Kiểm tra bản phát hành, CPU và mạng.'
curl --fail --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 "$base/checksums.txt" -o "$temp_dir/checksums.txt" || exit 1
checksum=$(awk -v file="$asset" '$2 == file {print $1}' "$temp_dir/checksums.txt")
[ "${#checksum}" = 64 ] || exit 1
case "$checksum" in *[!a-fA-F0-9]*) exit 1 ;; esac
printf '%s  %s\n' "$checksum" "$temp_dir/bridge" > "$temp_dir/expected.txt"
sha256sum -c "$temp_dir/expected.txt" || die '文件校验失败，安装已停止。请重新下载，不要跳过校验。' 'Checksum mismatch. Installation stopped. Download again; never skip verification.' 'Somme de contrôle incorrecte. Installation arrêtée.' 'Контрольная сумма неверна. Установка остановлена.' 'チェックサムが一致しません。インストールを停止しました。' 'Sai checksum. Đã dừng cài đặt.'
account=new-api-ops
if ! id "$account" >/dev/null 2>&1; then
  command -v useradd >/dev/null 2>&1 || die '缺少 useradd，请先安装系统账户管理工具。' 'Install useradd before continuing.' 'Installez useradd.' 'Установите useradd.' 'useradd をインストールしてください。' 'Hãy cài useradd.'
  useradd --system --create-home --home-dir /var/lib/new-api-ops-bridge --shell /usr/sbin/nologin "$account"
fi
[ "$(id -u "$account")" != 0 ] && [ "$(id -g "$account")" != 0 ] || exit 1
install -m 0755 "$temp_dir/bridge" /usr/local/bin/new-api-ops-bridge
if [ ! -f "$state/config.json" ]; then
  /usr/local/bin/new-api-ops-bridge init --origin "$origin" --public-host "$host" --user "$account"
fi
# 桥启动时要在降权前续证，只放开自己的证书目录，/etc 其余部分仍只读
cat > /etc/systemd/system/new-api-ops-bridge.service <<'UNIT'
[Unit]
Description=new-api browser-to-server operations bridge
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart=/usr/local/bin/new-api-ops-bridge serve
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=/etc/new-api-ops-bridge
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
LimitCORE=0
LimitNOFILE=512
TasksMax=64
MemoryMax=256M
UMask=0077
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now new-api-ops-bridge.service
systemctl restart new-api-ops-bridge.service
if ! systemctl is-active --quiet new-api-ops-bridge.service; then
  die '桥启动失败。运行 journalctl -u new-api-ops-bridge -n 30 查看原因，日志不含密码。' 'Startup failed. Run journalctl -u new-api-ops-bridge -n 30. Logs contain no passwords.' 'Échec du démarrage. Consultez journalctl -u new-api-ops-bridge -n 30.' 'Ошибка запуска. Выполните journalctl -u new-api-ops-bridge -n 30.' '起動失敗。journalctl -u new-api-ops-bridge -n 30 を確認してください。' 'Khởi động thất bại. Chạy journalctl -u new-api-ops-bridge -n 30.'
fi
/usr/local/bin/new-api-ops-bridge credentials
# 清单里的端口、网址、地址都以实际生效的配置为准，升级时也一样
port=$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' "$state/config.json")
bound_origin=$(sed -n 's/.*"origin":"\([^"]*\)".*/\1/p' "$state/config.json")
bound_host=$(sed -n 's/.*"public_host":"\([^"]*\)".*/\1/p' "$state/config.json")
bound_origin=${bound_origin:-$origin}
bound_host=${bound_host:-$host}
printf '\n'
msg next '安装完成。接下来请完成以下步骤：' 'Installation complete. Next steps:' 'Installation terminée. Étapes suivantes :' 'Установка завершена. Дальнейшие шаги:' 'インストールが完了しました。次の手順を行ってください：' 'Cài đặt hoàn tất. Các bước tiếp theo:'
msg firewall "1. 在服务器防火墙和云服务器安全组中放行 TCP 端口 $port，来源只填你电脑或手机的公网 IP；家庭网络还需在路由器上做端口转发。本脚本不会修改防火墙。" "1. Allow TCP port $port in the server firewall and your cloud security group, with only your computer's or phone's public IP as the source; on a home network also forward the port on your router. This script does not change the firewall." "1. Autorisez le port TCP $port dans le pare-feu du serveur et le groupe de sécurité du cloud, avec uniquement l'IP publique de votre ordinateur ou téléphone comme source ; sur un réseau domestique, redirigez aussi le port sur votre routeur. Ce script ne modifie pas le pare-feu." "1. Откройте TCP-порт $port в брандмауэре сервера и в группе безопасности облака, указав источником только публичный IP вашего компьютера или телефона; в домашней сети также настройте проброс порта на роутере. Скрипт не меняет брандмауэр." "1. サーバーのファイアウォールとクラウドのセキュリティグループで TCP ポート $port を許可し、送信元はご自身の PC またはスマートフォンのグローバル IP のみにしてください。家庭内ネットワークではルーターでポート転送も設定してください。このスクリプトはファイアウォールを変更しません。" "1. Mở cổng TCP $port trên tường lửa máy chủ và nhóm bảo mật của nhà cung cấp đám mây, chỉ cho phép IP công khai của máy tính hoặc điện thoại của bạn; với mạng gia đình, hãy chuyển tiếp cổng trên router. Script này không thay đổi tường lửa."
# 认出本机在用的防火墙就给一条能直接复制的放行命令，只打印不执行
fw_cmd=''
if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status 2>/dev/null | grep -q 'Status: active'; then
  fw_cmd="sudo ufw allow from YOUR_IP to any port $port proto tcp"
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  fw_cmd="sudo firewall-cmd --permanent --add-rich-rule='rule family=\"ipv4\" source address=\"YOUR_IP\" port port=\"$port\" protocol=\"tcp\" accept' && sudo firewall-cmd --reload"
fi
if [ -n "$fw_cmd" ]; then
  msg fwcmd '   可直接复制的放行命令（把 YOUR_IP 换成你的公网 IP）：' '   Ready-to-copy command (replace YOUR_IP with your public IP):' '   Commande prête à copier (remplacez YOUR_IP par votre IP publique) :' '   Готовая команда (замените YOUR_IP на ваш публичный IP):' '   コピー用コマンド（YOUR_IP をご自身のグローバル IP に置き換えてください）：' '   Lệnh có thể sao chép (thay YOUR_IP bằng IP công khai của bạn):'
  printf '   %s\n' "$fw_cmd"
fi
msg trust "2. 用浏览器打开上方的 Bridge URL，核对 TLS 指纹后信任证书。也可以把本机 CA 证书（$state/ca.pem）导入电脑或手机的系统信任，之后证书自动续期无需再操作。" "2. Open the Bridge URL above in your browser, compare the TLS fingerprint and trust the certificate. Alternatively import the local CA certificate ($state/ca.pem) into the system trust store of your computer or phone; automatic renewals then need no further action." "2. Ouvrez la Bridge URL ci-dessus dans le navigateur, vérifiez l'empreinte TLS et faites confiance au certificat. Vous pouvez aussi importer le certificat de l'AC locale ($state/ca.pem) dans le magasin de confiance de votre ordinateur ou téléphone ; les renouvellements automatiques ne demanderont alors plus rien." "2. Откройте Bridge URL выше в браузере, сверьте отпечаток TLS и доверьтесь сертификату. Либо импортируйте сертификат локального CA ($state/ca.pem) в системное хранилище доверия компьютера или телефона; тогда автоматические продления не потребуют действий." "2. 上の Bridge URL をブラウザで開き、TLS 指紋を確認して証明書を信頼してください。ローカル CA 証明書（$state/ca.pem）を PC やスマートフォンのシステム信頼ストアに登録すると、自動更新後も操作は不要です。" "2. Mở Bridge URL ở trên trong trình duyệt, đối chiếu dấu vân tay TLS rồi tin cậy chứng chỉ. Hoặc nhập chứng chỉ CA cục bộ ($state/ca.pem) vào kho tin cậy hệ thống của máy tính hoặc điện thoại; sau đó việc tự gia hạn không cần thao tác thêm."
msg connect "3. 用 $bound_origin 打开训练场（网址必须完全一致），把 Bridge URL 和 Bridge password 填到「服务器运维」设置中并测试连接。密码不要发到聊天中。" "3. Open the playground at exactly $bound_origin, enter the Bridge URL and Bridge password in the server operations settings and test the connection. Never send the password in chat." "3. Ouvrez le playground exactement à l'adresse $bound_origin, saisissez Bridge URL et Bridge password dans les paramètres d'exploitation du serveur et testez la connexion. N'envoyez jamais le mot de passe dans le chat." "3. Откройте playground строго по адресу $bound_origin, введите Bridge URL и Bridge password в настройках управления сервером и проверьте подключение. Никогда не отправляйте пароль в чат." "3. 訓練場を $bound_origin で開き（アドレスは完全一致が必要です）、サーバー運用の設定に Bridge URL と Bridge password を入力して接続テストを行ってください。パスワードはチャットに送信しないでください。" "3. Mở playground đúng tại $bound_origin, nhập Bridge URL và Bridge password vào cài đặt vận hành máy chủ rồi kiểm tra kết nối. Không gửi mật khẩu vào chat."
if is_private "$bound_host"; then
  msg private "提示：$bound_host 是内网地址，Chrome 和 Edge 首次连接时会询问是否允许访问本地网络，请选择允许。" "Note: $bound_host is a private network address. Chrome and Edge ask for local network access on the first connection; choose Allow." "Remarque : $bound_host est une adresse de réseau privé. Chrome et Edge demandent l'accès au réseau local lors de la première connexion ; choisissez Autoriser." "Примечание: $bound_host — адрес частной сети. Chrome и Edge при первом подключении запросят доступ к локальной сети; выберите «Разрешить»." "注意：$bound_host はプライベートネットワークのアドレスです。Chrome と Edge は初回接続時にローカルネットワークへのアクセス許可を求めるので、「許可」を選択してください。" "Lưu ý: $bound_host là địa chỉ mạng riêng. Chrome và Edge sẽ hỏi quyền truy cập mạng cục bộ ở lần kết nối đầu tiên; hãy chọn Cho phép."
fi
