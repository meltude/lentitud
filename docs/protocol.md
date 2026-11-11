# Lentitud WebSocket protocol

This document describes what a client must send to the server and what it
receives back. Everything is JSON over a WebSocket connection.

## Connecting

1. Open a WebSocket connection to the server endpoint:

   ```
   ws://<host>/ws      (or wss:// for TLS servers)
   ```

2. The first message must be a **join**. If no valid join arrives within
   10 seconds (default `-join-timeout`), the server closes the connection.

3. When the join is accepted, the server replies with a **welcome** message
   and broadcasts a **system** message (`"<nick> joined"`) to everyone else.

If the server is full (default 20 users, `-max-users`), the join is rejected
with an `error` message: `"server full"`.

## Message envelope

Every message is a JSON object with a `type` field:

```json
{"type": "...", "nick": "...", "text": "...", "time": "..."}
```

| Field | Direction | Meaning                                            |
| ----- | --------- | -------------------------------------------------- |
| type  | both      | Message kind (see below)                           |
| nick  | both      | Author nickname (server fills it; clients must not)|
| text  | both      | Message body                                       |
| time  | server    | RFC3339 UTC timestamp the server attached          |

## What the client must send

There are exactly **two** valid message types a client may send:

### 1. `join` — exactly once, first

```json
{"type": "join", "nick": "alice"}
```

- `nick` is required.
- Length: 1–32 bytes.
- Characters: letters, digits, `_`, and `-` only.
- Nicknames are unique by exact match. A disconnected user's nick becomes
  free again after they leave.
- Picking someone else's live nickname returns an `error`:
  `"nickname already in use"`.

### 2. `message` — repeatedly, after the join

```json
{"type": "message", "text": "hello"}
```

- Do **not** send `nick` with chat messages. The server attaches the
  authenticated connection nickname itself, so one client cannot impersonate
  another.
- `text` must be non-empty, at most 4096 bytes (`-max-message-bytes`), and
  must not contain `\r` or `\n`.
- Rate limit: 10 messages per second per connection (`-rate-limit`).
  Exceeding it returns an `error`: `"rate limit exceeded"`.

Sending anything else (including binary frames, invalid JSON, or an unknown
`type`) returns an `error` message.

## What the client receives

| Type      | When                                   | Fields              |
| --------- | -------------------------------------- | ------------------- |
| `welcome` | After a successful join                | `nick`, `time`      |
| `message` | Someone (possibly you) sent a message  | `nick`, `text`, `time` |
| `system`  | Users joining/leaving, server notices  | `text`, `time`      |
| `error`   | Your message was rejected              | `text`              |

Your own chat messages are echoed back to you as `message` frames with your
nick attached.

## Keepalive

The server sends a WebSocket ping every 30 seconds and closes the connection
if no pong arrives within 60 seconds. WebSocket libraries answer pongs
automatically, so a normal client does not have to handle this.

## Closing

Close the WebSocket with a normal close code. When the server shuts down it
broadcasts `"server shutting down"` as a `system` message before closing all
connections.
