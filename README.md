# lentitud

A small WebSocket chat with a server and a terminal client, written in Go.

## Features

- Broadcast chat over WebSocket.
- JSON protocol with server-assigned nicknames and timestamps.
- Case-insensitive unique nicknames.
- Ping/pong keepalive, read limits, rate limiting, and slow-client eviction.
- Origin allowlist and optional TLS.
- Client reconnects with exponential backoff.
- Optional signed webhook endpoint that posts repository events into the chat.
- Graceful server shutdown: clients receive a `server shutting down` notice and a normal close frame.

## Build with Nix

```bash
git add .
nix build
```

The default package contains both binaries:

```bash
./result/bin/server
./result/bin/client -nick alice
```

Build a single program:

```bash
nix build .#server
nix build .#client
```

There is intentionally no development shell in the flake.

## Build with Go

Go 1.26 or newer is required.

```bash
CGO_ENABLED=0 go build ./cmd/server
CGO_ENABLED=0 go build ./cmd/client
```

This creates `./server` and `./client`.

## Run

Start the server:

```bash
./server
# or choose an address
./server -addr :9000
```

Start one or more clients:

```bash
./client -nick alice
./client -nick bob -server ws://localhost:8080/ws
```

Environment variables are also supported:

```bash
LENTITUD_SERVER=wss://chat.example/ws LENTITUD_NICK=alice ./client
LENTITUD_ADDR=:9000 ./server
```

Press Ctrl-C to exit. Type a line and press Enter to send a message.

When the server is shutting down, connected clients receive a
`* server shutting down` system message followed by a normal close frame.

## Server flags

| Flag | Default | Description |
|---|---|---|
| `-addr` | `:8080` | HTTP listen address |
| `-allowed-origins` | empty | Comma-separated extra origins allowed by the WebSocket upgrader |
| `-max-message-bytes` | `4096` | Maximum message text size |
| `-join-timeout` | `10s` | Time allowed to send the join message |
| `-ping-interval` | `30s` | WebSocket ping interval |
| `-pong-wait` | `60s` | Time to wait for a pong |
| `-rate-limit` | `10` | Maximum messages per second per client |
| `-tls-cert` | empty | TLS certificate file |
| `-tls-key` | empty | TLS private key file |
| `-webhook-secret` | empty | Shared secret for `POST /webhook`; empty disables the endpoint |
| `-log-level` | `info` | `debug`, `info`, `warn`, or `error` |

Server environment variables: `LENTITUD_ADDR`, `LENTITUD_TLS_CERT`,
`LENTITUD_TLS_KEY`, `LENTITUD_WEBHOOK_SECRET`, `LENTITUD_ALLOWED_ORIGINS`, and
`LENTITUD_LOG_LEVEL`. Flags take precedence over the environment.

Requests with no `Origin` header, such as native clients, are allowed. Browser
requests must either match the server host or be listed in `-allowed-origins`.

Examples:

```bash
./server -addr :8080 -allowed-origins https://chat.example
./server -addr :8443 -tls-cert cert.pem -tls-key key.pem
```

## Client flags

| Flag | Default | Description |
|---|---|---|
| `-server` | `ws://localhost:8080/ws` | Server URL (`ws://` or `wss://`) |
| `-nick` | empty | Nickname, required |
| `-insecure` | `false` | Skip TLS certificate verification |

Client environment variables: `LENTITUD_SERVER` and `LENTITUD_NICK`. Flags take
precedence over the environment.

Nicknames are 1-32 bytes and may contain letters, digits, `_`, and `-`.
Nicknames are unique without regard to case.

If the connection drops, the client reconnects automatically with exponential
backoff and jitter. A reconnecting client may need to wait for the old
server-side connection to time out before the nickname becomes available again.

If the server rejects the join, for example because the nickname is already in
use, the client prints `! rejected by the server: <reason>` and keeps retrying
with backoff.

## Webhooks

Set `LENTITUD_WEBHOOK_SECRET` (or `-webhook-secret`) to enable
`POST /webhook`; leaving it empty makes the endpoint return `404`. Accepted
requests are broadcast to the chat as a `system` message.

Authentication uses the secret configured on the sender:

- `X-Hub-Signature-256: sha256=<hex>` with an HMAC-SHA256 of the raw body.
- `X-Gitea-Signature: <hex>` with the same digest, without the prefix.
- `Authorization: Bearer <secret>` for services that only send a plain header.

Push payloads are summarized, for example
`alice pushed 2 commits to owner/repo (main): fix the thing`. Any JSON body with
a `text` field is broadcast verbatim. Control characters are stripped, messages
are truncated to 512 bytes, and bodies larger than 1 MiB are rejected.

To connect a Codeberg repository, open **Settings → Webhooks → Add webhook**,
set the target URL to `https://chat.example/webhook`, choose
`application/json`, enter the same secret, and select the events you want.
Codeberg must be able to reach the URL, so use a public host or a tunnel for
local testing.

## Protocol

Messages are JSON objects with a `type` field:

| Type | Direction | Fields |
|---|---|---|
| `join` | client to server | `nick` |
| `welcome` | server to client | `nick`, `time` |
| `message` | client to server | `text` |
| `message` | server to client | `nick`, `text`, `time` |
| `system` | server to client | `text`, `time` |
| `error` | server to client | `text` |

Clients never supply a nickname on outgoing chat messages. The server attaches
the authenticated connection nickname, so one client cannot impersonate another.

Messages must be a single line. Control characters (including DEL and C1
controls) and Unicode formatting characters such as bidirectional overrides and
zero-width spaces are rejected.

## Security notes

- Use `wss://` on untrusted networks. The server can terminate TLS directly with
  `-tls-cert` and `-tls-key`, or run behind a TLS reverse proxy.
- The origin allowlist protects browser clients from cross-site WebSocket
  hijacking. It does not authenticate native clients.
- There is no user authentication, authorization, message persistence, or
  end-to-end encryption.
- Do not expose the plain `ws://` server directly to the public internet.

## Development

```bash
gofmt -l .
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
go test -race ./...
```

The race detector requires cgo, so `go test -race` needs a C compiler on
`PATH`. On Nix without `gcc` available, use `CGO_ENABLED=0 go test ./...`
and skip `-race`.

CI runs these checks with Forgejo Actions (`.forgejo/workflows/test.yml`).
The integration tests start a real HTTP server and connect over WebSocket.
