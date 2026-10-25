# Delete Watched Media — Scryer rewrite design

**Date:** 2026-10-25 · **Status:** approved in conversation (this doc records it)

## Intent & why

`deletewatchedarr` (v0.1.1) deleted watched Jellyfin media via Sonarr/Radarr. It broke
2026-08-30 when Sonarr 4.0.20 removed `PUT /api/v3/episode/editor` (episode unmonitor
404'd every run; movies kept working). The household has since moved to **Scryer**
(single-binary *arr replacement; the old arr stack is parked). The tool must target
Scryer's GraphQL API instead. **The logic is unchanged**: poll Jellyfin → unmonitor on
watch → delete files after a retention window. Only the action side changes.

## Goals

- Identical behavior to v0.1.1, acting on Scryer instead of Sonarr+Radarr:
  - **Movies**: on watch → unmonitor title; after `movie_retention_days` → delete
    file **and** remove title from Scryer.
  - **Episodes**: on watch → unmonitor episode; after `episode_retention_days` →
    delete episode file. Series/season stay in Scryer (unmonitored).
- Idempotent, stateless (no DB/files), dry-run mode, same config shape and logging style.
- Rename to **Delete Watched Media**: repo `honeybyhomo/delete-watched-media`, Go module
  `github.com/honeybyhomo/delete-watched-media`, binary + image
  `ghcr.io/honeybyhomo/delete-watched-media` (release workflow derives the image from
  `github.repository`, so it follows the rename automatically).

## Non-goals / anti-goals

- No web UI, no state database, no new Go dependencies (plain net/http + encoding/json).
- No change to Jellyfin interaction (same user, same polling; no refresh calls —
  Jellyfin's realtime LibraryMonitor + Scryer's media-server connection handle that).
- Whole-series cleanup stays out of scope (as before).
- The `-dn` library stays out of scope until that stack migrates to Scryer.

## Constraints

- Scryer GraphQL at `http://scryer:8080/graphql`, `Authorization: Bearer <api key>`.
- API mapping (verified live 2026-10-25):
  - lookup: `titlesByExternalIds(source: "TMDB"|"TVDB", values: […])`
  - `setTitleMonitored({titleId, monitored})`, `setEpisodeMonitored({episodeId, monitored})`
  - `deleteEpisodeFiles({titleId, episodeIds, deleteFromDisk, previewFingerprint})`
  - `deleteTitle({titleId, deleteFilesOnDisk, previewFingerprint})`
  - preview queries (`deleteEpisodeFilesPreview`, `deleteTitlePreview`) return a
    `fingerprint` + `requiresTypedConfirmation` — always preview first and pass the
    fingerprint; **abort the individual delete** if typed confirmation is demanded.
- Known gotchas: season/episode numbers are **strings** in Scryer GraphQL (coerce);
  `setEpisodeMonitored` is per-episode (no bulk mutation — acceptable, log the count).
- Env vars: `JELLYFIN_API_KEY` (reused) + `SCRYER_API_KEY` (new, dedicated key minted
  via `createMyApiKey`; keys inherit the creating user's permissions — no scope model).
- Deployment: new stack dir `delete-watched-media/` in the docker-compose repo
  (infrastructure-only: traefik network, no web UI, no dataset, config file bind-mounted
  read-only from the stack dir). The parked `deletewatchedarr` service +
  `deletewatchedarr.yaml` are removed from `arr-stack/` (superseded).

## Approaches considered

1. **In-place Go rewrite of this repo (chosen).** Same structure/CI/image pipeline;
   `sonarr.go`+`radarr.go` → one `scryer.go`; `jellyfin.go` nearly untouched. Smallest
   operational delta, keeps the boring-deployment property.
2. **Native Scryer maintenance rule set (rejected).** Scryer's rule engine has the right
   *actions* (`UNMONITOR_SCOPE_DELETE_FILES`, `DELETE_TITLE_AND_FILES`) but no evidence
   its Rego matchers see Jellyfin watch state (schema exposes no watched/last-played
   data; `playbackLinks` empty with login disabled). Can't express "watched by user X,
   N days ago".
3. **Python cron script from the trial's sync tooling (rejected).** Loses the packaged
   container + CI; weaker logging/retry; nothing gained.

## Decisions

- Rename repo/program to **Delete Watched Media** (user decision 2026-10-25); history is
  preserved via GitHub rename; old image name keeps existing tags, new tags follow the
  new name.
- Dry-run first in production, then flip `dry_run: false` (house pattern).
- Delete flow always runs the preview query first (fingerprint + file counts for logs).

## Left to the builder

- Exact Go types for the four Scryer calls; test strategy (httptest fake GraphQL server,
  string-coercion tests, dry-run path); README/config.example rewrite wording; timing of
  the GitHub repo rename vs. push/tag (rename first, then push code with new module path;
  try API rename with the instance PAT, fall back to asking the user to click rename).
