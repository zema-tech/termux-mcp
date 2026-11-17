# Remote access

Three modes:

1. **Local stdio** — `termux-mcp serve stdio` (default). Used by on-device agents.
2. **LAN HTTP** — `termux-mcp serve http` with `server.bind: 0.0.0.0` and a token.
3. **Remote HTTPS** — `termux-mcp tunnel start` (cloudflared quick tunnel).

## 1. LAN

```bash
termux-mcp token new --write
termux-mcp serve http --config $PREFIX/var/lib/termux-mcp/config.yaml
# => http://192.168.x.x:3000/mcp
```

Client config (`claude_desktop_config.json` / MCP settings):

```json
{
  "mcpServers": {
    "termux": {
      "url": "http://192.168.1.20:3000/mcp",
      "headers": { "Authorization": "Bearer YOUR_TOKEN" }
    }
  }
}
```

## 2. Remote via cloudflared

```bash
# Enforce auth (static token and/or OAuth):
# auth.require: true
# auth.token: <from termux-mcp token new --write>
# auth.oauth: true   # default
termux-mcp serve http &
termux-mcp tunnel start                    # prints https://xxx.trycloudflare.com
```

`termux-mcp tunnel start` refuses to run unless either:
- `auth.token` is set **and** `auth.require: true`, or
- `auth.oauth: true` (clients complete OAuth on the public URL).

Tunnel traffic arrives from the local cloudflared proxy (loopback).

### Static Bearer clients

```json
{
  "mcpServers": {
    "termux": {
      "url": "https://xxx.trycloudflare.com/mcp",
      "headers": { "Authorization": "Bearer YOUR_TOKEN" }
    }
  }
}
```

### OAuth 2.1 clients (Grok custom connector, Claude, Composio)

Point the connector at:

```
https://xxx.trycloudflare.com/mcp
```

The client will:

1. Discover `/.well-known/oauth-protected-resource`
2. Read AS metadata from `/.well-known/oauth-authorization-server`
3. Register via `POST /oauth/register`
4. Open `/oauth/authorize` → **Approve** on the tablet consent page
5. Exchange the code at `/oauth/token` (PKCE S256)
6. Call `/mcp` with `Authorization: Bearer <access_token>`

Optional: set `auth.public_base: "https://xxx.trycloudflare.com"` so metadata
URLs stay stable even if Host headers vary.

## 3. Hosts that only support stdio

Use the `mcp-remote` bridge on the desktop machine:

```bash
npx -y mcp-remote --transport http-first https://xxx.trycloudflare.com/mcp
# then point your stdio host at `npx mcp-remote ...`
```

## Keeping it alive

- `termux-services`: `sv up termux-mcp` (installed by `scripts/install.sh`)
- Termux:Boot: `~/.termux/boot/start-mcp.sh`
- `termux-wake-lock` is taken on start; exempt Termux from battery optimization.
