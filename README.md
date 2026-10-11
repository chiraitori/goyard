<div align="center">

# 🛥️ Goyard

**A lightweight, crash-resilient Go rewrite of [Lanyard](https://github.com/Phineas/lanyard).**  
Exposes Discord presences via a live REST API & WebSocket, provides user KV storage, and includes a Discord bot.

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Docker](https://img.shields.io/badge/Docker-scratch-2496ED?style=flat&logo=docker)](https://www.docker.com/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

</div>

---

## ⚡ Highlights

- **100% Drop-In Compatible:** Byte-compatible JSON shapes and field ordering. Works instantly with existing client libraries (`use-lanyard`, `lanyard.py`, `go-lanyard`, React hooks) with zero code changes.
- **Embedded Database Out-of-the-Box:** Pure Go embedded `bbolt` engine by default. Run locally or in a single container with **zero external services** needed (no Redis or Postgres required).
- **Optional Redis Backend:** Can be built with `-tags redis` for existing Lanyard Redis clusters or shared sync setups.
- **Ultra Lightweight:** Single ~11 MB static binary (< 20 MB container on `scratch`), idle RSS < 50 MB with thousands of monitored users.
- **BEAM-Like Fault Tolerance:** Supervised Discord gateway client and HTTP server with panic guards on all background goroutines.
- **Discord Avatar Quicklinks:** Built-in CDN image proxy (`/<user_id>.png`, `.gif`, `.webp`, `.jpg`, `.jpeg`).
- **Extensions Ready:** Pluggable extension hook API in `internal/ext` to add custom HTTP routes, bot commands, or WebSocket events.

---

## 🔌 API & WebSocket Reference

### REST Endpoints

| Method | Endpoint | Description | Auth |
|---|---|---|---|
| `GET` | `/` | Root information & monitored user count | None |
| `GET` | `/health` | Healthcheck status JSON | None |
| `GET` | `/metrics` | Prometheus metrics exposition | None |
| `GET` | `/discord` | Redirects to `DISCORD_INVITE` (if configured) | None |
| `GET` | `/v1/users/{id}` | Pretty presence for user | None |
| `GET` | `/v1/users/@me` | Pretty presence for API key owner | API Key |
| `PUT` | `/v1/users/{id}/kv/{key}` | Set single KV pair (raw body) | API Key |
| `PATCH` | `/v1/users/{id}/kv` | Atomic bulk update KV pairs (JSON object) | API Key |
| `DELETE`| `/v1/users/{id}/kv/{key}` | Delete a KV pair | API Key |
| `GET` | `/{user_id}.{ext}` | Proxied Discord avatar (`png`, `gif`, `webp`, `jpg`, `jpeg`) | None |

> **Note on Authentication:** Pass your raw API key in the `Authorization` header directly without any prefix (`Authorization: lnyd_...`).

---

### WebSocket Protocol (`/socket`)

Connect via `ws://HOST:PORT/socket` (or `wss://...`).  
Optionally append `?compression=zlib_json` for RFC 1950 zlib-compressed binary frames.

#### Opcodes:
- **`op: 1` Hello (Server ➔ Client):** Sent on connection (`{"op":1,"d":{"heartbeat_interval":30000}}`).
- **`op: 2` Initialize (Client ➔ Server):** Subscribe to users:
  ```json
  // Option A: Single user
  {"op": 2, "d": {"subscribe_to_id": "123456789012345678"}}

  // Option B: Multiple users
  {"op": 2, "d": {"subscribe_to_ids": ["123456789012345678", "234567890123456789"]}}

  // Option C: All monitored users
  {"op": 2, "d": {"subscribe_to_all": true}}
  ```
- **`op: 0` Event (Server ➔ Client):**
  - `t: "INIT_STATE"` — Sent immediately upon initialization.
  - `t: "PRESENCE_UPDATE"` — Real-time live presence changes.
- **`op: 3` Heartbeat (Client ➔ Server):** Send periodically (every 30s) to keep socket alive (idle timeout 60s).
- **`op: 4` Unsubscribe (Client ➔ Server):** Unsubscribe from users or all (`{"op": 4, "d": {"unsubscribe_from_id": "..."}}`).

---

## 🤖 Discord Bot Commands

Prefix defaults to `.` (configurable with `COMMAND_PREFIX`):

- **`.get <key>`** — Retrieve a KV value.
- **`.set <key> <value>`** — Set a KV value.
- **`.del <key>`** — Delete a KV key.
- **`.kv`** — List all current keys.
- **`.apikey`** — Generate or rotate your API key (DMs only).
- **`.help`** — Show command reference embed.

> 🛡️ **Leak Protection:** If you accidentally paste your API key in a `.set`, `.get`, or `.del` command, Goyard immediately intercepts it, purges the key, rotates it, and DMs you a replacement key.

---

## 🛠️ Discord Setup

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and create a new Application.
2. In the **Bot** tab:
   - Click **Reset Token** and copy the token into `BOT_TOKEN`.
   - Under **Privileged Gateway Intents**, enable:
     - **Presence Intent**
     - **Server Members Intent**
3. In **OAuth2 ➔ URL Generator**:
   - Scope: `bot`
   - Permissions: `View Channels`, `Send Messages`
   - Open the generated URL to invite your bot to your server.
4. Anyone whose presence you want to monitor must share a server with the bot!

---

## ⚙️ Configuration

Set via `.env` file or environment variables:

| Variable | Default | Description |
|---|---|---|
| `BOT_TOKEN` | **Required** | Discord bot token |
| `PORT` | `4001` | HTTP listen port |
| `DB_PATH` | `./data/goyard.db` | Embedded bbolt database path |
| `REDIS_URL` | *empty* | Redis connection URL (when built with `-tags redis`) |
| `COMMAND_PREFIX` | `.` | Command prefix for Discord bot |
| `BOT_PRESENCE` | `you <3` | Bot status text |
| `BOT_PRESENCE_TYPE` | `3` | Bot status type (0=Playing, 1=Streaming, 2=Listening, 3=Watching, 5=Competing) |
| `EXTERNAL_URL` | `https://api.lanyard.rest` | Public base URL used in bot replies |
| `DISCORD_INVITE` | *empty* | Optional invite URL shown in `/` and redirected at `/discord` |
| `BOT_IDEMPOTENCY_ENV_KEY` | *empty* | Format `ENV=val` to run bot commands on specific instances only |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `text` | Log format: `text` or `json` |
| `MAX_SOCKETS` | `5000` | Max concurrent WebSocket connections |

---

## 🚀 Running Goyard

### Build & Run from Source

```bash
# Standard build (uses embedded bbolt storage)
go build -trimpath -ldflags="-s -w" -o goyard ./cmd/goyard

# Or build with optional Redis support
go build -tags redis -trimpath -ldflags="-s -w" -o goyard ./cmd/goyard

# Run (picks up .env automatically)
./goyard
```

### CLI Subcommands

```bash
# Healthcheck (checks GET http://127.0.0.1:$PORT/health)
./goyard healthcheck

# Live consistent backup of embedded bbolt database
./goyard backup /path/to/backup.db
```

---

## 🐳 Docker Deployment

### Docker CLI

```bash
docker build -t goyard:latest .
docker run -d \
  -p 4001:4001 \
  -e BOT_TOKEN="your_bot_token_here" \
  -v goyard-data:/data \
  --name goyard \
  goyard:latest
```

### Docker Compose

```yaml
services:
  goyard:
    image: goyard:latest
    build: .
    restart: unless-stopped
    environment:
      BOT_TOKEN: ${BOT_TOKEN}
      EXTERNAL_URL: https://api.example.com
    ports:
      - "4001:4001"
    volumes:
      - goyard-data:/data

volumes:
  goyard-data:
```

---

## ⚖️ License

MIT License. Designed and ported from [Phineas/lanyard](https://github.com/Phineas/lanyard).
