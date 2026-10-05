# Security policy

Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.

No software can promise zero vulnerabilities. Before exposing this bridge, review the account permissions, exact browser origin, trusted HTTPS certificate and firewall. Keep the toolchain, OS and released executable updated. Use a dedicated low-privilege account and limit the port to your own IP/VPN. The browser's local storage is accessible to trusted scripts on its origin; it is not protection against a compromised playground/XSS, a malicious extension, or access to your browser profile. Do not grant the bridge account broad sudo privileges.

Bridge credentials are randomly generated, root-readable only, kept out of child command environments, redacted from responses, excluded from browser conversation exports and used only in browser-to-bridge Authorization headers. Authentication uses a constant-time hash comparison; CORS and Host checks are exact; redirects are refused by browser tools. Input size, command output, concurrent operations, background tasks and run duration are bounded. File tools reject system/credential paths and non-regular files. Arbitrary shell tools still have the execution account's permissions and can access other resources or the network; file-tool filters are not a shell sandbox.

The Release workflow runs tests and vulnerability checks. A successful scan is evidence about the tested version, not a guarantee against unknown vulnerabilities or every deployment configuration. Architecture cross-compilation is not hardware/kernel validation. No unauthenticated operation may execute code or inspect files. No shutdown/reboot operation exists.

After publishing this repository under your own account, enable GitHub private vulnerability reporting in repository settings. Report findings privately through that feature with the affected version, reproduction and impact. Do not include live bridge passwords or user data in reports. Rotate exposed passwords and restart the bridge immediately. The initial local repository has no remote or public security-report endpoint until its owner publishes it.
