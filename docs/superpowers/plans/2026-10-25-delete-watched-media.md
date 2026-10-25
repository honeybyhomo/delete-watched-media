# Delete Watched Media Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite deletewatchedarr as **delete-watched-media** — identical watch→unmonitor→delete logic, acting on Scryer's GraphQL API instead of Sonarr/Radarr — and deploy it.

**Architecture:** Poll Jellyfin for watched items (unchanged), resolve titles in Scryer by external ID (TMDB for movies, TVDB for series), unmonitor on watch, delete files after retention via preview-fingerprinted mutations. Stateless, no DB, no new dependencies.

**Tech Stack:** Go 1.26, stdlib `net/http` + `encoding/json` (+ existing `gopkg.in/yaml.v3`). Distroless image, ghcr via existing release workflow.

**Spec:** `docs/superpowers/specs/2026-10-25-delete-watched-media-design.md`

## Global Constraints

- No new Go dependencies. GraphQL = plain POST of `{"query":…,"variables":…}` with `Authorization: Bearer <key>`.
- Module path after rename: `github.com/honeybyhomo/delete-watched-media`; binary `delete-watched-media`; image follows `ghcr.io/${{ github.repository }}` automatically.
- Env vars: `JELLYFIN_API_KEY`, `SCRYER_API_KEY` (both required, checked at startup). Config YAML keys: `jellyfin.{url,username}`, `scryer.url`, `movie_retention_days`, `episode_retention_days`, `unmonitor_on_watch`, `interval_minutes`, `dry_run`, `log_level`.
- Scryer returns season/episode numbers as **strings** — coerce with `strconv.Atoi`, skip unparseable with a debug log.
- Every delete runs its `*Preview` query first and passes `previewFingerprint`; if `requiresTypedConfirmation` is true, log an error and skip that delete (never send typed confirmations).
- Idempotency: skip unmonitor when already unmonitored; skip delete when there is no file; `dry_run` logs and performs zero mutations.
- API contracts (verified live 2026-10-25, endpoint `http://scryer:8080/graphql`):

```graphql
query Lookup($src: String!, $val: String!) {
  titlesByExternalIds(source: $src, values: [$val]) {
    id name facet monitored mediaFiles { id }
  }
}
query Series($id: ID!) {
  title(id: $id) {
    id name monitored
    collections { episodes { id seasonNumber episodeNumber monitored mediaFiles { id } } }
  }
}
mutation UnmonitorEpisode($input: SetEpisodeMonitoredInput!) {
  setEpisodeMonitored(input: $input) { id monitored }
}
mutation UnmonitorTitle($input: SetTitleMonitoredInput!) {
  setTitleMonitored(input: $input) { id monitored }
}
query PreviewEpisodeFiles($input: DeleteEpisodeFilesPreviewInput!) {
  deleteEpisodeFilesPreview(input: $input) {
    preview { fingerprint requiresTypedConfirmation totalFileCount targetLabel }
    fileCount failedCount
  }
}
mutation DeleteEpisodeFiles($input: DeleteEpisodeFilesInput!) {
  deleteEpisodeFiles(input: $input) { fileCount failedCount }
}
query PreviewTitle($id: ID!) {
  deleteTitlePreview(titleId: $id) {
    fingerprint requiresTypedConfirmation totalFileCount targetLabel
  }
}
mutation DeleteTitle($input: DeleteTitleInput!) {
  deleteTitle(input: $input) { fingerprint }
}
```

  (Mutation return shapes: use `id monitored` where shown; `deleteEpisodeFiles`/`deleteTitle` may return any non-null — request minimal fields and tolerate `null` data with `"success"` not available: treat HTTP 200 + no `errors` as success.)
- GraphQL errors come back HTTP 200 with an `errors` array — surface joined messages as Go errors.
- External-ID source values: `"TMDB"` for movies, `"TVDB"` for series (API is case-insensitive; verified).

## Review Focus

- **GraphQL 200-with-errors:** a mutation that fails server-side returns HTTP 200 + `errors[]`; a client that only checks the status code reports success for failed deletes. Pinned in Task 1.
- **String season/episode numbers:** `"S1"`-style or non-numeric values must skip that episode, not panic or mis-key. Pinned in Task 4.
- **Jellyfin `ProviderIds` capitalization drift** (`Tmdb`/`TMDB`): existing `providerID()` case-insensitive match must survive the rewrite. Pinned in Task 4.
- **Episode with no file past retention:** must be a no-op (no delete call), not an error. Pinned in Task 4.
- **Preview demanding typed confirmation:** must refuse the delete, not bypass or hang. Pinned in Task 2.

---

### Task 1: Scryer client — transport, lookups

**Files:**
- Create: `scryer.go`
- Create: `scryer_test.go`

**Interfaces:**
- Produces:
  - `type ScryerClient struct` / `func NewScryerClient(baseURL, apiKey string) *ScryerClient`
  - `func (s *ScryerClient) gql(query string, vars map[string]any, out any) error` — POST, Bearer auth, HTTP-status **and** `errors[]` handling
  - `type scTitle struct { ID, Name, Facet string; Monitored bool; FileCount int }`
  - `func (s *ScryerClient) TitleByExternalID(source, externalID string) (*scTitle, error)` — nil, nil when not found
  - `type scEpisode struct { ID, SeasonNumber, EpisodeNumber string; Monitored, HasFile bool }`
  - `type scSeries struct { ID, Name string; Monitored bool; Episodes []scEpisode }`
  - `func (s *ScryerClient) SeriesEpisodes(titleID string) (*scSeries, error)` — flattens `collections[].episodes[]`

- [ ] **Step 1: Write failing tests** in `scryer_test.go` using `httptest` fake servers:
  - `TestGqlSurfacesGraphQLErrors` — server returns 200 + `{"errors":[{"message":"boom"}]}`; `gql` returns error containing `boom`.
  - `TestGqlSendsBearerAndBody` — assert `Authorization: Bearer k` header and decoded `query`/`variables` JSON.
  - `TestTitleByExternalIDFound` — canned `titlesByExternalIds` payload with one title (monitored true, 2 mediaFiles) → `scTitle{FileCount: 2, …}`.
  - `TestTitleByExternalIDNotFound` — empty array → `(nil, nil)`.
  - `TestSeriesEpisodesFlattensCollections` — two collections (incl. empty specials collection 0), string season/episode numbers → flat `[]scEpisode`, `HasFile` derived from `len(mediaFiles) > 0`.
- [ ] **Step 2: Run** `go test ./...` — expect compile failure (`scryer.go` missing).
- [ ] **Step 3: Implement** `scryer.go`: `gql` (30s timeout client; decode `{data, errors}`; join error messages), `TitleByExternalID` (query `Lookup` above), `SeriesEpisodes` (query `Series`; flatten collections; skip episodes whose id is empty).
- [ ] **Step 4: Run** `go test ./...` — pass (old req_test.go still passes; it is removed in Task 5).
- [ ] **Step 5: Commit** `feat: scryer graphql client (transport + external-id lookup + series episodes)`

### Task 2: Scryer client — monitoring + preview-fingerprinted deletes

**Files:**
- Modify: `scryer.go`
- Modify: `scryer_test.go`

**Interfaces:**
- Consumes: `gql` from Task 1.
- Produces:
  - `func (s *ScryerClient) SetEpisodeMonitored(episodeID string, monitored bool) error`
  - `func (s *ScryerClient) SetTitleMonitored(titleID string, monitored bool) error`
  - `func (s *ScryerClient) DeleteEpisodeFiles(titleID string, episodeIDs []string) (fileCount int, err error)` — preview → refuse if `requiresTypedConfirmation` → mutate with `previewFingerprint`, `deleteFromDisk: true`
  - `func (s *ScryerClient) DeleteTitle(titleID string) (fileCount int, err error)` — same handshake, `deleteFilesOnDisk: true`

- [ ] **Step 1: Write failing tests:**
  - `TestSetMonitoredSendInput` — capture request; assert `{"episodeId":"E1","monitored":false}` / `{"titleId":"T1","monitored":false}`.
  - `TestDeleteEpisodeFilesHandshake` — assert preview called with `{"titleId","episodeIds"}`, then mutation called with returned fingerprint and `deleteFromDisk: true`; returns preview `fileCount`.
  - `TestDeleteRefusesTypedConfirmation` — preview returns `requiresTypedConfirmation: true` → error mentioning typed confirmation; **assert no mutation request arrived**.
  - `TestDeleteTitleHandshake` — same for `deleteTitlePreview` → `deleteTitle`.
- [ ] **Step 2: Run** `go test ./...` — new tests fail.
- [ ] **Step 3: Implement** the four methods (queries in Global Constraints; empty-input guard: return nil early for empty `episodeIDs`).
- [ ] **Step 4: Run** `go test ./...` — pass.
- [ ] **Step 5: Commit** `feat: scryer unmonitor + preview-fingerprinted delete mutations`

### Task 3: Config, env, wiring

**Files:**
- Modify: `config.go`, `main.go`
- Create: `config_test.go`

**Interfaces:**
- Produces: `Config{Jellyfin Service; Scryer Service; MovieRetentionDays int; EpisodeRetentionDays int; UnmonitorOnWatch bool; IntervalMinutes int; DryRun bool; LogLevel string}` with `yaml` keys per Global Constraints; `LoadConfig` validates `jellyfin.url`, `jellyfin.username`, `scryer.url`; `main` requires env `JELLYFIN_API_KEY` + `SCRYER_API_KEY`.

- [ ] **Step 1: Write failing tests** in `config_test.go`: full YAML parse (all keys), defaults (`interval_minutes`→60, `log_level`→info), missing `scryer.url` → error naming it.
- [ ] **Step 2: Run** — fail.
- [ ] **Step 3: Implement**: replace `Sonarr`/`Radarr` fields with `Scryer Service \`yaml:"scryer"\``; update validation; `main.go`: two env reads, error message lists both names; startup log line gains `scryer_url` instead of arr URLs; `NewCleaner(cfg, jellyfinKey, scryerKey)`.
- [ ] **Step 4: Run** `go test ./... && go vet ./...` — pass.
- [ ] **Step 5: Commit** `feat: scryer config + env wiring (replaces sonarr/radarr)`

### Task 4: Cleaner rewrite

**Files:**
- Modify: `cleaner.go` (movies + episodes phases)
- Create: `cleaner_test.go`
- Modify: `jellyfin.go` (comment on `SeriesTVDBIDs` mentions Sonarr → Scryer)

**Interfaces:**
- Consumes: Task 1/2 client methods; existing `JellyfinClient` (`UserID`, `WatchedItems`, `SeriesTVDBIDs`), `providerID`, `pastRetention` (keep both helpers as-is).
- Produces: `Cleaner{cfg, jellyfin, scryer}`; `NewCleaner(cfg *Config, jellyfinKey, scryerKey string) (*Cleaner, error)`; `Run()` unchanged contract (user-resolution error aborts; per-phase errors log, don't abort).

- [ ] **Step 1: Write failing tests** in `cleaner_test.go` with **two** httptest servers (jellyfin + scryer) and recorded requests:
  - `TestMoviesUnmonitorOnWatch` — watched movie, in Scryer, monitored, fresh watch → exactly one `setTitleMonitored(false)`, no delete.
  - `TestMoviesDeletePastRetention` — `LastPlayedDate` 3 days ago, retention 2, has files → `deleteTitle` (with preview); also unmonitor first.
  - `TestMovieNotInScryerSkipped` — lookup empty → no mutations, no error.
  - `TestEpisodesUnmonitorAndDelete` — one series, watched ep fresh (unmonitor only) + one past retention with file (batched `deleteEpisodeFiles`, string S/E numbers in payload) + one past retention without file (skipped).
  - `TestDryRunMakesNoMutations` — dry_run true → scryer server receives **only** queries (assert zero mutations).
  - `TestEpisodeStringSeasonNumbers` — Scryer returns `seasonNumber: "2"`, Jellyfin `ParentIndexNumber: 2` → matched.
- [ ] **Step 2: Run** — fail (won't compile: Cleaner still has sonarr/radarr).
- [ ] **Step 3: Implement** `cleaner.go`:
  - Movies: `providerID(it.ProviderIDs, "Tmdb")` → `TitleByExternalID("TMDB", …)` → unmonitor if `UnmonitorOnWatch && monitored` → if `pastRetention && FileCount > 0` → `DeleteTitle` (log fileCount).
  - Episodes: group by `SeriesID` → `SeriesTVDBIDs` → `TitleByExternalID("TVDB", …)` → `SeriesEpisodes(titleID)` → build `map[string]scEpisode` keyed by `epKey(season, episode)` after `strconv.Atoi` coercion (skip unparseable, debug log) → per watched ep: unmonitor if monitored; collect due-with-file → single `DeleteEpisodeFiles(titleID, ids)` per series.
  - Logging mirrors old style: `[dry-run] would …` lines, counts, labels `Name S%02dE%02d`.
- [ ] **Step 4: Run** `go test ./...` — pass. Also `go vet ./...`.
- [ ] **Step 5: Commit** `feat: cleaner acts on scryer (same watch→unmonitor→delete logic)`

### Task 5: Rename + docs + housekeeping

**Files:**
- Delete: `sonarr.go`, `radarr.go`, `req_test.go`
- Modify: `go.mod` (module line), `Dockerfile` (binary name ×3), `README.md`, `config.example.yaml`, `.github/workflows/ci.yml` (only if it hardcodes names — it doesn't; verify)

- [ ] **Step 1:** Delete the three files; fix any stragglers (`go build ./...` catches references).
- [ ] **Step 2:** `go.mod` module → `github.com/honeybyhomo/delete-watched-media`; Dockerfile build/copy/entrypoint → `delete-watched-media`.
- [ ] **Step 3:** Rewrite `README.md`: new name, Scryer instead of Sonarr/Radarr (keep positioning + why-not table, add "Why not Scryer maintenance rules?" row), env table (`JELLYFIN_API_KEY`, `SCRYER_API_KEY`), config example, docker/compose snippets with new image name, note the repo/image rename from `deletewatchedarr`. Update `config.example.yaml` to match Global Constraints.
- [ ] **Step 4:** `go vet ./... && go test ./... && go build ./...` — pass.
- [ ] **Step 5: Commit** `feat!: rename to delete-watched-media, target scryer (replaces sonarr/radarr)`

### Task 6: Live dry-run verification (from this container — no commit)

- [ ] **Step 1:** Build `./delete-watched-media`. Write `/tmp/opencode/dwm-config.yaml`: `jellyfin: {url: http://jellyfin:8096, username: admin}`, `scryer: {url: http://scryer:8080}`, `movie_retention_days: 2`, `episode_retention_days: 1`, `interval_minutes: 60`, `dry_run: true`, `log_level: debug`.
- [ ] **Step 2:** Run once with `JELLYFIN_API_KEY=$(cat /data/credentials/jellyfin-api.key) SCRYER_API_KEY=$(cat /data/credentials/scryer-api.key)` and a one-shot invocation (temporary env `DWM_ONCE=1` hack not needed — run with `timeout 30` and read first-cycle logs).
- [ ] **Step 3:** Verify log lines: watched counts > 0, `[dry-run] would delete …` lines for the items the user says are pending, `[dry-run] would unmonitor …` counts. Investigate anything unexpected before proceeding.

### Task 7: GitHub rename + ship

- [ ] **Step 1:** Try repo rename via GitHub API (`PATCH /repos/honeybyhomo/deletewatchedarr` `{"name":"delete-watched-media"}`) using the PAT from `~/.git-credentials`. If 403 → ask the user to rename in the UI (Settings → General); PAT access follows the repo automatically.
- [ ] **Step 2:** Update local remote URL; push `main`; tag `v0.2.0` and push it — release workflow publishes `ghcr.io/honeybyhomo/delete-watched-media:0.2.0` (+latest).
- [ ] **Step 3:** Verify workflow green and package present (GHCR package version API or workflow run conclusion).

### Task 8: docker-compose repo changes

**Files (all in `/projects/docker-compose`):**
- Create: `delete-watched-media/compose.yaml`, `delete-watched-media/.env`, `delete-watched-media/config.yaml`
- Delete: `arr-stack/deletewatchedarr.yaml`; remove the `deletewatchedarr` service block from `arr-stack/compose.yaml`
- Modify: `AGENTS.md` (drop the Sept deletewatchedarr reminder; update Scryer cutover bullet), `docs/superpowers/plans/2026-10-23-scryer-weaver-trial.md` (Phase 4 ledger entry)

- [ ] **Step 1:** `delete-watched-media/compose.yaml`: service `delete-watched-media`, image `ghcr.io/honeybyhomo/delete-watched-media:0.2.0` (pinned, house style), `user: "${PUID}:${PGID}"`, env `JELLYFIN_API_KEY`/`SCRYER_API_KEY` from Dockhand-secrect vars, volume `./config.yaml:/config.yaml:ro`, networks `traefik` only (single-homed — jellyfin + scryer are traefik members; avoids the multi-homing DNS quirk), `restart: unless-stopped`. No traefik labels (no web UI), no dataset.
- [ ] **Step 2:** `.env` with only referenced vars (super-secret placeholders); `config.yaml` = Task 6 config with `dry_run: true` initially.
- [ ] **Step 3:** Remove parked deletewatchedarr service + its config from arr-stack; validate both stacks with `docker compose config` (or yq if docker unavailable here); update AGENTS.md + trial ledger.
- [ ] **Step 4:** Commit + push to docker-compose main.

### Task 9: Deploy + go live

- [ ] **Step 1:** Mint dedicated key: `createMyApiKey(label: "delete-watched-media")` → save `/data/credentials/delete-watched-media-scryer.key` (mode 600); smoke-test it with a `titlesByExternalIds` query + one `deleteEpisodeFilesPreview`.
- [ ] **Step 2:** Add Dockhand secrets (`JELLYFIN_API_KEY` value from `/data/credentials/jellyfin-api.key`, `SCRYER_API_KEY` from the new key) + register the git stack + deploy, following the scryer/weaver stack precedent (SSH `root@10.10.10.11`, key `/data/.ssh/id_ed25519_truenas`).
- [ ] **Step 3:** Verify container `Up`, first cycle logs match Task 6 dry-run expectations (still dry-run).
- [ ] **Step 4:** Flip `config.yaml` `dry_run: false`, commit, push, redeploy. Watch one cycle: deletions logged, files gone from `/mnt/apple/media`, Scryer shows episodes unmonitored + no files, Jellyfin items disappear (LibraryMonitor / scryer refresh — answers the refresh question empirically).
- [ ] **Step 5:** Final ledger note in the trial plan doc; report results.
