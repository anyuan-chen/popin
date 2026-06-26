# Popin - Video Call Infrastructure

A Go + Next.js video call application built with LiveKit, featuring username/password auth with long-lived cookie sessions backed by SQLite.

The browser is a thin client: it hosts the login page (used both for normal login and for authorizing the CLI daemon) and a `/room` page that renders an in-progress call from query params. Starting calls, room management, and friend management all live in the CLI daemon (`popin`).

## Install the CLI (end-users)

The released `popin` CLI targets the hosted instance and needs no flags:

**Homebrew (macOS + Linux):**
```bash
brew tap anyuan-chen/popin https://github.com/anyuan-chen/homebrew-popin
brew install popin
popin login   # opens a browser to authorize (or `popin signup` if you don't have an account yet)
popin listen  # listen for incoming calls
```

**Or one-liner:**
```bash
curl -fsSL https://raw.githubusercontent.com/anyuan-chen/popin/main/scripts/install.sh | sh
popin login
popin listen
```

To point the CLI at a self-hosted server instead, set the URLs once and they
persist in `~/.config/popin/config.json`:
```bash
popin config --server https://api.example.com --web https://example.com
popin login
popin listen
```
(`BACKEND_URL` / `WEB_URL` environment variables still work as a fallback if no
config file value is set.)

## Self-hosting

See [`docs/DEPLOY.md`](docs/DEPLOY.md) for a full VPS deploy with Docker Compose
+ Caddy (automatic TLS) + LiveKit.

## Features

- **LiveKit Integration**: Full room management and participant handling
- **User Authentication**: Username/password registration and login with bcrypt
- **Session Management**: SQLite-backed long-lived cookie sessions (99-year default, server-revocable)
- **Next.js Frontend**: Login + call-viewer only — renders the active call when opened with `?token=&name=&livekit_url=` query params
- **CLI Daemon**: Initiates and receives calls, manages friends (see the `popin` binary below)
- **Noise Suppression**: Browser-level noise suppression, echo cancellation, and auto gain control
- **Docker Orchestration**: One-command startup with Docker Compose

## Project Structure

```
popin/
├── cmd/
│   ├── server/          # Go backend entry point (popin-server)
│   └── daemon/          # CLI daemon entry point (popin)
├── config/              # Configuration management
├── db/                  # SQLite database setup and migrations
├── auth/                # Authentication service and middleware
├── server/              # HTTP API server + daemon presence WebSocket
├── room/                # LiveKit room and token management
├── livekit.yaml         # LiveKit server config
├── Dockerfile           # Go backend Docker build
├── docker-compose.yml   # Orchestrates LiveKit + backend + web
├── .env.example
└── web/                 # Next.js frontend
    ├── src/
    │   ├── app/
    │   │   ├── layout.tsx
    │   │   ├── page.tsx          # Logged-in landing
    │   │   ├── login/page.tsx    # Login / register / daemon authorize
    │   │   └── room/page.tsx     # Call viewer (query-param join only)
    │   ├── components/
    │   │   └── VideoConference.tsx
    │   └── lib/
    │       └── api.ts            # Auth-only API client
    ├── Dockerfile
    ├── next.config.ts            # Rewrites /api to Go backend
    └── package.json
```

## Quick Start with Docker

```bash
docker compose up --build
```

### Dev Scripts

Handy scripts in `scripts/` manage all three services locally with logging:

```bash
./scripts/start.sh        # start livekit, backend, web (in background w/ logs)
./scripts/start.sh --docker  # use docker compose instead
./scripts/stop.sh         # stop all services
./scripts/restart.sh      # stop + start
./scripts/status.sh       # show port + process status
./scripts/logs.sh         # tail all logs (or: logs.sh backend)
./scripts/health.sh       # check tools, config, and /health endpoint
```

Logs are written to `scripts/logs/<service>.log`.

Then open `http://localhost:3000` in your browser to register an account / log in.
Calls themselves are attended in the browser: a deep link like
`http://localhost:3000/room?token=...&name=...&livekit_url=...` opens the call UI.

Services:
- **web** (Next.js) on port 3000
- **backend** (Go API) on port 8080
- **livekit** on ports 7880 (WebSocket), 7881 (TCP), 50000-50100 (UDP)

## Manual Setup (Without Docker)

### Prerequisites

1. **Go 1.26+**: [go.dev](https://go.dev/dl/)
2. **Node.js 22+**: [nodejs.org](https://nodejs.org/)
3. **LiveKit Server**: `brew install livekit/tap/livekit-server`

### Running

1. **Start LiveKit server** (terminal 1):
   ```bash
   livekit-server --config livekit.yaml --dev
   ```

2. **Start Go backend** (terminal 2):
   ```bash
   cp .env.example .env
   go run ./cmd/server
   ```

3. **Start Next.js frontend** (terminal 3):
   ```bash
   cd web
   npm install
   npm run dev
   ```

4. Open `http://localhost:3000`

## Architecture

```
Browser (Next.js, :3000)
  |
  |-- /api/* (rewritten) --> Go Backend (:8080)
  |                             |-- gRPC --> LiveKit Server (:7880)
  |                             |-- SQLite (users, sessions)
  |
  |-- WebSocket + WebRTC --> LiveKit Server (:7880/:7881/UDP)
```

The Next.js dev server proxies `/api/*` requests to the Go backend via `rewrites` in `next.config.ts`. This means cookies work on the same origin during development. In Docker, the Next.js standalone server proxies to the Go backend container.

The browser connects directly to the LiveKit server for real-time media (video, audio, screen share).

## CLI Daemon (`popin`)

A second Go binary — `cmd/popin` — runs as a long-lived process on a user's
machine and receives incoming video calls. A caller drives the same
`POST /api/call {target_username}` endpoint (the CLI caller flow is wired up
separately; for now any HTTP client works) and the backend looks up the
target's live daemon WebSocket and pushes an `incoming_call` message
containing a LiveKit token and a prebuilt browser URL. The daemon
auto-accepts by opening that URL in the system browser — the `/room` page
accepts `?token=&name=&livekit_url=` query params and joins LiveKit directly,
**no web-app login required** on the callee's machine.

### Building & authorizing

```bash
# From the repo root:
go build -o popin ./cmd/popin
go build -o popin-server ./cmd/server

# Authorize the daemon by opening a browser tab to the web login page:
./popin login
#   -> opens http://localhost:3000/login?redirect=http://127.0.0.1:<port>/callback
#   -> you log in as your user; the web app POSTs /api/auth/login with
#      kind:"daemon" + that redirect, gets a daemon token, and redirects the
#      browser to the callback URL carrying ?token=. The daemon's local
#      server captures it and writes it to ~/.config/popin/daemon-token (0600).

# Don't have an account yet? `signup` opens the same flow but pre-sets the
# web page to register mode, so you create the account and authorize the
# daemon in one go:
./popin signup

# Point at a self-hosted server (optional; persists to ~/.config/popin/config.json):
./popin config --server https://api.example.com --web https://example.com
./popin config            # print current resolved URLs

# Then run the daemon:
./popin listen
#   -> opens a WebSocket to <server>/ws/daemon?token=<...>, reconnects
#      on failure with exponential backoff, and opens a browser tab for each
#      incoming call.

./popin logout   # deletes the stored token and revokes it server-side.

# Friend management:
./popin friend alice   # send a friend request to user "alice"
./popin friend         # open the friends TUI (bubbletea): tab between
                       # Incoming (j/k to move, a to accept, d to deny)
                       # and Friends (j/k to move, u to unfriend with y/n
                       # confirmation), r to refresh, q to quit.
```

URL resolution precedence: `popin config` values (config file) > `BACKEND_URL`
/ `WEB_URL` env vars (or `.env`) > built-in defaults (baked in at build time
for release binaries). A live daemon WebSocket is the source of truth for "user
X is online right now"; if no daemon is connected for a user, calls to them
return `{status:"unavailable"}`.

## API Endpoints

### Auth

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/auth/register` | POST | No | Create user (username, password, display_name) |
| `/api/auth/login` | POST | No | Login, sets `popin_session` cookie |
| `/api/auth/me` | GET | Yes | Get current user |
| `/api/auth/logout` | POST | Yes | Logout, clears session |

### Rooms

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/rooms` | GET | No | List active rooms |
| `/api/rooms/create` | POST | Yes | Create a room |
| `/api/token` | POST | Yes | Generate LiveKit token (identity = logged-in username) |
| `/api/call` | POST | Yes | Call a user's daemon; pushes an `incoming_call` to the target and returns a token so the caller can join the room |
| `/ws/daemon` | WS | Daemon | Daemon presence WebSocket (long-lived; `kind:"daemon"` sessions only) |
| `/health` | GET | No | Health check |

### Friends

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/friends` | GET | Yes | List accepted friends + incoming requests |
| `/api/friends/request` | POST | Yes | Send a friend request (`target_username`) |
| `/api/friends/accept` | POST | Yes | Accept a pending request (`username`) |
| `/api/friends/deny` | POST | Yes | Deny a pending request (`username`) |
| `/api/friends/unfriend` | POST | Yes | Unfriend an accepted friend (`username`) |

Friendship state is append-only: every transition (`request`/`accept`/`deny`/
`unfriend`) is a new row in `friendship_events`, and current relationship is
derived from the **latest event** of the unordered pair. Denial and unfriending
never erase history; re-requesting after a `deny`/`unfriend` is simply a fresh
`request` that becomes the new latest event. `/api/call` is gated to accepted
friendships — calling a non-friend returns `403 "not friends"`.

### Authentication kinds

The `sessions` table has a `kind` column (`browser` | `daemon`). `/api/auth/login`
accepts an optional `kind` field:

- `kind:"browser"` (default): sets the `popin_session` cookie as before.
- `kind:"daemon"` (requires `redirect` to an `http(s)://localhost`/`127.0.0.1`
  URL): mints a daemon-kind bearer token, returns it in the JSON body (no
  cookie set). This is used by the `popin login` browser flow to authorize the
  CLI on another machine. Issuing a new daemon token for a user revokes their
  previous daemon tokens (single-active-daemon invariant); the WebSocket layer
  additionally enforces last-connection-wins per user.

## Configuration

### Go Backend

| Variable | Default | Description |
|----------|---------|-------------|
| `LIVEKIT_URL` | `ws://localhost:7880` | LiveKit server URL (server-to-server) |
| `LIVEKIT_CLIENT_URL` | `ws://localhost:7880` | LiveKit server URL (browser-facing) |
| `LIVEKIT_API_KEY` | `devkey` | LiveKit API key |
| `LIVEKIT_API_SECRET` | `secret` | LiveKit API secret |
| `PORT` | `8080` | HTTP server port |
| `BACKEND_URL` | `http://localhost:8080` | Backend base URL (consumed by the CLI daemon) |
| `WEB_URL` | `http://localhost:3000` | Web app base URL (used to build callee browser URLs) |
| `DB_PATH` | `popin.db` | SQLite database file |
| `SESSION_DURATION` | `867240h` (~99 years) | Session lifetime |
| `COOKIE_SECURE` | `false` | Set `true` for HTTPS |
| `COOKIE_DOMAIN` | (empty) | Optional cookie domain |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:3000` | Comma-separated allowed origins |

### Next.js Frontend

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKEND_URL` | `http://localhost:8080` | Go backend URL for API proxying |

## Development

### Go Tests
```bash
go test ./... -v
```

### Next.js Build
```bash
cd web && npm run build
```

### Code Formatting
```bash
go fmt ./...        # Go
go vet ./...        # Go vet
cd web && npm run lint  # Next.js
```

## Security Notes

- **CSRF**: JSON-only API with `SameSite=Lax` cookies. Add CSRF tokens if you add form endpoints.
- **Login rate limiting**: Not implemented. Add before production.
- **HTTPS**: Set `COOKIE_SECURE=true` in production behind TLS.
- **TURN server**: Configure in `livekit.yaml` for production behind NAT.

## License

MIT
