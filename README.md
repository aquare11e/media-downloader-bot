# Media Downloader Bot

A distributed system for managing media downloads through Telegram, with automatic Plex integration for media and an [Ownfoil](https://github.com/a1ex4/ownfoil) shop for Nintendo Switch games.

## Services

The system consists of four microservices:

1. **Bot Service** (`/bot`)
   - Telegram bot interface
   - Handles user commands and authentication
   - Communicates with coordinator service

2. **Coordinator Service** (`/coordinator-service`)
   - Central service that coordinates all operations
   - Manages download requests
   - Handles communication between services
   - Uses Redis for state management

3. **Plex Service** (`/plex-service`)
   - Updates Plex library

4. **Transmission Service** (`/transmission-service`)
   - Handles download operations
   - Provides download status updates

[Ownfoil](https://github.com/a1ex4/ownfoil) is an external dependency, deployed as its own
stack like Transmission and Plex: it serves the Nintendo Switch library and indexes
`SWITCH_DIR_PATH` straight off the filesystem, with no integration code on either side
(see [Switch downloads](#switch-downloads)).

## Prerequisites

- Go 1.23 or later
- Docker and Docker Compose
- protoc (Protocol Buffers compiler)
- Redis
- Plex Media Server
- Transmission torrent client
- Ownfoil (only for the `SWITCH` category, deployed separately)

## Development Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/aquare11e/media-downloader-bot.git
   cd media-downloader-bot
   ```

2. Generate protobuf files:
   ```bash
   make protoc
   ```

3. Set up environment variables:
   - Copy `.env.example` to `.env` in each service directory
   - Fill in the required environment variables

4. Build and run services:
   ```bash
   docker-compose up -d
   ```

## Environment Variables

### Bot Service
- `TELEGRAM_BOT_TOKEN`: Telegram bot token
- `ALLOWED_USERS`: Comma-separated list of allowed Telegram user IDs
- `COORDINATOR_SERVICE_URL`: URL of the coordinator service

### Coordinator Service
- `SERVICE_PORT`: The port number on which the gRPC server will listen.
- `TRANSMISSION_SERVICE_URL`: The URL of the Transmission service.
- `PLEX_SERVICE_URL`: URL of the Plex service
- `REDIS_URL`: Redis connection URL
- `REDIS_PASSWORD`: Redis password
- `*_DIR_PATH`: Paths for different media types
- `SWITCH_DIR_PATH`: Download directory of the `SWITCH` category (see [Switch downloads](#switch-downloads))

### Plex Service
- `SERVICE_PORT`: gRPC service port
- `PLEX_HOST`: Plex server host
- `PLEX_PORT`: Plex server port
- `PLEX_TOKEN`: Plex authentication token
- `PLEX_CATEGORY_*`: Category IDs for different media types

### Transmission Service
- `SERVICE_PORT`: gRPC service port
- `TRANSMISSION_HOST`: Transmission host
- `TRANSMISSION_PORT`: Transmission port
- `TRANSMISSION_USER`: Transmission username
- `TRANSMISSION_PASSWORD`: Transmission password

## Docker Images

Docker images are automatically built and pushed to GitHub Container Registry (GHCR) on each push to main branch.

Images are available at:
- `ghcr.io/aquare11e/media-downloader-bot/bot:latest`
- `ghcr.io/aquare11e/media-downloader-bot/coordinator:latest`
- `ghcr.io/aquare11e/media-downloader-bot/plex:latest`
- `ghcr.io/aquare11e/media-downloader-bot/transmission:latest`

Ownfoil is not built or deployed by this repository: it lives in its own stack and is pulled
from Docker Hub as `a1ex4/ownfoil`.

## Switch downloads

The `SWITCH` category is an independent download flow: Plex is never involved, and there
is no Ownfoil integration code — the two sides only share a directory on disk.

```text
Telegram
   ↓
media-downloader-bot
   ↓
Transmission
   ↓
SWITCH_DIR_PATH
   ↓
Ownfoil
```

1. In the bot, `/download` → send a magnet link, `.torrent` file or Rutracker URL → pick `🎮 Switch`.
2. The coordinator maps `RequestType_SWITCH` to `SWITCH_DIR_PATH` and hands the torrent to transmission.
3. When the torrent completes, the coordinator marks the request as successful and runs **no**
   post-download action — the message is `✅ Download completed / Added to Switch library.`
   (Plex is only refreshed for media categories.)
4. Ownfoil has the same directory mounted as `/games` and picks the new files up by itself:
   its file watcher (enabled by default) reacts to filesystem events, and `Scan library` in
   the Web UI forces a rescan. Nothing calls Ownfoil - there is no API client, no polling.

### Shared directory model

`SWITCH_DIR_PATH` and Ownfoil's `/games` are the same physical directory, reached from two sides:

| Component | Path | Access |
| --- | --- | --- |
| Transmission | `SWITCH_DIR_PATH`, i.e. `/downloads/switch` as the transmission container sees it | read-write |
| Ownfoil | `/games`, bind mount of the same host directory | read (read-write only if its organizer/compression are enabled) |

Both stacks run outside this compose project, so the paths are container paths of one host
directory. `SWITCH_DIR_PATH` is what *transmission* writes to: with a host mount of
`/disks/heavy8:/downloads`, the library at `/disks/heavy8/switch` is `SWITCH_DIR_PATH=/downloads/switch`
here and `/disks/heavy8/switch:/games` in the Ownfoil stack.

### Ownfoil

Ownfoil is not part of this compose project - it is deployed as its own stack, the same way the
Transmission daemon and the Plex server are. This repository only needs to agree with it on one
thing: the directory. Nothing calls Ownfoil, and Ownfoil calls nothing here.

Its stack mounts the host directory behind `SWITCH_DIR_PATH` as `/games` (already the app's
default library path, so a fresh install scans it with no UI step), keeps `/app/config` and
`/app/data` on persistent volumes, and serves the Web UI on port `8465` - published on the LAN
directly or through a reverse proxy. See the upstream
[Install.md](https://github.com/a1ex4/ownfoil/blob/master/Install.md) for the container itself,
and create the admin account on first login: until it exists, anyone who can reach the UI can
change the configuration.

### Volume permissions

The two stacks share one directory, so their UIDs have to agree:

- Ownfoil runs as its `PUID:PGID` (`1000:1000` by default) and must be able to **read** what
  transmission writes - match the owner of the download directory
  (`stat -c '%u %g' /path/to/switch/library`), or give Ownfoil the owning group with group-read
  permissions. If they do not match, a scan silently finds nothing. Do not `chmod 777` the library.
- Read access is enough to index and serve the library. Uploads through the Web UI, the organizer,
  NSZ compression and "delete older updates" write back, so mount `/games` read-write only when
  those are wanted (all of them are off by default) - and mind that the organizer moving files
  under transmission is a good way to break seeding.
- Ownfoil's own `/app/config` and `/app/data` must be **writable** by its `PUID:PGID`. Docker
  creates missing bind-mount directories as `root`, so create and `chown` them before its first
  start.
- The library should be a local filesystem for instant indexing: Ownfoil's file watcher uses
  inotify, which network mounts (NFS/SMB/sshfs) do not support - those fall back to interval
  polling instead.

## Usage

1. Start a conversation with your Telegram bot
2. Send `/start` to begin
3. Available commands:
   - `/download` - Start a download (categories: Films, Series, Cartoons, Cartoon Series, Cartoon Shorts, 🎮 Switch)
   - `/status` - Check the current status of ongoing downloads. The list and the per-download
     details refresh themselves every 2 seconds; auto-refresh pauses after 5 minutes of
     inactivity and resumes on the next button tap
   - `/help` - Get a list of available commands and their descriptions

## License

This project is licensed under the MIT License - see the LICENSE file for details. 