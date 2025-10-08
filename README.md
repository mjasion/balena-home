# balena-home

Multi-service home automation and security setup running on Balena.

## Services

- **WolWeb**: Web interface for Wake-on-LAN magic packets
- **nginx**: Reverse proxy with Cloudflare tunnel integration

## Quick Start

```bash
# Start all services
docker-compose up -d

# Build WolWeb locally
cd wolweb && go build -o wolweb .
```

## Configuration

- WolWeb: Configure via `wolweb/config.json` or environment variables (`WOLWEB*`)
- Cloudflare tunnel token: Set `TUNNEL_TOKEN` in `.env` file

See [CLAUDE.md](CLAUDE.md) for detailed documentation.
