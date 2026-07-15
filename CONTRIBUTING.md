# Contributing to Mail

Thank you for improving Mail.

## Development setup

```bash
npm install
npm run dev
```

Before submitting a pull request, run:

```bash
npm run typecheck
npm test
npm run build
npm audit
```

## Pull requests

- Keep changes focused and explain the user-facing outcome.
- Add or update tests for authentication, tenant isolation, imports, and data migrations.
- Never commit `.env`, SQLite databases, encryption keys, mailbox passwords, OAuth tokens, or real email content.
- Preserve Chinese and English translations for every new visible interface string.
- Treat all email HTML and remote content as untrusted.

## Security-sensitive changes

Changes involving sessions, owner scoping, encryption, OAuth, sending, or guest migration require explicit regression coverage. See [SECURITY.md](SECURITY.md).
