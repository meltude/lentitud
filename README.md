# lentude

A small WebSocket chat with a server and a terminal client, written in Go.

## Features

- Broadcast chat over WebSocket.
- JSON protocol with server-assigned nicknames and timestamps.
- Case-insensitive unique nicknames.
- Ping/pong keepalive, read limits, rate limiting, and slow-client eviction.
- Origin allowlist and optional TLS.
- Client reconnects with exponential backoff.
- Graceful server shutdown.

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
nix build github:meltude/lentitud#server
nix build github:meltude/lentitud#client
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
NKIT_SERVER=wss://chat.example/ws NKIT_NICK=alice ./client
NKIT_ADDR=:9000 ./server
```

Press Ctrl-C to exit. Type a line and press Enter to send a message.

## Server flags

| Flag                 | Default | Description                                                     |
| -------------------- | ------- | --------------------------------------------------------------- |
| `-addr`              | `:8080` | HTTP listen address                                             |
| `-allowed-origins`   | empty   | Comma-separated extra origins allowed by the WebSocket upgrader |
| `-max-message-bytes` | `4096`  | Maximum message text size                                       |
| `-join-timeout`      | `10s`   | Time allowed to send the join message                           |
| `-ping-interval`     | `30s`   | WebSocket ping interval                                         |
| `-pong-wait`         | `60s`   | Time to wait for a pong                                         |
| `-rate-limit`        | `10`    | Maximum messages per second per client                          |
| `-tls-cert`          | empty   | TLS certificate file                                            |
| `-tls-key`           | empty   | TLS private key file                                            |
| `-log-level`         | `info`  | `debug`, `info`, `warn`, or `error`                             |

Requests with no `Origin` header, such as native clients, are allowed. Browser
requests must either match the server host or be listed in `-allowed-origins`.

Examples:

```bash
./server -addr :8080 -allowed-origins https://chat.example
./server -addr :8443 -tls-cert cert.pem -tls-key key.pem
```

## Client flags

| Flag        | Default                  | Description                       |
| ----------- | ------------------------ | --------------------------------- |
| `-server`   | `ws://localhost:8080/ws` | Server URL (`ws://` or `wss://`)  |
| `-nick`     | empty                    | Nickname, required                |
| `-insecure` | `false`                  | Skip TLS certificate verification |

Nicknames are 1-32 bytes and may contain letters, digits, `_`, and `-`.
Nicknames are unique without regard to case.

If the connection drops, the client reconnects automatically. A reconnecting
client may need to wait for the old server-side connection to time out before
the nickname becomes available again.

## Protocol

Messages are JSON objects with a `type` field:

| Type      | Direction        | Fields                 |
| --------- | ---------------- | ---------------------- |
| `join`    | client to server | `nick`                 |
| `welcome` | server to client | `nick`, `time`         |
| `message` | client to server | `text`                 |
| `message` | server to client | `nick`, `text`, `time` |
| `system`  | server to client | `text`, `time`         |
| `error`   | server to client | `text`                 |

Clients never supply a nickname on outgoing chat messages. The server attaches
the authenticated connection nickname, so one client cannot impersonate another.

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
CGO_ENABLED=0 go test -race ./...
go vet ./...
gofmt -l .
```

The integration tests start a real HTTP server and connect over WebSocket.
