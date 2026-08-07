# domestique

> The rider who fetches the bottles so the others can just ride.

A shared cycling route library that carries routes to every head unit. Two riders commit GPX
routes to one git repo; domestique reconciles that library into each rider's Garmin Connect and
Wahoo account, so a route added once shows up on a Garmin Edge *and* a Wahoo ELEMNT.

## Status

Early. The library, diff engine, CLI, HTTP API and web UI work end to end. **The provider
adapters are stubs** — see [Roadmap](#roadmap). Today you can add routes, see exactly what would
be pushed where, and watch sync state; you cannot yet actually push.

## How it works

The git repo is the source of truth. Add a route by committing it:

```
routes/
  library.yaml                    # accounts and default targets
  wilant/kemmelberg-loop/
    route.gpx                     # the track
    route.yaml                    # name, description, targets, tags (optional)
```

domestique reads that, compares it against what each account is recorded as holding, and
reconciles the difference: new routes are created, edited routes updated, deleted routes removed.
Re-running changes nothing — the plan is empty when everything is in sync.

A route with no `route.yaml` still works; it is named after its directory and goes to the
library's `default_targets`.

## Quick start

```bash
just install
just build
just api
```

Then open <http://localhost:8080>. For frontend work run `just api` and `just web` side by side
and use the Vite server on :5173 instead.

Without the UI:

```bash
just validate                     # parse the library, report problems
just plan                         # what would change on each account
just push -- --dry-run            # same, in push's own words
```

## Layout

| Path | What |
|---|---|
| `apps/api/` | Go service: CLI and HTTP API |
| `apps/web/` | Vue 3 + Vite frontend |
| `routes/` | The route library |
| `docs/plan.md` | Why the providers work the way they do — read before touching an adapter |

## Roadmap

| Phase | What | Status |
|---|---|---|
| 1 | Library, diff engine, CLI, API, web UI | ✅ |
| 2 | GPX → FIT course conversion, ideally with turn cues | ⬜ blocks Phase 4 |
| 3 | Garmin push (unofficial Connect session) | ⬜ stub |
| 4 | Wahoo push (Cloud API) | ⬜ stub, needs approved API access |
| 5 | Deploy: Helm chart, ArgoCD, Vault-backed tokens, scheduled reconcile | ⬜ |
| 6 | Metrics + staleness alerting | ⬜ |

Phases 2–4 are where this succeeds or fails. Neither provider offers a self-serve route API:
Garmin has none at all, and Wahoo's is approval-gated and wants FIT rather than GPX.
`docs/plan.md` has the detail, including what to do if Wahoo says no.

## A note on privacy

GPX files are personal location data — a route usually starts at somebody's front door. Keep the
repo private, and keep credentials out of it entirely: they come from the environment, sourced
from Vault. See `AGENTS.md`.
