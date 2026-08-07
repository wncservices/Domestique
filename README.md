# domestique

> The rider who fetches the bottles so the others can just ride.

Two riders, two different head units, one shared set of routes. domestique keeps a route library
in sync with each rider's Garmin Connect and Wahoo account, so a route added once shows up on a
Garmin Edge *and* a Wahoo ELEMNT.

Source-available under [PolyForm Noncommercial 1.0.0](LICENSE): use it, change it and share it
freely for anything non-commercial — personal riding, hobby projects, research, clubs. Commercial
use is not permitted. This is deliberately **not** an open source license.

**This repository holds no route data** — see [Where routes live](#where-routes-live).

## Status

Early. The library, diff engine, CLI, HTTP API and web UI work end to end. **The provider
adapters are stubs** — see [Roadmap](#roadmap). Today you can add routes, browse them, and see
exactly what would be pushed where; you cannot yet actually push.

## Where routes live

GPX files are personal location data — a route usually starts at somebody's front door. So the
app is generic and open source, and the routes live somewhere you control. Pick a source:

**`fs` — a directory of GPX files.** Point it at a checkout of a separate, private routes repo.
Read-only: routes are added by committing them, so you get review, history and blame for free.

```
routes/                          # a different repository, private
  wilant/kemmelberg-loop/
    route.gpx
    route.yaml                   # name, description, targets, tags (optional)
```

**`db` — GPX blobs in a SQLite database.** Riders upload through the web UI. No git, no
checkout; the database is the library, and the write endpoints appear automatically.

Switch with one line of config, or `--source fs|db` on the CLI. Moving from one to the other is
one command:

```bash
just import ../routes
```

## Quick start

```bash
just install
just build
just demo          # serves the bundled example routes, read-only
```

Then open <http://localhost:8080>. To try uploads instead:

```bash
just demo-db
```

For frontend work run `just api` and `just web` side by side and use the Vite server on :5173.

Without the UI:

```bash
just validate                    # read the source, report problems
just plan                        # what would change on each account
just push -- --dry-run           # same, in push's own words
```

## Logging in

domestique has no login of its own. Put it behind Traefik with an Authelia
forwardAuth middleware and it reads the identity Authelia passes down.

```yaml
auth:
  mode: proxy
  trusted_proxies: [10.42.0.0/16]
  roles:
    admin: [domestique-admins]
    rider: [cyclists]
    viewer: [guests]
```

| Role | Can |
|---|---|
| `viewer` | read routes, download GPX, see what would be pushed |
| `rider` | + upload, import from Komoot, push to devices, edit and delete **their own** routes |
| `admin` | + edit and delete **anyone's** routes |

> **The app must not be reachable except through the proxy.** With `mode: proxy`
> it believes the `Remote-User` header — and so would anyone who can talk to it
> directly. `trusted_proxies` narrows that; leave it empty only for a
> ClusterIP-only service.

With `mode: none` (the default) there is no login at all and every visitor is
an admin. That is right for a laptop and wrong for anything else; the UI says
so in the header.

## Importing from Komoot

```yaml
komoot:
  enabled: true
```

with `KOMOOT_EMAIL` and `KOMOOT_PASSWORD` in the environment. Then pick tours
in the web UI, or:

```bash
just komoot list
just komoot import          # everything not already here
```

Komoot has **no public API**. This uses the same undocumented endpoints their
apps do, so it will break from time to time — treat it as a convenience, not a
dependency. Already-imported tours are skipped, so running it twice is safe.

## Configuration

Copy `domestique.example.yaml` to `domestique.yaml`. It holds the route source and the riders'
accounts — **account ids and labels only, never a credential**. Provider credentials come from
the environment; in a cluster, Vault → ExternalSecret → `envFrom`.

A route is pushed to the accounts it names in `targets`, or to `default_targets` when it names
none. That is what keeps one rider's private routes off the other's head unit.

## Layout

| Path | What |
|---|---|
| `apps/api/` | Go service: CLI and HTTP API |
| `apps/web/` | Vue 3 + Vite frontend |
| `examples/routes/` | A sample route, so the demo has something to show |
| `docs/plan.md` | Why the providers work the way they do — read before touching an adapter |

## Roadmap

| Phase | What | Status |
|---|---|---|
| 1 | Library, diff engine, CLI, API, web UI, pluggable sources | ✅ |
| 2 | GPX → FIT course conversion, ideally with turn cues | ⬜ blocks Phase 4 |
| 3 | Garmin push (unofficial Connect session) | ⬜ stub |
| 4 | Wahoo push (Cloud API) | ⬜ stub, needs approved API access |
| 5 | Deploy: container, scheduled reconcile, Vault-backed tokens | ⬜ |
| 6 | Metrics + staleness alerting | ⬜ |

Phases 2–4 are where this succeeds or fails. Neither provider offers a self-serve route API:
Garmin has none at all, and Wahoo's is approval-gated and wants FIT rather than GPX.
`docs/plan.md` has the detail, including what to do if Wahoo says no.

## Contributing

`just check` runs the typecheck, vet and tests. Keep the Go side close to the standard library —
the dependencies today are a YAML parser and a pure-Go SQLite driver, and that is the budget.
