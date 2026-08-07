# AGENTS.md

Single source of truth for agent behaviour in this repository. Tool-agnostic — `CLAUDE.md` is a pointer to this file.

## What this repo is

**domestique** carries cycling routes to head units. Two riders share one route library; the
service reconciles it into each rider's Garmin Connect and Wahoo account, so a route added once
shows up on both a Garmin Edge and a Wahoo ELEMNT.

It is a monorepo, source-available under PolyForm Noncommercial 1.0.0, and **it contains no
route data**:

| Path | What |
|---|---|
| `apps/api/` | Go service — CLI (`validate`/`plan`/`push`/`state`/`import`) and HTTP API (`serve`) |
| `apps/web/` | Vue 3 + Vite + TypeScript frontend |
| `examples/routes/` | One sample route so the demo has something to show. Not a library |
| `docs/` | Design notes, including the research behind the provider choices |

Go module: `github.com/wncservices/domestique/apps/api`, wired through the root `go.work`.
npm workspace: `@domestique/web`, wired through the root `package.json`.

## The source split — the thing to not undo

The app is public; routes are personal location data. They are kept apart by
`internal/source`, and that separation is load-bearing:

- **`source.FS`** reads a directory of GPX files — typically a checkout of a *separate, private*
  routes repo. It is deliberately **read-only**: in a git-backed library, adding a route is a
  commit, which is where review and history come from. Do not add write methods to it.
- **`source.DB`** stores GPX blobs in SQLite and accepts uploads through the UI.

`source.AsWritable` is the only thing that decides whether write endpoints do anything. The
write routes are **always registered**, and answer 405 with an explanation on a read-only
source — leaving them unregistered let the SPA fallback answer `200` with HTML, which no client
can interpret. There is a test for this; keep it passing.

Never commit real routes to this repo, and never widen `examples/routes/` into a library. The
`.gitignore` blocks `/routes/` and `data/` for that reason.

## Security guardrails

This repository is **public**. Everything below assumes a reader who is not you.

- **Never commit a credential.** No Garmin passwords, Wahoo client secrets, OAuth tokens or
  refresh tokens in `domestique.yaml`, in any source file, or in a test fixture — including
  "temporary" values and things that look like placeholders.
- Credentials come from the environment. In the cluster they arrive by
  **Vault → ExternalSecret → K8s Secret → `envFrom`**, from `kv2_tooling/domestique/env`.
- Refreshed OAuth tokens must be written back to Vault with a `PushSecret`, not to a file in the
  repo — otherwise a pod restart breaks the refresh chain.
- `domestique.yaml` holds account **ids and labels only**, and is gitignored anyway;
  `domestique.example.yaml` is the committed template.
- If a credential is ever committed, rotate it. Removing the commit is not enough.
- **GPX files are personal location data** — a route usually starts at somebody's front door.
  They belong in a private source, never in this repo. `examples/routes/` holds one synthetic
  route and must stay that way.

## Commands

```bash
just install      # npm install + seed domestique.yaml (Go deps come from go.work)
just check        # typecheck + vet + go test — run before pushing
just test         # go test ./apps/api/...
just build        # frontend then binary
just demo         # serve the bundled example routes, read-only
just demo-db      # serve a local SQLite library, uploads enabled
just api          # run the API on :8080, serving apps/web/dist if built
just web          # Vite dev server on :5173, proxying /api to :8080
just import DIR   # copy a directory library into the database
```

For UI work run `just api` and `just web` side by side and use :5173.

## Architecture

Desired state is whatever the source offers; observed state is a JSON file recording what each
remote account holds. Everything else is a diff:

```
source (fs | db) ──List──> []model.Route ─┐
                                          ├──> sync.BuildPlan ──> model.Plan ──> sync.Apply ──> targets
state file ──────Open────> state.Store ───┘
```

- `internal/gpx` — parses GPX with the stdlib `encoding/xml` (no dependency), derives
  distance/ascent/start point, computes the content hash.
- `internal/config` — `domestique.yaml`: accounts, default targets, which source to use. Separate
  from the routes on purpose, since a DB source has no config file of its own.
- `internal/source` — where routes come from. See the split above.
- `internal/state` — a JSON file behind a `Store` interface, the seam for SQLite or Postgres.
- `internal/sync` — the diff engine. Pure: give it routes, config and a store, get a plan.
- `internal/targets` — one adapter per provider. Adapters are dumb; the engine decides what to do.
- `internal/api` — JSON API plus the built SPA.

`model.Route` carries **no file paths**: a route may be a directory or a database row, and only
its source knows which. Fetch the track through the source.

Four rules the code already follows, worth keeping:

1. **One bad route never aborts a run.** A half-finished GPX export is reported as a problem and
   skipped; the other rider's routes still go out.
2. **The content hash ignores noise but not renames.** Sub-metre jitter and timestamps are not
   changes — otherwise re-exporting from a different planner churns every route — but the name
   feeds the hash, because the providers display it.
3. **The source is re-read on every request.** A git pull can change it at any moment, so caching
   would mostly buy stale answers.
4. **Slugs come from URLs.** The FS source rejects anything that escapes the library root. There
   is a test; keep it passing.

## Tests

Every backend package has tests, and `go test ./apps/api/...` is the gate.

- **Acceptance** — `internal/api/acceptance_test.go` (package `api_test`) drives every endpoint
  over real HTTP against a real `httptest` server, in **both** source modes: status codes,
  headers, JSON shapes, and full lifecycles (upload → plan → push → rename → delete → plan empty).
  `cmd/domestique/main_test.go` does the same for every CLI command against real files.
- **Unit** — alongside the code they cover.

Two things make the suite worth trusting, and both are easy to lose:

1. **`Server.TargetFactory`** exists so acceptance tests can substitute fake adapters. The real
   ones are stubs that always error, so without it no successful push is reachable and the entire
   create/update/delete path would be untested. Do not inline `targets.Build` back into `handlePush`.
2. **Traversal tests must plant a real file outside the library root.** The FS source appends
   `route.gpx` to a slug, so `../../etc/passwd` never names an existing file and 404s *even with
   the guard removed* — a test written that way passes for the wrong reason. `TestFSRefusesPathTraversal`
   creates a readable `secret/route.gpx` beside the library precisely so the guard is what fails it.

When you add behaviour, check the test fails without it. Several tests here were written after
confirming that deleting the code they cover turns them red.

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

- Go: standard library first. The dependencies are `gopkg.in/yaml.v3` and `modernc.org/sqlite`
  (pure Go, so no cgo and the image stays small). That is the budget — add to it only for
  something genuinely hard, like FIT encoding.
- `gofmt` is the formatter; `go vet` must be clean.
- Vue: `<script setup lang="ts">`, no state-management library — the app is one screen and
  `ref`/`computed` cover it. No CSS framework; theme tokens are CSS custom properties in
  `src/styles.css` and the UI follows the OS light/dark preference.
- The frontend has no map library on purpose: route previews are inline SVG drawn from the
  coordinates the API returns, so nothing calls out to a tile server.
- DTOs in `apps/api/internal/api/server.go` and `apps/web/src/api/types.ts` mirror each other by
  hand. Change them together.
- Comments explain *why*. The code already says what.
