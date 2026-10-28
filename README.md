# delete-watched-media

Automatically deletes media you've already watched from **Jellyfin**, via **[Scryer](https://www.scryer.media/scryer/)** — at **episode** and **movie** granularity.

> Polls Jellyfin for watched items → unmonitors them in Scryer immediately → deletes their files after a configurable number of days.

Formerly `deletewatchedarr` (Jellyfin + Sonarr/Radarr). The logic is unchanged from v0.1; only the backend changed when the server moved from Sonarr/Radarr to Scryer. Repo and image were renamed accordingly — old `ghcr.io/honeybyhomo/deletewatchedarr` tags stay, new builds publish under the new name.

Most "media cleanup" tools either delete whole shows only, require Plex + Tautulli, or are heavyweight rule-engine apps. `delete-watched-media` is the boring thing a lot of people actually want: **"I watched it; get it off my disk, but keep the show tracked so it doesn't re-download."**

## What it does

On each poll (default every 60 minutes), for the configured Jellyfin user:

**Movies**
- *On watch* → unmonitor in Scryer (no more upgrade searches)
- *After `movie_retention_days`* → delete the file **and** remove the title from Scryer

**Episodes**
- *On watch* → unmonitor that episode in Scryer
- *After `episode_retention_days`* → delete the episode file from disk
- The **series and season stay in Scryer** (unmonitored) — whole-show cleanup is left to you, since that's rare

Deletions are **idempotent**: items already without a file (or already removed) are skipped, so re-polling is a no-op and there's no database/state file to manage.

It deletes through Scryer's GraphQL API (preview-fingerprinted mutations — Scryer itself refuses deletes that would demand a typed confirmation), so it works with **any** downloader (Usenet/Weaver/SABnzbd, qBittorrent, …) — it does not touch your download client. No Jellyfin refresh is needed either: Jellyfin's realtime library monitor picks up the deletions, and Scryer's media-server connection pushes refreshes.

## Configuration

Settings live in a `config.yaml` (no secrets → safe to commit/share). API keys come from the environment.

`config.yaml`:

```yaml
jellyfin:
  url: http://jellyfin:8096
  username: admin
scryer:
  url: http://scryer:8080
movie_retention_days: 2
episode_retention_days: 1
unmonitor_on_watch: true
interval_minutes: 60
log_level: info
```

Environment variables (secrets):

| Variable | Required | Purpose |
|----------|----------|---------|
| `JELLYFIN_API_KEY` | yes | Jellyfin API key (Dashboard → API Keys) |
| `SCRYER_API_KEY` | yes | Scryer API key (Settings → Security → API Keys; keys inherit your user's permissions) |

## Run with Docker

```bash
docker run -d \
  --name delete-watched-media \
  -v ./config.yaml:/config.yaml:ro \
  -e JELLYFIN_API_KEY=... \
  -e SCRYER_API_KEY=... \
  --restart unless-stopped \
  ghcr.io/honeybyhomo/delete-watched-media:latest
```

Or with Docker Compose:

```yaml
services:
  delete-watched-media:
    image: ghcr.io/honeybyhomo/delete-watched-media:latest
    container_name: delete-watched-media
    environment:
      - JELLYFIN_API_KEY=...
      - SCRYER_API_KEY=...
    volumes:
      - ./config.yaml:/config.yaml:ro
    restart: unless-stopped
```

Put the container on the same Docker network as your Jellyfin/Scryer containers so the `url:` hostnames resolve.

## Safety

- **There is no dry-run mode** (removed in v0.2.1): every cycle acts for real. Deploying the tool is the decision to delete; old config files carrying a `dry_run` key simply ignore it.
- Every deletion runs Scryer's preview query first and passes its fingerprint; if Scryer would require a typed confirmation for a delete, the tool refuses rather than bypassing the guard.
- The tool only ever acts on items **marked watched** for the configured Jellyfin user — never on unwatched media.

## Why not …?

| Tool | Why this exists instead |
|------|------------------------|
| Sanitarr | Deletes whole series only (no per-episode/per-movie-on-watch) |
| Deleterr | Requires Plex + Tautulli |
| Maintainerr | Capable but a heavyweight rule-engine + DB + web UI |
| OCDarr | Heavyweight beta app |
| Scryer maintenance rules | Scryer's own rule engine has the right *actions* (unmonitor/delete-files) but can't match on Jellyfin watch state — no watched/last-played data reaches its matchers |
| Jellyfin plugins (Media Cleaner etc.) | Delete files but don't unmonitor/remove in the media manager, so library entries re-download |

## Development

```bash
go run . --config config.example.yaml   # needs real URLs/keys to do anything useful
go vet ./...
go test ./...
```

Images are published for `linux/amd64` and `linux/arm64` automatically on every `v*` tag via GitHub Actions.

## License

MIT
