# Flinternet

An open-source, self-hostable, unauthenticated Pinterest client with a zero-dependency Go backend, server-side rendered HTML UI, and privacy-redirector URL parity.

## Features

- **Unauthenticated Pinterest API**: Search pins, video pins, boards, and creators; browse public profiles, saved/created pins, boards, comments, comment replies, and ideas topics with anonymous `csrftoken`-only sessions.
- **Privacy Redirector Compatibility**: Drop-in replacement for `pinterest.com`, `i.pinimg.com`, and `v1.pinimg.com` paths (`/search/...`, `/pin/...`, `/ideas/...`, `/username/`, `/username/board/`, `/originals/...`, `/videos/iht/...`, plus Binternet `/api.php`, `/search.php`, `/image_proxy.php`).
- **SSRF-Safe Media & Video Proxy**: Streams images, progressive MP4s, and rewrites CMAF HLS playlists (`.m3u8`, `.cmfv`, `.cmfa`) over pooled HTTP/2 connections.
- **Zero External Go Dependencies**: Uses strictly the Go 1.26 standard library with embedded static assets and templates.

## Quick Start

### Binary

```bash
go test -race ./...
go build -o bin/flinternet ./cmd/flinternet
./bin/flinternet
```

### Docker Compose (Prebuilt GHCR Image)

Uses `docker-compose.yml` pointing to `ghcr.io/cat-ling/flinternet-go:latest`:

```bash
docker compose up -d
```

### Docker Compose (Local Build & Deploy)

Uses `docker-compose.build.yml` to build from the local `Dockerfile` and start the container:

```bash
docker compose -f docker-compose.build.yml up -d --build
```

## Configuration (CLI Flags & Environment Variables)

Every CLI flag can be configured via environment variables (see `.env.example`):

| CLI Flag | Environment Variable | Default | Description |
| --- | --- | --- | --- |
| `-addr` | `FLINTERNET_ADDR` (or `PORT`) | `:8080` | HTTP listen address |
| `-base-url` | `FLINTERNET_BASE_URL` | `https://www.pinterest.com` | Upstream Pinterest web host |
| `-timeout` | `FLINTERNET_TIMEOUT` | `20s` | Upstream request timeout |
| `-cache-ttl` | `FLINTERNET_CACHE_TTL` | `5m` | In-memory cache TTL |
| `-app-version` | `FLINTERNET_APP_VERSION` | `""` | Optional `X-App-Version` header |
| `-allow-unverified-pagination` | `FLINTERNET_ALLOW_UNVERIFIED_PAGINATION` | `true` | Allow best-effort cursor continuations |
| `-share-key` | `FLINTERNET_SHARE_KEY` | `""` | Secret key enabling encrypted pin correlation tokens (empty disables correlation) |
| `-log-level` | `FLINTERNET_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |

## License

Licensed under the [GNU General Public License v3.0](LICENSE).
