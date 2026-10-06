# Implementation verification

Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.

The initial implementation was verified locally on 2026-10-05. The v1.1.0 section at the end lists the later GitHub Actions verification.

- new-api frontend: 100 tests passed, 0 failures; production build succeeded. Relevant Go controller/settings tests passed. All 127 interface translation keys checked in eight locale files.
- Chromium: actual site-model selection and administrator-provided repository/installer links verified. At 390 px and desktop widths, no page overflow; the credential flow rendered within the tool panel. Real DOMParser tests rejected external entity declarations, failed DAV properties, malformed encoded paths and paths outside the configured root.
- Linux bridge: 15 race-enabled behavioral/integration tests passed using Go 1.25.14. The real HTTPS executable initialized as root, authenticated over a trusted generated certificate, then executed under a non-root account with no root groups. Thirty-two parallel shell children all reported `NoNewPrivs: 1`. Invalid passwords did not consume the authenticated operation budget.
- File regressions: exclusive uploads, custom-state denial, foreign config/directory ownership rejection, FIFO nonblocking rejection, 1,000 concurrent symlink replacement reads, bounded output, clean environment, command timeout and background cancellation passed.
- Cron: numeric ranges/steps, day/week semantics, persistence, deletion, execution tracking and four-task capacity reporting passed. No OS setuid crontab helper is used.
- All 13 Linux targets compiled: amd64, arm64, 386, ARMv6/v7, riscv64, ppc64le, s390x, loong64, mips/mipsle and mips64/mips64le. MIPS builds use soft-float. Low-memory build tests run sequentially to avoid exceeding the local 1 GB Linux VM.
- Installer: 13 CPU mappings, six language selections, reinstall configuration preservation, corrupted checksum rejection and custom-password rejection passed in a disposable root container using mocked Release downloads and systemd. This is not a live GitHub Release installation. The test caught and fixed a release-stamping guard error before handoff.
- `go vet`, shell syntax checks and ShellCheck passed. `govulncheck` v1.8.0 inspected the compiled Linux amd64 binary and returned **No vulnerabilities found**.

The initial source snapshot also underwent an independent Codex Security audit. Its five reported issues were addressed: root state/config ownership, thread-local privilege restriction, pre-authentication rate-budget exhaustion, symlink/open races and blocking FIFO opens. The sealed report describes that initial snapshot; these fixes and regression tests are subsequent implementation work, not a claim that a second formal scan found nothing.

The audit tool reported 9,240,789 total tokens across four participating threads, including 8,758,400 cached input tokens; its measurement uses thread rollouts and is not a separate estimate of only the bridge feature's incremental usage.

Actual Nutstore accounts, paid upstream models, arbitrary customer firewall/TLS configurations, hosted Actions runs and every hardware/kernel combination were not exercised. Cross-compilation and known-vulnerability checks cannot establish zero vulnerabilities. Only publish after reviewing the code, permissions and Release publisher.

## v1.1.0

Verified in GitHub Actions on 2026-10-06 (ubuntu-latest runner; root tests in a disposable `golang:1.25-bookworm` container):

- Race-enabled unit tests for the local CA: IP, IPv6 and domain chains verify; name constraints reject certificates for other hosts (the test was checked to fail when the constraints are removed); renewal window, CA reuse, CA replacement near expiry, legacy self-signed migration, untouched custom certificates and recovery of interrupted renewals.
- Root integration: an expiring bridge certificate is renewed at startup with the same local CA and served over HTTPS; `renew-cert` replaces the server certificate, keeps the CA and prints no password; files stay root-owned with mode 600 and a foreign-owned CA key is refused.
- Installer: unusable, loopback and playground-identical hosts and invalid origins are rejected before downloading; a missing terminal falls back to `--host` guidance instead of waiting; addresses are normalized; the checklist shows the real port, origin and a ufw rule; reinstalling keeps settings and warns about a different playground; private addresses get the local network permission note; 13 CPU mappings, six languages and the `ReadWritePaths` unit line pass.
- `go vet`, ShellCheck and `govulncheck` (no known vulnerabilities).

Not exercised: the hourly renewal restart under a real systemd, real browsers and phones trusting the local CA, and a live installation from the published Release on real hardware.
