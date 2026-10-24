# edgartools

[EdgarTools](https://github.com/dgunning/edgartools) MCP server: SEC EDGAR filings (8-K, 10-Q, 10-K,
XBRL financials, insider trades) as MCP tools for claude.ai and Claude Code. No code of our own: the
image installs the pinned `edgartools[ai]` from `requirements.txt` (Renovate bumps it) and runs
`edgartools-mcp --transport streamable-http` on port 8000 (stateless, JSON responses; `/mcp` + `/health`).

## Request path

```
claude.ai → Cloudflare Access (Managed OAuth) → tunnel → nginx (edgar.mjasion.dev, /mcp only) → edgartools:8000
```

The server has **no authentication** — Cloudflare Access is the only gate. nginx (`nginx/web_edgartools.conf`)
exposes only `/mcp` and rejects requests without the `Cf-Access-Jwt-Assertion` header (a sanity check, not
a JWT validation). The container publishes no ports; nginx reaches it by service name on the compose network.

## Configuration

- `EDGAR_IDENTITY` (`ENV` in the Dockerfile, required): `"Name email"` — SEC rejects requests without a User-Agent
  with contact details. SEC allows ~10 requests/s per IP; exceeding it blocks the IP for ~10 minutes.
- `mem_limit: 512m`: ~80 MB idle, ~200 MB after parsing filings.

## Cloudflare setup (dashboard, not in this repo)

1. Tunnel → Public hostname `edgar.mjasion.dev` → service `http://nginx:80` (same as the other hostnames).
2. Zero Trust → Access → Applications → Self-hosted `edgar.mjasion.dev` with an Allow policy for the owner's e-mail.
3. That application → Advanced settings → **Managed OAuth** on, with dynamic client registration and the
   claude.ai callback in *Allowed redirect URIs*. Optionally the tunnel hostname's *Protect with Access*
   (JWT validation by cloudflared).
4. claude.ai → Settings → Connectors → Add custom connector → `https://edgar.mjasion.dev/mcp`.
