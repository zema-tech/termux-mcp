# Security model

## Principles

- **Default-deny.** Sensitive and dangerous tool tiers are off until explicitly
  enabled. The shell tool is ON by default with a wildcard allowlist; use
  `exec.shell_deny_patterns` to block specific commands if needed.
- **Never expose without credentials.** Binding a non-loopback address without
  `auth.token` or `auth.oauth` is a startup error. Tunnels require
  `auth.require: true` with a token, or OAuth enabled (tunnel traffic arrives
  from loopback, which is exempt by default for static tokens).
- **Least privilege per path.** File tools run inside a resolved-path sandbox;
  every command runs with a timeout and an output cap.

## Mechanisms

| Concern | Control |
|---|---|
| Transport auth | Static Bearer + OAuth 2.1 access tokens (`internal/auth`, `internal/oauth`) |
| OAuth | PKCE S256, dynamic client registration, consent page, opaque tokens |
| Tool gating | Permission tiers + per-tool allow/deny (`internal/registry`) |
| Shell | Regex allow/deny; must be explicitly enabled (`exec.shell_allowed`) |
| Files | Path sandbox under `tools.file_roots`, symlink resolution |
| Command safety | Timeouts (default 30s), output caps (1 MiB), retry-once JSON validation |
| Observability | JSONL audit log with argument redaction (`internal/audit`) |
| Secrets | Never logged; config `chmod 600`; `termux-mcp get_config` is sanitized |
| DoS | Max body size, max sessions, rate-limit-ready middleware |

## OAuth notes

- Access tokens are **opaque** and held in process memory (lost on restart).
- Authorization codes are single-use and expire in 10 minutes.
- Access tokens expire in 24 hours.
- Consent must be approved on the device (or any browser that can reach the
  public tunnel URL).

## Audit redaction

Arguments named like `text`, `content`, `message`, `token`, `password`,
`secret`, `key`, `pin`, `otp`, `authorization`, `body` are replaced with
`[REDACTED]` in the audit trail; values over 200 chars are truncated.

## Remote exposure checklist

1. `termux-mcp token new --write` and keep the config file private **or**
   rely on OAuth (`auth.oauth: true`).
2. Keep `tools.enable_sensitive` and `tools.enable_dangerous` **off** unless
   needed; prefer narrow `tools.allow` lists.
3. Restart the server after any config change.
4. Check `audit.jsonl` periodically.
5. Prefer `auth.require: true` on public tunnels even when OAuth is enabled.

## Reporting

Any exposed device on a public tunnel without credentials is a misconfiguration —
the server refuses to start in that state. If you find a bug, open an issue in
the repository.
