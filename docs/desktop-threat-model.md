# Mail Desktop Threat Model

This document defines the security boundary for the Rust + Tauri Windows client. The desktop application packages the React interface locally and connects only to the controlled Aillive Mail HTTPS API. It must never connect directly to Microsoft OAuth, Graph, IMAP, or SMTP.

## Protected assets

- Desktop access and refresh tokens
- Microsoft mailbox credentials stored by the service
- Encrypted local mailbox cache and pending operations
- Message bodies, attachments, account identifiers, and user settings
- Release packages and update metadata

## Trust boundaries

| Boundary | Rule |
| --- | --- |
| React → Rust IPC | Only registered commands are callable; command input, API path, method, size, timeout, and ownership context are validated in Rust. |
| Rust → Mail API | Production builds allow only the compiled HTTPS origin and `/api/` paths. Access tokens remain in Rust memory. |
| Mail API → Microsoft | OAuth, Graph, IMAP, and SMTP credentials remain server-side and are encrypted before persistence. |
| Cache → Windows user | Each Aillive profile uses a separate encrypted cache and a Windows-protected key. Rebuildable cache data is separated from pending user operations. |
| Email HTML → WebView | Server-side sanitization removes active content and remote resources; the reader iframe is sandboxed without `allow-same-origin`. |
| Attachment → filesystem | Downloads must use server-owned stable IDs, safe filenames, explicit user actions, size limits, and executable-file warnings. |
| Release → client | Public releases require Authenticode signing, SHA-256 checksums, signed updater metadata, and a trusted timestamp. |

## Primary threats and controls

| Threat | Required controls |
| --- | --- |
| Malicious HTML or XSS invoking Tauri IPC | Sanitize script, event attributes, iframe, form, SVG, CSS network loads, tracking pixels, and unsafe URL schemes; keep the iframe sandboxed; expose no shell or unrestricted filesystem plugin. |
| Credential theft | Keep access tokens in Rust memory, refresh tokens in Windows Credential Manager, mailbox credentials on the encrypted service, and redact authentication material from logs. |
| Cross-user data access | Scope every server query and local cache operation to the authenticated user or isolated guest identity; return 403/404 for foreign resource IDs. |
| Malicious attachment | Validate ownership, media type, size, disposition, and filename; stream to a private temporary file; never auto-run; confirm executable and script types. |
| Man-in-the-middle | Require HTTPS in production, use the operating-system trust store, and never provide an option to disable TLS verification. |
| Local cache disclosure | Encrypt sensitive payloads, use per-profile cache files and keys, restrict files to the current Windows user, and remove sensitive temporary data on cleanup. |
| Update hijacking | Authenticode-sign the EXE, installers, uninstaller, and updater packages; separately sign Tauri updater metadata; reject invalid signatures. |
| Denial of service or sync storms | Bound payloads and timeouts, cancel obsolete requests, cap concurrency, honor `Retry-After`, and use exponential backoff with jitter. |

## Residual release risks

- Current public packages are not Authenticode-signed and may trigger Windows SmartScreen.
- Streaming attachment download and signed automatic updates remain release blockers and must stay disabled until completed.
- Diagnostics must remain opt-in and must never include message bodies, attachments, addresses, credentials, or raw tokens.

Security-sensitive changes require Go, Rust, and TypeScript validation plus a Windows installation-package smoke test before release.
