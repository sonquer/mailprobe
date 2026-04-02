# AGENTS.md

Instructions for AI coding agents working on this codebase.

## Project Overview

mailprobe is a lightweight email verification API that checks whether an email address exists by performing SMTP RCPT TO probing. It connects to the recipient's MX server, performs an SMTP handshake up to RCPT TO, and returns the result. No email is ever sent.

## Tech Stack

- Go (latest stable, currently 1.26.1)
- Zero external dependencies - standard library only
- `net` for raw TCP SMTP connections
- `net/http` for the HTTP API
- `log/slog` for structured logging
- `encoding/json` for request/response serialization

## Project Structure

Go source code lives under `src/` with idiomatic `cmd/` and `internal/` directories:

```
src/
  cmd/mailprobe/main.go        Entry point: .env loading, config, routing, server start, graceful shutdown
  internal/config/config.go    Config struct + Load() from environment variables
  internal/config/dotenv.go    Stdlib-only .env file parser
  internal/smtp/models.go      Result constants + VerifyResult, BatchVerifyResponse structs
  internal/smtp/prober.go      SMTP RCPT TO probing (MX resolution, TCP connect, handshake)
  internal/api/handler.go      HTTP handlers for /verify, /verify/batch, /health, /version + middleware
  internal/version/version.go  Build version info (ldflags + BuildInfo fallback)
  go.mod                       Go module definition
  .env.example                 Example environment variable configuration
doc/img/mailprobe.png          Project logo
Dockerfile                     Multi-stage build, Alpine-based, <10MB
docker-compose.yml             Docker Compose for local development
.goreleaser.yml                GoReleaser config for cross-platform releases
```

Test files follow Go convention (`*_test.go` alongside source files).

## How to Build

```
cd src
go build -o mailprobe ./cmd/mailprobe
```

## How to Run

```
cd src
PORT=8080 HELO_DOMAIN=probe.example.com ./mailprobe
```

Or create a `.env` file (see `src/.env.example`) and run without environment variables:

```
cd src
./mailprobe
```

## How to Test

```
cd src
go test -v -race ./...
```

All tests use mock SMTP servers on localhost - no external network access needed.

## Coding Conventions

1. **No comments in code.** The code should be self-explanatory through clear naming.
2. **No external dependencies.** Everything uses Go's standard library.
3. **English only.** All code, variable names, error messages, and documentation must be in English.
4. **No emojis** in code or documentation.
5. **Idiomatic Go layout.** `src/cmd/` for entry points, `src/internal/` for private packages.

## Key Design Decisions

### Raw TCP instead of net/smtp

We use `net.Conn` directly instead of the `net/smtp` package. This gives us full control over timeouts (per-command read/write deadlines), EHLO/HELO fallback, and connection reuse for batch operations.

### Catch-all Detection

Before probing real addresses, we send RCPT TO to a random nonexistent address (`zxqj_{random}@domain`). If the server accepts it with 250, the domain is a catch-all and all results are flagged accordingly.

### Dependency Injection for Testing

The `Prober` struct accepts `MXResolver` and `Dialer` interfaces. Tests inject mocks to redirect MX lookups and TCP connections to local mock SMTP servers without any real network access.

### Connection Reuse

Batch requests to the same domain reuse a single SMTP connection. RSET is sent between RCPT TO probes. If the connection drops mid-batch, the prober automatically reconnects and resumes.

### .env File Support

The stdlib-only `.env` parser (`src/internal/config/dotenv.go`) loads environment variables from a `.env` file at startup. Real environment variables always take precedence (existing env vars are never overridden).

### API Key Authentication

Optional API key protection via the `API_KEYS` environment variable (comma-separated). When set, all requests to `/verify` and `/verify/batch` must include a valid `X-API-Key` header. The `/health` and `/version` endpoints are always open. When `API_KEYS` is not set, the API is completely open.

### Versioning

Version info is injected at build time via ldflags (`-X internal/version.Version=...`). For local dev builds, `runtime/debug.ReadBuildInfo()` provides commit and date as fallback. GoReleaser handles this automatically for releases.

## What NOT to Add

- No rate limiting (handle externally)
- No persistent storage or database
- No email sending capability
- No third-party dependencies
- No comments in the code

## CI/CD

- **CI** (`.github/workflows/ci.yml`): runs vet, tests with race detector, and build on every push/PR
- **Release** (`.github/workflows/release.yml`): GoReleaser creates GitHub Release with cross-platform binaries + changelog; separate job builds multi-platform Docker image and pushes to GHCR on version tags (`v*`)

## Docker Image

Published to `ghcr.io/sonquer/mailprobe`. Multi-arch: `linux/amd64` and `linux/arm64`.

```
docker pull ghcr.io/sonquer/mailprobe:latest
docker run -p 8080:8080 -e API_KEYS=my-secret-key ghcr.io/sonquer/mailprobe:latest
```
