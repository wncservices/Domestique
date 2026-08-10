# AGENTS.md

Single source of truth for agent behaviour in this repository. Tool-agnostic — `CLAUDE.md` is a pointer to this file.

## What this repo is

**Domestique** carries cycling routes to head units. Two riders share one route library; the
service reconciles it into each rider's Garmin Connect and Wahoo account, so a route added once
shows up on both a Garmin Edge and a Wahoo ELEMNT.

It is a monorepo, free software under the GNU AGPL-3.0, and **it contains no
route data**:

| Path | What |
|---|---|
| `apps/api/` | Go service — CLI (`validate`/`plan`/`push`/`state`/`import`) and HTTP API (`serve`) |
| `apps/web/` | Vue 3 + Vite + TypeScript frontend |
| `charts/domestique/` | The Helm chart, published as a Helm repo on GitHub Pages |
| `examples/routes/` | One sample route so the demo has something to show. Not a library |
| `docs/` | Design notes, including the research behind the provider choices |

Go module: `github.com/wncservices/domestique/apps/api`, wired through the root `go.work`.
npm workspace: `@domestique/web`, wired through the root `package.json`.

## Users, riders and accounts

Three words that are easy to confuse, and the distinction is the whole design:

- A **user** is a person who logs in. Users come from Authelia and are **never
  stored** — no table, no config. `Remote-User` says who, `Remote-Groups` says
  what they may do.
- A **rider** is that user's name as it appears on things they own. It is
  simply the Authelia username, copied at the moment they act.
- An **account** is a *connection to a head unit* — a Garmin Connect or Wahoo
  account, with a label and (once the adapters exist) a credential. Authelia
  knows nothing about anyone's Garmin login, so these cannot come from there.

Accounts live in the `accounts` table and are created by **riders linking their
own** through the UI. Nothing in the config file names them. Two rules hold
this together:

- **The rider comes from the session, never the request body.** Letting the
  body decide would let someone plant an account on another rider, or create
  one they cannot then unlink. An admin may link on someone's behalf, and only
  an admin.
- **One account per rider per provider**, which is what makes `provider:rider`
  a safe primary key. A duplicate would mean two rows claiming one device.

A route with no `targets` of its own goes to **every linked account** — the
useful default for a library two people share. Naming targets is what keeps a
private route off the other rider's head unit.

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

Riders sign in from the UI; `KOMOOT_EMAIL` / `KOMOOT_PASSWORD` are the
alternative, one shared account for the whole deployment, and a rider's own
connection wins over it.

**The password is never stored** — see *Provider sign-ins* below, which is the
same machinery Garmin uses.

Imported routes carry a `komoot:<id>` tag, which is how re-imports are
detected; without it a second import silently duplicates every route and the
rider cannot tell which copy their device follows.

## Provider sign-ins

Komoot and Garmin are both signed in to from the UI, and both go through
`internal/providerlink`: one table, `provider_links`, keyed on
`(provider, rider)`. One package rather than one per provider because the
rules below have to hold for all of them, and three copies is three places to
get them wrong. Wahoo joins it when its adapter lands.

**The password is never stored.** Signing in returns something reusable — a
Komoot session token, a Garmin OAuth1 token pair — so the password is used for
one request and discarded. What comes back is encrypted with
`DOMESTIQUE_ENCRYPTION_KEY` (`internal/secrets`, AES-256-GCM). `Secret` is
opaque to the store: Komoot keeps a token, Garmin keeps a JSON-encoded
`garmin.Session`. Four rules hold this together:

- **No key, no storing.** `providerlink.Store.CanStore` is false without one,
  `Save` refuses, and the UI does not offer the form. There is no path that
  writes a session in clear.
- **The rider comes from the session**, never the request body — same rule as
  linking a head unit.
- **Refuse before signing in.** With no key — or, for Garmin, no OAuth1
  consumer — the handler returns 412 without contacting the provider, so a
  password is not sent somewhere useless.
- **Reads for display never decrypt.** `Get` returns the connection; `Secret`
  is called only where the session is about to be used.

An expired or undecryptable session is not an error to work around: the rider
reconnects. That is why `Save` is an upsert.

Rows made before this table existed are copied from `komoot_links` on start,
sealed bytes and all, and only where nothing is already there — so a
reconnection is never overwritten by the fossil. The old table is left behind
deliberately, so rolling back to the previous image does not lose every Komoot
connection. Drop it once no deployment runs that image.

## Garmin sign-in

`internal/garmin` does the four-step handshake (CSRF page → credentials →
OAuth1 token → OAuth2 bearer); `api.LiveGarmin` is the seam between it and
the store. Three things are easy to get wrong:

- **Two-factor gets its own answer.** `ErrMFARequired` returns 409 with
  `"mfa": true`, not "wrong password" — this flow cannot complete a code
  challenge, and saying otherwise sends a rider round in circles. The UI keys
  off the flag, not off the message text.
- **The consumer pair is not in this repository.** `GARMIN_OAUTH_CONSUMER_KEY`
  and `_SECRET` are Connect's own OAuth1 consumer. Without them `Ready()`
  fails and the UI says the sign-in is unavailable rather than offering a form
  that cannot work. Do not hardcode them and do not fetch them at runtime from
  the bucket the Python reference implementation reads.
- **Signing in *is* linking the head unit.** The handler links the account
  too, and disconnecting unlinks it. A head unit with no session behind it is
  a push target that can only fail.

The profile lookup after sign-in is best-effort: it names the account and is
the first call that proves the OAuth1 token exchanges for a bearer, but an
undocumented endpoint moving must not fail a sign-in that otherwise worked.

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
push to `main` that touches it, as a Helm repository on GitHub Pages
(`https://wncservices.github.io/Domestique`). **That URL follows the
repository name**, capital D included — the lowercase one 404s. **Bump `version` in `Chart.yaml` for any chart
change**, or the release is skipped and the published chart silently lags the
repository.

### Two registries, one build

`image.yml` pushes the same build to GHCR and Docker Hub under identical tags.
One `build-push-action` invocation with two entries in `images:` — never two
builds, which would put two different digests behind the same tag.

GHCR is canonical: it is what the chart pulls and where the provenance
attestation is pushed. Docker Hub is a mirror and an **optional** one, wired to
an organisation-level variable and secret:

| Kind | Name | Value |
|---|---|---|
| Variable | `DOCKERHUB_USERNAME` | the Docker Hub namespace, e.g. `wilant` |
| Secret | `DOCKERHUB_TOKEN` | a Docker Hub access token with Read/Write |

The namespace is **derived from the variable**, not hardcoded — `docker.io/<var
lowercased>/domestique` — so moving the mirror to a Docker Hub organisation is
a variable change and nothing else. It is lowercased because an image reference
may not contain uppercase and the variable is free text somebody typed.

**GHCR is the one that has to work.** It is public, it has no pull rate
limit, the chart pulls from it and the attestation lives there. Docker Hub is
a convenience.

So: neither credential set means the mirror is off, which is a supported way
to run — a notice, and the build passes. **One set and the other missing
fails the build**, after the GHCR push so the image is not lost. That is
somebody meaning to configure it and it not taking, and it is invisible
otherwise: everything reports success while half the contract went unmet.

Two ways for the credentials not to arrive, and the second is the one that bit:

- they do not exist; or
- they exist at organisation level but their **Repository access** excludes
  this repo. An organisation secret defaults to *Private repositories*, and
  this repository is public, so it is handed nothing. Set the access to *All
  repositories*, or list this one explicitly.

`secrets` is not available to a step-level `if`, which is why the decision is
computed into an output by the `registries` step.

### GHCR packages default to private

A package published to GHCR for the first time is **private**, whatever the
visibility of the repository that published it. Both of ours started that way,
which means `docker pull` and `helm install` fail for everyone — including the
cluster, unless you fit an imagePullSecret.

There is no API for this. GitHub exposes package visibility only in the UI, so
it is a one-time manual step per package, under *Package settings → Danger Zone
→ Change visibility*:

- [image](https://github.com/orgs/wncservices/packages/container/domestique/settings)
- [chart](https://github.com/orgs/wncservices/packages/container/charts%2Fdomestique/settings)

Check it after adding any new published artifact. Nothing in CI will tell you;
the push succeeds and the pull is what fails, later, somewhere else.

Two defaults are deliberately unsafe-but-obvious rather than safe-but-silent:
authentication is `none` and the NOTES warn loudly about it, because a chart
that quietly required config nobody set would be worse. Same for persistence.

The chart renders the app's config file, so a values change can produce
something the app rejects. CI catches that by rendering every example under
`ci/` and running the real binary's `validate` against the result — keep that
step working when adding a value.

## The route library

`internal/source` is the library: routes as rows, the GPX as a blob, on
PostgreSQL or SQLite. One implementation, deliberately.

An earlier version could also read a directory of GPX files kept under git.
It read well on paper — review and history for free — but it was a second
storage model that could not do half of what the database one could: no
uploads, no Komoot import, nowhere to link a head unit, nowhere to keep sync
state. Every feature ended up asking which kind of library it was talking to,
and the answer decided whether the feature existed at all. Removing it deleted
more code than it added.

`domestique import --from <dir>` still loads a folder of `.gpx` files. That is a
one-off copy into the database, not a storage mode: nothing keeps reading the
folder afterwards.

**Never commit real routes to this repo.** GPX files are personal location
data. `examples/routes/` holds one synthetic route for the demo, and the
`.gitignore` blocks `/routes/` and `data/`.

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
just demo         # serve a local SQLite library with the example route loaded
just api          # run the API on :8080, serving apps/web/dist if built
just web          # Vite dev server on :5173, proxying /api to :8080
just import DIR   # load a folder of .gpx files into the database
just komoot ARGS  # list or import Komoot routes (needs KOMOOT_* env vars)
just fit SLUG     # write a route out as a FIT course for a real device
```

For UI work run `just api` and `just web` side by side and use :5173.

Everything above needs a local Go and Node. The container equivalents need only
Docker, and run against PostgreSQL rather than SQLite:

```bash
just up           # PostgreSQL + the app on :8080 (`down`, `reset`, `logs`)
just cli ARGS     # the CLI inside the running app, same database
just docker-test  # the Go suite, PostgreSQL cases included
just docker-check # gofmt, vet, go test, web typecheck
just docker-build # frontend bundle + a Linux binary in ./bin
```

`compose.yaml` gives the test suite its **own** database (`domestique_test`).
That is not tidiness: the suite drops and recreates its tables, so sharing one
database would make `just docker-test` silently wipe whatever `just up` is
holding.

## The name is Domestique; the identifiers are not

Capitalised wherever it is a name — prose, page title, UI heading, CLI banner,
GPX creator. Everything a machine parses stays lowercase, and has to:

- an OCI image reference and an npm package name may not contain uppercase;
- a Helm chart name becomes a Kubernetes object name (RFC 1123, lowercase);
- a Go module path with a capital is escaped as `!d` in the module cache.

So the image name in `image.yml` is **hardcoded** as
`ghcr.io/wncservices/domestique` rather than derived from
`${{ github.repository }}` — the repository is `wncservices/Domestique`, and
deriving it would produce a reference the registry rejects. Do not "simplify"
it back.

## Toolchain versions

Node and Go versions are pinned in three places that must agree: `.tool-versions`
(asdf/mise), `.nvmrc` (CI, via `node-version-file`), and the image tags in
`Dockerfile` and `compose.yaml`. Node tracks the **LTS** line, not Current.
Changing one and not the others is how CI and the image end up on different
majors.

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
- `internal/accounts` — the linked head units. See above.
- `internal/config` — `domestique.yaml`, deliberately small: where the database
  is, and how to recognise a user. No accounts, no targets.
- `internal/auth` — identity, roles and permissions. See above.
- `internal/fitcourse` — GPX to FIT course conversion. See above.
- `internal/komoot` — the undocumented Komoot client.
- `internal/source` — the route library. **PostgreSQL and SQLite**: `dbx` holds
  every place they differ, queries are written once with `?` and rebound per
  engine, and `TestEachEngine` is what stops one engine silently rotting.
- `internal/dbx` — which engine a DSN means, and the few places SQLite and
  PostgreSQL differ. Shared by the route source and the state store.
- `internal/state` — sync state, in a database table when the source is a
  database, otherwise a JSON file. **The reads return errors on purpose**:
  treating a failed read as "no state" re-pushes every route to every device,
  and panicking takes the server down, so the caller has to decide.
- `internal/sync` — the diff engine. Pure: give it routes, config and a store, get a plan.
- `internal/targets` — one adapter per provider. Adapters are dumb; the engine decides what to do.
- `internal/api` — JSON API plus the built SPA.

`model.Route` carries **no file paths**. A route is a row; fetch its track through the library.

Four rules the code already follows, worth keeping:

1. **One bad route never aborts a run.** A half-finished GPX export is reported as a problem and
   skipped; the other rider's routes still go out.
2. **The content hash ignores noise but not renames.** Sub-metre jitter and timestamps are not
   changes — otherwise re-exporting from a different planner churns every route — but the name
   feeds the hash, because the providers display it.
3. **The library is re-read on every request.** Another replica or an import
   can change it at any moment, so caching would mostly buy stale answers.

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
2. **`TestEachEngine` runs the behavioural suites against SQLite *and* PostgreSQL.**
   They differ in placeholders, the boolean column, the blob type and the upsert,
   so passing on one says nothing about the other — and PostgreSQL is what a
   deployment uses. CI runs a service container and fails if those tests *skip*,
   because a silently skipped engine reads green while covering nothing.

When you add behaviour, check the test fails without it. Several tests here were written after
confirming that deleting the code they cover turns them red.

## Provider adapters — read before touching

Both adapters are deliberate stubs. `targets.Implemented` returns false for both, which is what
makes the UI say "not wired up" instead of offering a push that always fails. Flip a provider's
entry only when its adapter genuinely works.

- **Garmin (Phase 3)** — there is no self-serve API. The official Courses API is Connect
  Developer Program only (commercial partners), so this uses the unofficial Connect web session.
  The SSO handshake, the course upload (`course-service`, the call *Training → Courses → Import*
  makes) and the per-rider sign-in all exist; what is missing is the `targets.Target` adapter
  wiring them to the library, which is why `targets.Implemented` still says false. Grey-area and
  breakable — fine for two personal accounts, not for anything shared more widely.
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
