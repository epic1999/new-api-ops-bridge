#!/bin/sh
# Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
# Release workflow replaces the repository/version, so forks need no code edits.
set -eu
umask 077
repo='__REPOSITORY__'
release='__RELEASE__'
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
if [ -z "$origin" ]; then
  msg prompt '请输入训练场网址的来源（例如 https://ai.example.com）：' 'Enter your playground origin (e.g. https://ai.example.com):' 'Origine du site (ex. https://ai.example.com) :' 'Источник сайта (например https://ai.example.com):' '訓練場のオリジンを入力してください（例 https://ai.example.com）:' 'Nhập nguồn trang (ví dụ https://ai.example.com):'
  IFS= read -r origin </dev/tty
fi
if [ -z "$host" ]; then
  detected=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 8 --proto '=https' https://api.ipify.org 2>/dev/null || true)
  msg prompt "请输入服务器公网 IP 或域名（回车使用 $detected）：" "Public server IP or domain (Enter for $detected):" "IP publique ou domaine (Entrée pour $detected) :" "Публичный IP или домен (Enter: $detected):" "公開 IP またはドメイン（Enter: $detected）:" "IP công khai hoặc tên miền (Enter: $detected):"
  IFS= read -r host </dev/tty
  host=${host:-$detected}
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
if [ ! -f /etc/new-api-ops-bridge/config.json ]; then
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
msg 'done' '安装完成。将桥 URL 和密码填到训练场工具设置，切勿粘贴到聊天中。' 'Installed. Paste the URL and password into tool settings, never into chat.' 'Installé. Collez URL et mot de passe dans les paramètres, jamais dans le chat.' 'Готово. Вставьте URL и пароль в настройки, не в чат.' '完了。URL とパスワードをツール設定に貼り付けてください。チャットに入力しないでください。' 'Đã cài. Dán URL và mật khẩu vào cài đặt công cụ, không vào chat.'
msg tls '首次连接：在浏览器打开桥 URL，核对 TLS 指纹后信任证书。证书到期前会自动续期；将上方的本机 CA 证书导入系统信任一次，续期后无需重新信任。只向你的 IP 开放输出的端口；脚本不会修改防火墙。' 'First connection: open the bridge URL, compare the TLS fingerprint and trust the certificate. The certificate renews automatically before it expires; import the local CA certificate shown above into your system trust store once so renewals need no new trust. Allow only your IP through the port; this script does not change the firewall.' 'Ouvrez cette URL, vérifiez cette empreinte TLS et faites confiance au certificat. Le certificat se renouvelle automatiquement avant expiration ; importez une seule fois le certificat de votre AC locale indiqué ci-dessus dans le magasin de confiance du système pour éviter de le refaire après chaque renouvellement. Limitez le port à votre IP ; le pare-feu reste inchangé.' 'Откройте URL, сверьте отпечаток TLS и доверьтесь сертификату. Сертификат продлевается автоматически до истечения срока; один раз импортируйте указанный выше сертификат локального CA в системное хранилище доверия, чтобы не подтверждать доверие после продлений. Разрешите порт только для своего IP; скрипт не меняет firewall.' 'URL を開き TLS 指紋を確認して証明書を信頼してください。証明書は期限前に自動更新されます。上に表示されたローカル CA 証明書を一度システムの信頼ストアに登録すると、更新後に再度信頼する必要はありません。ポートは自分の IP のみに許可してください。ファイアウォールは変更しません。' 'Mở URL, đối chiếu dấu vân tay TLS rồi tin cậy chứng chỉ. Chứng chỉ tự gia hạn trước khi hết hạn; nhập chứng chỉ CA cục bộ hiển thị ở trên vào kho tin cậy của hệ thống một lần để không phải tin cậy lại sau mỗi lần gia hạn. Chỉ mở cổng cho IP của bạn; script không đổi tường lửa.'
