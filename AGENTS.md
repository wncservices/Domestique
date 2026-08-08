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
| `charts/domestique/` | The Helm chart, published to GHCR and GitHub Pages on every change |
| `examples/routes/` | One sample route so the demo has something to show. Not a library |
| `docs/` | Design notes, including the research behind the provider choices |

Go module: `github.com/wncservices/domestique/apps/api`, wired through the root `go.work`.
npm workspace: `@domestique/web`, wired through the root `package.json`.

## Authentication and roles

The app authenticates nobody. It sits behind Traefik with an Authelia
forwardAuth middleware and reads `Remote-User` / `Remote-Groups` /
`Remote-Name` / `Remote-Email`. `internal/auth` owns all of it.

**The entire scheme rests on the app being unreachable except through the
proxy.** A browser can set `Remote-User` as easily as Traefik can. Two things
protect that, and both must stay:

1. Header trust is opt-in — `auth.mode` must be `proxy`. The default is `none`,
   which ignores the headers completely and treats everyone as a local admin.
2. `auth.trusted_proxies` discards headers from any other peer. Leave it empty
   only when the service is genuinely unreachable (ClusterIP-only).

Roles come from Authelia groups, most-privileged match wins:

| Role | Can |
|---|---|
| `viewer` | read routes, download GPX, see the plan |
| `rider` | + upload, Komoot import, push, edit/delete **their own** routes |
| `admin` | + edit/delete **anyone's** routes |

Two rules that are easy to break:

- **Ownership comes from the session, never the form.** `handleUpload` ignores
  the `uploadedBy` field when someone is authenticated. Trusting it would let a
  rider upload as somebody else and put the route beyond their own reach.
- **An unknown permission denies.** `Role.Can` returns false for anything not
  in `minimumRole`, so a typo in a handler cannot open a hole.

The frontend mirrors these rules to decide what to *show*. That is a courtesy,
not a control — the server enforces, the UI only avoids offering buttons that
would 403.

## Komoot

`internal/komoot` speaks Komoot's undocumented v006/v007 API: there is no
public one. Expect it to break; Komoot changed hands in 2025. Failures are
contained — the API returns 502 and the rest of the app carries on.

Credentials come from `KOMOOT_EMAIL` / `KOMOOT_PASSWORD` in the environment,
never the config file. Imported routes carry a `komoot:<id>` tag, which is how
re-imports are detected; without it a second import silently duplicates every
route and the rider cannot tell which copy their device follows.

## FIT courses

`internal/fitcourse` turns a GPX track into a Garmin FIT course. It exists
because **Wahoo's API will not accept GPX at all** — `POST /v1/routes` takes a
base64 FIT — and because a FIT course can carry turn cues where a GPX gives the
device only a breadcrumb line.

Encoding is delegated to `github.com/muktihari/fit`. FIT is a binary format
with definition messages, scaled fields and a CRC; this is the "genuinely hard"
case the dependency budget is for.

**Turn cues are inferred, and off by default.** `DeriveTurns` knows nothing
about roads — only that the line bends. It reports a hairpin on an open road
and stays quiet through a junction taken as a gentle curve. When a route comes
from a planner that knows the road network (Komoot, RideWithGPS), that
planner's own cues are better and should win. Two details that took a bug to
find:

- Cues are placed at the **apex** of a bend, not the first point over the
  threshold. The heading is measured over a window, so on the approach that
  window already spans part of the corner and reports roughly half the true
  angle — which put the cue short of the junction and classified hairpins as
  ordinary turns.
- `DeriveTurns` indexes the distances slice it is handed. Use `Turns(points)`
  unless the distances are already computed; passing a nil slice used to panic.

Tests check the output two ways: a round trip through the library, and the
header and CRC checked against the FIT spec with an independent implementation,
so a bug in the library cannot pass unnoticed. **Neither proves a real device
accepts the file** — `domestique fit <slug>` writes one out for exactly that.

## The Helm chart

`charts/domestique` is published by `.github/workflows/chart-release.yml` on any
push to `main` that touches it — to GHCR as an OCI artifact and to the Helm
repository on GitHub Pages. **Bump `version` in `Chart.yaml` for any chart
change**, or the release is skipped and the published chart silently lags the
repository.

Two defaults are deliberately unsafe-but-obvious rather than safe-but-silent:
authentication is `none` and the NOTES warn loudly about it, because a chart
that quietly required config nobody set would be worse. Same for persistence.

The chart renders the app's config file, so a values change can produce
something the app rejects. CI catches that by rendering every example under
`ci/` and running the real binary's `validate` against the result — keep that
step working when adding a value.

## The source split — the thing to not undo

The app is public; routes are personal location data. They are kept apart by
`internal/source`, and that separation is load-bearing:

- **`source.DB`** is the default: routes as rows, the GPX as a blob, uploads through the UI.
  It speaks **PostgreSQL and SQLite** — PostgreSQL is what the cluster runs, SQLite is for a
  laptop or a single container. `dialect.go` holds every place they differ (placeholders, the
  boolean column, the blob type); queries are written once with `?` and rebound per engine.
  Anything touching SQL must keep working on both, and `TestEachEngine` is what enforces that.
- **`source.FS`** reads a directory of GPX files — typically a checkout of a *separate, private*
  routes repo. It is deliberately **read-only**: in a git-backed library, adding a route is a
  commit, which is where review and history come from. Do not add write methods to it.

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
just komoot ARGS  # list or import Komoot routes (needs KOMOOT_* env vars)
just fit SLUG     # write a route out as a FIT course for a real device
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
- `internal/auth` — identity, roles and permissions. See above.
- `internal/fitcourse` — GPX to FIT course conversion. See above.
- `internal/komoot` — the undocumented Komoot client.
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

- Go: standard library first. The dependencies are `gopkg.in/yaml.v3`, `modernc.org/sqlite`
  (pure Go, so no cgo), `github.com/jackc/pgx/v5` and `github.com/muktihari/fit`. That is the
  budget — add to it only for something genuinely hard, as FIT encoding was.
- `gofmt` is the formatter; `go vet` must be clean.
- Vue: `<script setup lang="ts">`, no state-management library — the app is one screen and
  `ref`/`computed` cover it.
- **UI is [Nuxt UI](https://ui.nuxt.com) (MIT), used standalone in Vite.** Its components are
  auto-imported by the Vite plugin, so `UCard`/`UButton`/`UTable` need no import statement.
  Reach for a Nuxt UI component before hand-rolling one; the point of adopting it was to stop
  hand-rolling buttons and dialogs.
  - PrimeVue is **not** an option: from v5 it needs a licence key with annual renewal, and v4 is
    frozen at the last MIT release.
  - Theme tokens live in `src/styles.css` (`--color-primary-*`). Use Nuxt UI's semantic classes
    (`text-muted`, `text-highlighted`, `bg-elevated`) rather than raw Tailwind colours, so light
    and dark both work without a second thought.
  - `src/color-mode.ts` puts the `dark` class on `<html>` from the OS preference. Outside Nuxt
    nothing does this for you, and without it the app is permanently light.
  - `App.vue` must stay wrapped in `<UApp>` — toasts, tooltips and modals need it.
  - `vue-router` is a dependency only because Nuxt UI's link components import it
    unconditionally. The app is still one screen; do not build routing on it without a reason.
- The frontend has no map library on purpose: route previews are inline SVG drawn from the
  coordinates the API returns, so nothing calls out to a tile server with somebody's home
  address in the request.
- DTOs in `apps/api/internal/api/server.go` and `apps/web/src/api/types.ts` mirror each other by
  hand. Change them together.
- Comments explain *why*. The code already says what.
