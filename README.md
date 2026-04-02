# mailprobe

<p align="center">
  <img src="doc/img/mailprobe.png" alt="mailprobe" height="256px" />
</p>

<p align="center">
  <a href="https://github.com/sonquer/mailprobe/actions/workflows/ci.yml"><img src="https://github.com/sonquer/mailprobe/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/sonquer/mailprobe/releases/latest"><img src="https://img.shields.io/github/v/release/sonquer/mailprobe" alt="Release" /></a>
  <a href="https://goreportcard.com/report/github.com/sonquer/mailprobe"><img src="https://goreportcard.com/badge/github.com/sonquer/mailprobe" alt="Go Report Card" /></a>
  <a href="https://github.com/sonquer/mailprobe/blob/main/LICENSE"><img src="https://img.shields.io/github/license/sonquer/mailprobe" alt="License" /></a>
  <a href="https://ghcr.io/sonquer/mailprobe"><img src="https://img.shields.io/badge/container-GHCR-blue?logo=docker" alt="Docker" /></a>
</p>

Lightweight email verification API via SMTP RCPT TO probing. Written in Go, zero dependencies beyond stdlib.

## What it Does

HTTP API that verifies whether an email address exists by connecting directly to the recipient's MX server and performing an SMTP RCPT TO check. No email is ever sent - it only probes the SMTP envelope.

### Flow

1. Caller sends `POST /verify` with an email address
2. mailprobe resolves MX records for the domain via DNS
3. Connects to the highest-priority MX server on port 25
4. Performs SMTP handshake: `EHLO` -> `MAIL FROM` -> `RCPT TO` -> `RSET` -> `QUIT`
5. Returns the result based on SMTP response code (`250` = exists, `550` = doesn't exist)

## Quick Start

### Docker

```bash
docker pull ghcr.io/sonquer/mailprobe:latest
docker run -p 8080:8080 ghcr.io/sonquer/mailprobe:latest
```

### Build from Source

```bash
cd src
go build -o mailprobe ./cmd/mailprobe
./mailprobe
```

## API

### Authentication

If `API_KEYS` is set, all requests to `/verify` and `/verify/batch` must include a valid `X-API-Key` header. The `/health` and `/version` endpoints are always open.

```bash
curl -X POST http://localhost:8080/verify \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-secret-key" \
  -d '{"email": "user@example.com"}'
```

If `API_KEYS` is not set, the API is open with no authentication required.

### Verify Single Email

```
POST /verify
Content-Type: application/json
```

Request:

```json
{
  "email": "jan.kowalski@firma.pl"
}
```

Response:

```json
{
  "email": "jan.kowalski@firma.pl",
  "result": "deliverable",
  "mx": "mx.firma.pl",
  "smtp_code": 250,
  "catch_all": false,
  "duration_ms": 342
}
```

### Verify Batch

```
POST /verify/batch
Content-Type: application/json
```

Request:

```json
{
  "emails": [
    "jan.kowalski@firma.pl",
    "j.kowalski@firma.pl",
    "kowalski.jan@firma.pl"
  ]
}
```

Response:

```json
{
  "results": [
    {"email": "jan.kowalski@firma.pl", "result": "deliverable", "mx": "mx.firma.pl", "smtp_code": 250, "catch_all": false, "duration_ms": 342},
    {"email": "j.kowalski@firma.pl", "result": "undeliverable", "mx": "mx.firma.pl", "smtp_code": 550, "catch_all": false, "duration_ms": 85},
    {"email": "kowalski.jan@firma.pl", "result": "undeliverable", "mx": "mx.firma.pl", "smtp_code": 550, "catch_all": false, "duration_ms": 78}
  ],
  "domain": "firma.pl",
  "mx": "mx.firma.pl",
  "catch_all": false,
  "total_duration_ms": 505
}
```

Batch optimization: emails sharing the same domain resolve MX once and reuse a single SMTP connection. Maximum 50 emails per batch. Automatic reconnect if the server drops the connection mid-batch.

### Health Check

```
GET /health
```

Response:

```json
{"status": "ok"}
```

### Version

```
GET /version
```

Response:

```json
{"version": "1.0.0", "commit": "abc1234", "date": "2026-04-02T12:00:00Z"}
```

## Result Values

| Result | Meaning |
|--------|---------|
| `deliverable` | RCPT TO returned 250, address exists |
| `undeliverable` | RCPT TO returned 550/551/552/553, address doesn't exist |
| `catch_all` | Server accepts any address (verification unreliable) |
| `unknown` | Couldn't determine (connection failed, timeout, greylisting, etc.) |
| `no_mx` | Domain has no MX records |

## Configuration

All via environment variables. You can also use a `.env` file (see `.env.example`). Real environment variables always take precedence over `.env` file values.

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `SMTP_TIMEOUT` | `10s` | Per-connection SMTP timeout (Go duration format: `5s`, `30s`, `1m`) |
| `HELO_DOMAIN` | `localhost` | Domain announced in EHLO/HELO command |
| `MAIL_FROM` | `probe@localhost` | Sender address for MAIL FROM |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `API_KEYS` | _(empty)_ | Comma-separated list of valid API keys. If not set, API is open |

Example:

```bash
cd src
PORT=3000 \
SMTP_TIMEOUT=30s \
HELO_DOMAIN=probe.example.com \
MAIL_FROM=verify@example.com \
LOG_LEVEL=debug \
API_KEYS=key-abc-123,key-def-456 \
./mailprobe
```

Or with a `.env` file:

```bash
cd src
cp .env.example .env
# edit .env with your values
./mailprobe
```

## Project Structure

```
src/
  cmd/mailprobe/main.go        Entry point, .env loading, config, server, graceful shutdown
  internal/config/config.go    Config struct + Load() from environment variables
  internal/config/dotenv.go    Stdlib-only .env file parser
  internal/smtp/models.go      Result constants + VerifyResult, BatchVerifyResponse structs
  internal/smtp/prober.go      SMTP RCPT TO probing (MX resolution, TCP connect, handshake)
  internal/api/handler.go      HTTP handlers, validation, middleware (auth, logging, recovery)
  internal/version/version.go  Build version info (ldflags + BuildInfo fallback)
  go.mod                       Go module definition
  .env.example                 Example environment variable configuration
doc/img/mailprobe.png          Project logo
Dockerfile                     Multi-stage build, Alpine-based, <10MB
docker-compose.yml             Docker Compose for local development
.goreleaser.yml                GoReleaser config for cross-platform releases
```

## Docker

### Pull from GHCR

```bash
docker pull ghcr.io/sonquer/mailprobe:latest
```

### Run

```bash
docker run -p 8080:8080 \
  -e HELO_DOMAIN=probe.example.com \
  -e MAIL_FROM=verify@example.com \
  -e API_KEYS=my-secret-key \
  ghcr.io/sonquer/mailprobe:latest
```

### Docker Compose

```bash
docker compose up -d
```

Configuration is set via environment variables in `docker-compose.yml`. To override locally, create a `docker-compose.override.yml` (gitignored) or use an `.env` file.

### Build Locally

```bash
docker build -t mailprobe .
docker run -p 8080:8080 mailprobe
```

The image is built with a multi-stage Dockerfile. Final image is Alpine-based and under 10MB. Available for `linux/amd64` and `linux/arm64`.

## How it Works

### SMTP Probe Sequence

```
Client (mailprobe)              MX Server
    |                               |
    |--- TCP connect :25 ---------> |
    |<-- 220 greeting ------------- |
    |--- EHLO probe.example.com --> |
    |<-- 250 OK ------------------- |
    |--- MAIL FROM:<probe@...> ---> |
    |<-- 250 OK ------------------- |
    |--- RCPT TO:<user@domain> ---> |
    |<-- 250 OK / 550 Not found --- |
    |--- RSET --------------------> |
    |<-- 250 OK ------------------- |
    |--- QUIT --------------------> |
    |<-- 221 Bye ------------------ |
```

### Catch-All Detection

Before probing real addresses, mailprobe sends `RCPT TO` for a randomly generated nonexistent address (`zxqj_{random}@domain`). If the server accepts it with 250, the domain is flagged as catch-all and all results for that domain return `catch_all`.

### Batch Connection Reuse

For emails sharing the same domain, mailprobe:
1. Resolves MX once
2. Opens one SMTP connection
3. Runs catch-all detection
4. Sends `RCPT TO` for each email, with `RSET` between probes
5. Reconnects automatically on connection drops
6. Sends `QUIT` when done

## Testing

```bash
cd src
go test -v -race -cover ./...
```

All tests use mock SMTP servers on localhost. No external network access required. The test suite covers:

- Configuration parsing and defaults
- .env file loading with precedence rules
- API key authentication middleware
- HTTP request validation (method, content-type, body format)
- SMTP probing (deliverable, undeliverable, catch-all, timeout, greylisting)
- Batch operations with connection reuse
- Result ordering preservation
- Middleware (auth, logging, panic recovery)
- Full integration tests (HTTP -> SMTP -> response)

## CI/CD

- **CI**: Runs `go vet`, tests with race detector, and build on every push and PR to main
- **Release**: On version tag push (`v*`), GoReleaser creates a GitHub Release with cross-platform binaries and changelog, and a separate job builds and pushes the multi-platform Docker image to GHCR

### Creating a Release

```bash
git tag v1.0.0
git push origin v1.0.0
```

This triggers the release workflow which:
- Creates a GitHub Release with downloadable binaries (linux/darwin, amd64/arm64), checksums, and auto-generated changelog
- Builds and publishes Docker images: `ghcr.io/sonquer/mailprobe:1.0.0`, `:1.0`, `:1`, and `:latest`

## Non-Goals

- No rate limiting - handle externally (nginx, cloud provider, etc.)
- No persistent storage
- No email sending capability

## License

MIT
