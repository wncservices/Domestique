# AGENTS.md

Single source of truth for agent behaviour in this repository. Tool-agnostic — `CLAUDE.md` is a pointer to this file.

## What this repo is

**domestique** carries cycling routes to head units. Two riders share one git-tracked library of
GPX routes; the service reconciles that library into each rider's Garmin Connect and Wahoo
account, so a route committed once shows up on both a Garmin Edge and a Wahoo ELEMNT.

It is a monorepo:

| Path | What |
|---|---|
| `apps/api/` | Go service — CLI (`validate`/`plan`/`push`/`state`) and HTTP API (`serve`) |
| `apps/web/` | Vue 3 + Vite + TypeScript frontend |
| `routes/` | The route library — GPX files and their metadata. This is the source of truth |
| `docs/` | Design notes, including the research behind the provider choices |

Go module: `github.com/wncservices/domestique/apps/api`, wired through the root `go.work`.
npm workspace: `@domestique/web`, wired through the root `package.json`.

## Security guardrails

- **Never commit a credential.** No Garmin passwords, Wahoo client secrets, OAuth tokens or
  refresh tokens in `routes/library.yaml`, in any source file, or in a test fixture — including
  "temporary" values and things that look like placeholders.
- Credentials come from the environment. In the cluster they arrive by
  **Vault → ExternalSecret → K8s Secret → `envFrom`**, from `kv2_tooling/domestique/env`.
- Refreshed OAuth tokens must be written back to Vault with a `PushSecret`, not to a file in the
  repo — otherwise a pod restart breaks the refresh chain.
- `routes/library.yaml` holds account **ids and labels only**. It is committed; secrets are not.
- If a credential is ever committed, rotate it. Removing the commit is not enough.
- GPX files are personal location data: a route usually starts at somebody's front door. Keep the
  repo private and think before adding a route to a public example.

## Commands

```bash
just install      # npm install (Go deps come from go.work)
just check        # typecheck + vet + go test — run before pushing
just test         # go test ./apps/api/...
just build        # frontend then binary
just api          # run the API on :8080, serving apps/web/dist if built
just web          # Vite dev server on :5173, proxying /api to :8080
```

For UI work run `just api` and `just web` side by side and use :5173.

## Architecture

Desired state is the library on disk; observed state is a JSON file recording what each remote
account holds. Everything else is a diff:

```
routes/ ──load──> library.Library ─┐
                                   ├──> sync.BuildPlan ──> model.Plan ──> sync.Apply ──> targets
state file ──load──> state.Store ──┘
```

- `internal/library` — reads `library.yaml` and each route directory; parses GPX with the stdlib
  `encoding/xml` (no dependency), derives distance/ascent/start point, computes a content hash.
- `internal/state` — a JSON file behind a `Store` interface. That interface is the seam to swap
  in SQLite or Postgres; two riders do not need either yet.
- `internal/sync` — the diff engine. Pure: give it a library and a store, get a plan.
- `internal/targets` — one adapter per provider. Adapters are dumb; the engine decides what to do.
- `internal/api` — JSON API plus the built SPA.

Three rules the code already follows, worth keeping:

1. **One bad route never aborts a run.** A half-finished GPX export is reported as a problem and
   skipped; the other rider's routes still go out.
2. **The content hash ignores noise.** Sub-metre coordinate jitter and timestamps do not count as
   changes, or re-exporting from a different planner would churn every route.
3. **The library is re-read on every request.** A git pull can change it at any moment, so
   caching would mostly buy stale answers.

## Provider adapters — read before touching

Both adapters are deliberate stubs. `targets.Implemented` returns false for both, which is what
makes the UI say "not wired up" instead of offering a push that always fails. Flip a provider's
entry only when its adapter genuinely works.

- **Garmin (Phase 3)** — there is no self-serve API. The official Courses API is Connect
  Developer Program only (commercial partners). The planned path is the unofficial Connect web
  session: the Garmin SSO handshake plus the call the *Training → Courses → Import* button makes.
  Confirm the `course-service` path with devtools first; it is undocumented and moves. Grey-area
  and breakable — fine for two personal accounts, not for anything shared more widely.
- **Wahoo (Phase 4)** — the Cloud API is documented and clean, but access is approval-gated and
  `POST /v1/routes` takes a **base64 FIT file, not GPX**. Requesting API access is the long pole.

**GPX → FIT (Phase 2) blocks Wahoo entirely** and decides navigation quality on both: a naive
conversion navigates as a breadcrumb line with no turn cues. See `docs/plan.md`.

## Conventions

- Go: standard library first. The only dependency is `gopkg.in/yaml.v3`, and it should stay that
  way unless something genuinely hard (FIT encoding) needs help.
- `gofmt` is the formatter; `go vet` must be clean.
- Vue: `<script setup lang="ts">`, no state-management library — the app is one screen and
  `ref`/`computed` cover it. No CSS framework; theme tokens are CSS custom properties in
  `src/styles.css` and the UI follows the OS light/dark preference.
- The frontend has no map library on purpose: route previews are inline SVG drawn from the
  coordinates the API returns, so nothing calls out to a tile server.
- DTOs in `apps/api/internal/api/server.go` and `apps/web/src/api/types.ts` mirror each other by
  hand. Change them together.
- Comments explain *why*. The code already says what.
