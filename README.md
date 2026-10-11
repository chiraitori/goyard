# Goyard

Goyard is a lightweight, drop-in Go rewrite of [Phineas/lanyard](https://github.com/Phineas/lanyard). It monitors Discord user presences via the Discord Gateway, exposes real-time state via HTTP REST and WebSocket, persists custom user key-value data, and provides Discord bot commands for KV management.

---

## What Works With Goyard (Ecosystem Compatibility)

Because Goyard maintains exact byte-for-byte JSON payload shape and WebSocket protocol compatibility, existing tools and libraries built for Lanyard work with zero modifications:

### Frontend & Client Libraries
- **[`alii/use-lanyard`](https://github.com/alii/use-lanyard)** — Popular React hook for Lanyard REST & WebSocket.
- **[`barbarbar338/react-use-lanyard`](https://github.com/barbarbar338/react-use-lanyard)** — React hook library for Lanyard API.
- **[`leonardssh/use-lanyard`](https://github.com/leonardssh/use-lanyard)** — Vue Composition API hook for Lanyard REST & WebSocket.
- **[`barbarbar338/go-lanyard`](https://github.com/barbarbar338/go-lanyard)** — Go client library with WebSocket support.
- **[`pxseu/lanyard-ui`](https://github.com/pxseu/lanyard-ui)** — Visual web interface for Lanyard user presence & KV data.

### Personal Websites & Portfolios
- **Discord Status Badges / Cards** — Real-time online/offline badges, Spotify "Listening to" cards, and active rich presence widgets on developer portfolios and personal landing pages.
- **Dynamic Readme Widgets** — Live Discord cards and avatar proxy badges in GitHub profile READMEs using `/<user_id>.png` quicklink endpoints.
- **VSCode / Neovim Presences** — Rich presence status display (games, editors, music, stream) embedded on personal dashboards.

---

## Architecture & Differences from Original

| Aspect | Original Lanyard (Elixir / OTP) | Goyard (Go 1.24+) |
|---|---|---|
| **Default Storage** | Redis (required) | Embedded `bbolt` (zero external dependencies) |
| **Optional Storage** | N/A | Redis (build with `-tags redis`) |
| **Binary Size** | Full Erlang BEAM runtime | Single static binary (~11 MB stripped) |
| **Idle Memory** | ~100–200 MB | < 50 MB with thousands of tracked users |
| **Guild Member Indexing** | O(n²) nested search on `GUILD_CREATE` | O(n) hash-indexed mapping |
| **Missing `client_status`** | Worker crash | Handled cleanly with empty status |
| **KV Write on Unmonitored User** | Crashes request | Returns `404 user_not_monitored` |
| **Prefix Stripping** | Strips first character only | Strips entire configured `COMMAND_PREFIX` |

---

## API & WebSocket Reference

### REST API

All JSON responses follow the standard envelope:
- Success: `{"success": true, "data": ...}`
- Error: `{"success": false, "error": {"code": "...", "message": "..."}}`

| Method | Route | Description | Auth |
|---|---|---|---|
| `GET` | `/` | Service information, monitored user count | Public |
| `GET` | `/health` | Healthcheck status and uptime JSON | Public |
| `GET` | `/metrics` | Prometheus metrics exposition | Public |
| `GET` | `/discord` | 302 redirect to `DISCORD_INVITE` (if set) | Public |
| `GET` | `/v1/users/{id}` | Pretty presence record for user | Public |
| `GET` | `/v1/users/@me` | Pretty presence of API key owner | `Authorization: <key>` |
| `PUT` | `/v1/users/{id}/kv/{field}` | Set single KV value (raw body, max 256 KB) | `Authorization: <key>` |
| `PATCH` | `/v1/users/{id}/kv` | Bulk update KV pairs (JSON object, max 1 MB) | `Authorization: <key>` |
| `DELETE`| `/v1/users/{id}/kv/{field}` | Delete a KV key | `Authorization: <key>` |
| `GET` | `/{user_id}.{ext}` | Proxied Discord avatar (`png`, `gif`, `webp`, `jpg`, `jpeg`) | Public |

> Auth note: Pass the raw API key directly in the `Authorization` header (`Authorization: lnyd_...`) without any `Bearer` prefix.

### WebSocket Protocol (`/socket`)

Connect to `ws://HOST:PORT/socket` (or `wss://...`).  
Append `?compression=zlib_json` for RFC 1950 zlib-compressed binary frames.

#### Protocol Flow:
1. **Hello (`op 1`)** — Server sends immediately on connect:
   ```json
   {"op": 1, "d": {"heartbeat_interval": 30000}}
   ```
2. **Initialize (`op 2`)** — Client sends subscription target:
   ```json
   // Single user
   {"op": 2, "d": {"subscribe_to_id": "123456789012345678"}}

   // Multiple users
   {"op": 2, "d": {"subscribe_to_ids": ["123456789012345678", "234567890123456789"]}}

   // All monitored users
   {"op": 2, "d": {"subscribe_to_all": true}}
   ```
3. **Init State (`op 0`, `t: "INIT_STATE"`)** — Server delivers initial presence snapshot.
4. **Presence Updates (`op 0`, `t: "PRESENCE_UPDATE"`)** — Live presence changes pushed as they occur.
5. **Heartbeat (`op 3`)** — Client sends every 30s to maintain connection. Idle timeout is 60s.
6. **Unsubscribe (`op 4`)** — Client can unsubscribe via `unsubscribe_from_id`, `unsubscribe_from_ids`, or `unsubscribe_from_all`.

---

## Bot Commands

Command prefix defaults to `.` (`COMMAND_PREFIX`):

- **`.get <key>`** — Retrieve a KV value.
- **`.set <key> <value>`** — Store a KV value.
- **`.del <key>`** — Delete a KV key.
- **`.kv`** — List all current keys for the author.
- **`.apikey`** — Generate or rotate API key (DMs only).
- **`.help`** — Show command list embed.

**API Key Leak Protection:** If a user accidentally pastes their active API key in `.set`, `.get`, or `.del` commands, Goyard suppresses the command, immediately rotates the key, sends a warning message in the channel, and DMs the replacement key to the user.

---

## Discord Setup

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and create an Application.
2. In the **Bot** tab:
   - Click **Reset Token** and copy the bot token into `BOT_TOKEN`.
   - Enable **Privileged Gateway Intents**:
     - **Presence Intent** (required)
     - **Server Members Intent** (required)
3. In **OAuth2 ➔ URL Generator**:
   - Scope: `bot`
   - Permissions: `View Channels`, `Send Messages`
   - Invite the bot to your Discord server.
4. Users must be in at least one mutual server with the bot to be monitored.

---

## Configuration

Environment variables (read from `.env` automatically if present):

| Variable | Default | Description |
|---|---|---|
| `BOT_TOKEN` | *Required* | Discord bot token |
| `PORT` | `4001` | HTTP listen port |
| `DB_PATH` | `./data/goyard.db` | Embedded bbolt database path |
| `REDIS_URL` | *empty* | Redis URL (e.g. `redis://127.0.0.1:6379`) when built with `-tags redis` |
| `COMMAND_PREFIX` | `.` | Command prefix for bot commands |
| `BOT_PRESENCE` | `you <3` | Bot status activity text |
| `BOT_PRESENCE_TYPE` | `3` | Bot activity type (3 = Watching) |
| `EXTERNAL_URL` | `https://api.lanyard.rest` | Base URL used in bot replies |
| `DISCORD_INVITE` | *empty* | Optional invite URL shown in `/` and redirected at `/discord` |
| `BOT_IDEMPOTENCY_ENV_KEY` | *empty* | Format `ENV=val` to restrict bot command handling to specific instances |
| `LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `LOG_FORMAT` | `text` | Log format (`text` or `json`) |
| `MAX_SOCKETS` | `5000` | Max concurrent WebSocket connections |

---

## Build & Deployment

### Standalone Binary

```bash
# Standard build with embedded bbolt storage
go build -trimpath -ldflags="-s -w" -o goyard ./cmd/goyard

# Build with Redis support
go build -tags redis -trimpath -ldflags="-s -w" -o goyard ./cmd/goyard

# Run
./goyard
```

### CLI Subcommands

```bash
# Run healthcheck
./goyard healthcheck

# Consistent live backup of bbolt database
./goyard backup /path/to/backup.db
```

### Docker

```bash
docker build -t goyard:latest .
docker run -d \
  -p 4001:4001 \
  -e BOT_TOKEN="your_token_here" \
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

## Benchmarks & Performance

Load tested using **k6** against all live endpoints on an AMD Ryzen 7 system with 300 concurrent VUs:

| Test Scenario | Total Requests / Sessions | Throughput | Median Latency | p95 Latency | Success Rate |
|---|---|---|---|---|---|
| **Multi-Endpoint Stress (5,000,000 reqs)** | **5,014,047 HTTP + 1,022 WS** | **44,453 req/s** | **3.12 ms** | **18.48 ms** | **99.99%** |
| Realtime WebSockets + REST Users | 712,337 HTTP + 339 WS | 21,514 req/s | 2.15 ms | 10.50 ms | 100.00% |
| Monitored User Presence (`/v1/users/:id`) | 276,603 HTTP | 17,287 req/s | 2.61 ms | 3.62 ms | 100.00% |

All scenarios executed against live Discord presence records with embedded zero-allocation JSON streaming.

---

## License

MIT License. Ported from [Phineas/lanyard](https://github.com/Phineas/lanyard).
