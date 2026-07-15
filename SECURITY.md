# Security Policy

Mail processes mailbox credentials and private email content. Deploy it as a security-sensitive application.

## Reporting a vulnerability

Do not open a public issue containing credentials, tokens, private messages, exploit details, or personal data. Contact the repository maintainer privately and include only the minimum reproduction information required.

## Deployment requirements

- Use HTTPS through a trusted reverse proxy.
- Complete the one-time administrator setup immediately after first deployment.
- Set `MAIL_SESSION_SECRET`, `MAIL_ENCRYPTION_KEY`, and SMTP credentials through a secret manager.
- Keep `MAIL_ENCRYPTION_KEY` outside the SQLite data volume.
- Set `MAIL_TRUST_PROXY=1` only when direct access to the application port is blocked.
- Back up the database and encryption key separately and protect both at rest.
- Restrict filesystem access to the application user.
- Keep Node.js and npm dependencies current and review `npm audit` results.

## Credential exposure

If a mailbox password or refresh token is exposed:

1. Change the mailbox password.
2. Revoke the affected application/session in the Microsoft account security portal.
3. Complete authorization again to obtain a new refresh token.
4. Remove the exposed value from logs, screenshots, issues, commits, and backups where possible.

## Current protections

- Owner-scoped multi-user account access.
- Signed HttpOnly cookies backed by revocable server-side sessions.
- Receive-only isolated guest sessions.
- AES-256-GCM encrypted credential fields.
- Sanitized email HTML, iframe sandboxing, and remote-image blocking.
- No-store API responses and restrictive browser security headers.
- Production startup checks for strong secrets.
- One-time administrator bootstrap and email-verified registration with five-minute codes.
