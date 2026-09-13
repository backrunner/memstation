# Security policy

Security fixes currently target the `main` branch as MemStation prepares its first
stable release. There is no separate long-term support branch.

## Reporting a vulnerability

Use GitHub's **Security → Report a vulnerability** on
[backrunner/memstation](https://github.com/backrunner/memstation/security) when private
reporting is enabled. If that option is unavailable, contact the maintainer through a
private contact method listed on [their profile](https://github.com/backrunner).
Do not post vulnerability details, credentials or private conversations in public issues.

Include the affected commit/version, deployment type, reproduction steps, expected
and observed behavior, and potential impact. Use synthetic data and redact secrets.

## Deployment configuration

The Compose development defaults are for local use. For an exposed deployment,
configure unique admin credentials and encryption keys, TLS, secure session cookies
and an explicit CORS allowlist. Keep PostgreSQL and Redis on a private network.
Follow the [deployment and backup guides](deploy/compose/DEVELOPMENT.md) and preserve
the encryption key when planning backup and restore.
