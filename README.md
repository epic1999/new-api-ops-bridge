# new-api Ops Bridge

Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.

An open-source Linux server bridge for the new-api playground. The model proposes a tool name and arguments; the **browser** adds locally stored credentials and talks directly to this bridge over HTTPS. No SSH credentials are sent to new-api. Shell output, selected file content and other tool results are returned to the selected model through the normal new-api model gateway. This is a real server connection, not an offline-only system.

## Publish your own repository

This directory is an independent Go module and Git repository. No remote is configured and nothing has been pushed. Create a public GitHub repository under your chosen account, then manually:

```sh
git remote add origin https://github.com/YOUR_ACCOUNT/new-api-ops-bridge.git
git push -u origin main
git tag v0.1.0
git push origin v0.1.0
```

The Actions workflow runs race tests, vet, ShellCheck and govulncheck, builds 13 Linux targets (amd64, arm64, 386, ARMv6/v7, riscv64, ppc64le, s390x, loong64, MIPS/MIPS64 little/big endian), and publishes public **GitHub Release assets** and SHA-256 checksums. The published `install.sh` is stamped with the actual `github.repository` and release tag; forks need no hard-coded author URL. The playground accepts `owner/repository` and generates the matching installer command. Release assets can be downloaded without a GitHub access token; Actions artifacts would require authentication and expire, so they are not the installer source.

The standalone source requires Go 1.25+ and has no third-party runtime dependencies. Use an updated Go toolchain. Architectures must also have a Linux kernel supported by that Go version; architecture builds are not real-hardware installation tests.

A local initial commit is prepared. Configure the public repository and installer URLs in new-api admin General settings after publishing. See [VERIFICATION.md](VERIFICATION.md) for the completed local checks and their limits.

## Install

Use the command generated in the playground after publishing your first Release. The installer defaults to Simplified Chinese; `--lang en|fr|ru|ja|vi` selects other languages. It recognizes CPU architecture, checks the checksum, creates an unprivileged `new-api-ops` system account and installs a systemd service. It asks only for your public IP/domain if not auto-detected; the playground origin is already embedded in the command. Public-IP detection contacts `api.ipify.org` from your server and sends no bridge secret. The installer does not change firewall rules. Restrict the random port to your own client IP/VPN.

The daemon generates a 256-bit password, an available random port between 20000 and 59999, and a self-signed ECDSA TLS certificate. There are **no password/port input flags**. Configuration and TLS private key remain root-owned with directory mode 0700 and file mode 0600. `serve` reads them and then drops all supplementary groups, UID/GID and privilege escalation capability before accepting any requests. Shell children receive a small clean environment with no bridge secrets. Linux core dumps and ptrace against the daemon are disabled; systemd adds resource limits and protects system configuration.

Copy the generated **Bridge URL** and **Bridge password** into tool settings, never into chat. Open that HTTPS URL in your browser and verify the certificate fingerprint shown on your server before trusting the self-signed certificate. Browser trust varies: if it still rejects API fetches, import this exact certificate into your OS/browser trust store or replace `/etc/new-api-ops-bridge/cert.pem` and `key.pem` with a CA-trusted certificate for the same hostname, keeping owner-only permissions, then restart **the bridge service**. Browsers with stricter certificate policies need a CA-trusted certificate. Do not disable TLS verification or use HTTP as a fallback. Renew before the generated certificate expires in one year. For an already initialized installation, re-running the installer preserves its existing origin, hostname, port and password.

```sh
sudo new-api-ops-bridge credentials
sudo new-api-ops-bridge rotate
sudo systemctl restart new-api-ops-bridge   # apply password/certificate changes
sudo journalctl -u new-api-ops-bridge -n 30
```

This is not a server reboot. There are no shutdown/reboot tools. The service account cannot administer the whole server by default. Give it narrowly scoped directory permissions as needed; do not grant unrestricted sudo. systemd `ProtectSystem=full` additionally makes system configuration read-only. Cron uses a bridge-managed persistent scheduler, so it works with no-new-privileges and needs no setuid cron helper. It does not edit the OS crontab. Jobs run only while the bridge is active, in server local time, up to 100 entries; stopped periods are not replayed. Each run is a tracked background task, limited to one hour and four concurrent tasks. Job listing includes the last task ID or a capacity error.

Without systemd, build the binary, create a dedicated account, initialize as root and run `sudo ./new-api-ops-bridge serve` under your preferred process supervisor. The process still drops privileges internally. The automated installer intentionally reports this prerequisite instead of making distribution-specific changes.

## Capabilities and limits

- Server information, CPU/memory/disk/uptime; shell commands with bounded output and 120-second timeout.
- Directory listing; bounded file reads; file download/upload (16 MB; uploads never overwrite).
- Bridge-managed persistent cron listing/addition/deletion. Five numeric fields support lists, ranges and steps; existing jobs and the OS crontab are preserved.
- Up to four background tasks, up to one hour each, status/output/cancellation by task ID. Tasks continue after the page closes, but task records are held in memory and disappear on bridge restart. Spawned process groups are killed at timeout/cancellation/completion.
- DNS/TCP outbound reachability probes to public targets and an 8 MB download throughput test via Cloudflare. A successful outbound probe **does not prove** that inbound access from a region is unblocked. Browser connection testing verifies reachability only from the user's present browser network.

Normal mode asks approval before commands and writes. Playground YOLO skips approval. The bridge enforces authentication, exact Origin/Host, input/output limits and permissions regardless of UI mode. Commands run with the dedicated account's filesystem and network permissions, not in an operating-system sandbox. Commands can cause irreversible changes to resources that account can access. A small reboot/destructive-command filter provides protection against obvious accidents, but arbitrary scripts can bypass text filters; never claim every command is safe or that this software has zero vulnerabilities. The non-root account, no-new-privileges setting and absence of a reboot endpoint are the enforceable protections.

## Protocol

`POST /v1/operate`, `Origin: <configured origin>`, `Authorization: Bearer <generated password>`, `Content-Type: application/json`. Body: `{ "action": "info", "args": {} }`. No cookies, credential query parameters, wildcard origins, telemetry or gateway callback endpoints. Unknown fields/actions and oversized requests are rejected. TLS is required by the executable. The front end rejects credential-bearing redirects, so secrets cannot be carried to a redirected destination.

Operations: `info`, `execute`, `list`, `read`, `download`, `upload`, `cron_list`, `cron_add`, `cron_delete`, `task_start`, `task_status`, `task_cancel`, `network`, `speedtest`. See `args` in `main.go` and `operations.go`. The password never appears in API responses or service logs. Random ports reduce common scanning noise; they are not authentication.

## Verification

```sh
go test -race ./...
# In a disposable root Linux container (creates only the test account):
sh scripts/check-linux.sh
sh scripts/check-builds.sh
go vet ./...
sh -n install.sh
shellcheck -S warning install.sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Only configure bridges you own and trust. Review the open-source code and Release publisher before running its installer. Release checksums verify downloaded bytes against that publisher's manifest; they do not independently establish publisher trust.
