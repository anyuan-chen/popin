# Self-hosting Popin (production VPS)

This guide deploys the full Popin stack — LiveKit, the Go backend, the Next.js
web app, and a Caddy edge — onto a single VPS using Docker Compose. TLS is
automatic via Caddy + Let's Encrypt.

The example uses `popin.andrewchen.uk`, `api.popin.andrewchen.uk`, and
`livekit.andrewchen.uk`. Swap the domain for your own.

## 1. VPS prerequisites

- A Linux VPS (Ubuntu 22.04+ or Debian 12+) with a **public IPv4** address.
- At least 2 GB RAM and 20 GB disk.
- These ports open in your cloud firewall + `ufw`/`iptables`:
  - `80/tcp`, `443/tcp` — Caddy (HTTP + HTTPS)
  - `7881/tcp` — LiveKit TCP fallback
  - `50000-50100/udp` — LiveKit media (WebRTC)
  - (Do **not** expose 7880, 8080, 3000 to the internet; Caddy handles them
    internally.)
- Docker Engine + the `docker compose` plugin installed.

## 2. DNS

Create A/AAAA records (all pointing at your VPS):

```
popin.andrewchen.uk       -> <VPS IP>
api.popin.andrewchen.uk   -> <VPS IP>
livekit.andrewchen.uk     -> <VPS IP>
```

Wait for DNS to propagate before continuing — Caddy needs the records to be
resolvable to fetch a TLS certificate.

## 3. Clone + secrets

```bash
git clone https://github.com/anyuan-chen/popin.git
cd popin

# Generate a real LiveKit key/secret pair:
docker run --rm livekit/livekit-server generate-keys
#   -> API Key:  APIXXXXXXXXXXXX
#   -> API Secret:  ....

cp .env.prod.example .env.prod
$EDITOR .env.prod            # fill in LIVEKIT_API_KEY / LIVEKIT_API_SECRET
chmod 600 .env.prod
```

`.env.prod` is gitignored — never commit it.

## 4. Start the stack

```bash
docker compose --env-file .env.prod \
  -f docker-compose.prod.yml up -d --build
```

First boot takes a minute (images build/pull, Caddy fetches certs). Check:

```bash
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs -f caddy     # watch TLS issuance
```

Once Caddy has certs, verify:

- `https://popin.andrewchen.uk` → Next.js login page
- `https://api.popin.andrewchen.uk/health` → `{"status":"ok"}`
- `wss://livekit.andrewchen.uk` → LiveKit signalling reachable

If TLS issuance fails, ensure DNS is live and ports 80/443 are reachable from
the internet (Caddy uses HTTP-01 challenges by default).

## 5. Firewall hardening (recommended)

```bash
ufw default deny incoming
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 7881/tcp
ufw allow 50000:50100/udp
ufw enable
```

## 6. Operations

- **Logs**: `docker compose -f docker-compose.prod.yml logs -f backend`
- **Restart**: `docker compose -f docker-compose.prod.yml restart backend`
- **Update**: `git pull && docker compose --env-file .env.prod -f docker-compose.prod.yml up -d --build`
- **Backup SQLite**: the database is in the `backend-data` volume. Stop the
  backend, copy the file out, restart:
  ```bash
  docker compose -f docker-compose.prod.yml stop backend
  docker cp $(docker compose -f docker-compose.prod.yml ps -q backend):/app/data/popin.db ./popin-backup.db
  docker compose -f docker-compose.prod.yml start backend
  ```
- **LiveKit key rotation**: edit `.env.prod`, then
  `docker compose --env-file .env.prod -f docker-compose.prod.yml up -d`.

## 7. Connecting the CLI

The released `popin` CLI is built against `api.popin.andrewchen.uk` and
`popin.andrewchen.uk` (see `.goreleaser.yaml` ldflags), so end-users just run:

```bash
brew tap anyuan-chen/popin https://github.com/anyuan-chen/homebrew-popin
brew install popin
popin login        # opens a browser to the hosted login page
popin run          # listen for incoming calls
```

Or one-liner install:

```bash
curl -fsSL https://raw.githubusercontent.com/anyuan-chen/popin/main/scripts/install.sh | sh
```

To point the CLI at a **different** (e.g. self-hosted) server, override at
runtime:

```bash
popin login --backend https://api.example.com --web https://example.com
```

## 8. TURN server (optional, for restrictive NATs)

The default config relies on host STUN via `use_external_ip: true`. For users
behind symmetric / corporate NATs, add a TLS TURN listener. LiveKit ships its
own TURN; add to `livekit.prod.yaml`:

```yaml
turn:
  enabled: true
  domain: livekit.andrewchen.uk
  tls_port: 5349
  secret: "<long-random-secret>"
```

And open `5349/tcp` + `49152-65535/udp` in the firewall (the UDP relay range).
This is not enabled by default to keep the port surface minimal.

## 9. Architecture (production)

```
Internet
   |
   |-- 443/tcp  popin.andrewchen.uk  --> Caddy --> web:3000      (Next.js)
   |-- 443/tcp  api.popin.andrewchen.uk --> Caddy --> backend:8080 (Go API)
   |-- 443/tcp  livekit.andrewchen.uk --> Caddy --> livekit:7880 (WS signalling)
   |-- 7881/tcp  livekit.andrewchen.uk --> livekit:7881          (TCP fallback)
   |-- 50000-50100/udp livekit --> livekit                        (WebRTC media)

Browser (/room page) opens WebRTC directly to livekit.andrewchen.uk:7881 + UDP.
CLI daemon dials wss://api.popin.andrewchen.uk/ws/daemon (bearer token).
```

LiveKit media is **SFU-relayed, not peer-to-peer** — bandwidth scales with the
number of participants, not the number of calls. A single small VPS handles a
handful of low-participant concurrent calls comfortably.