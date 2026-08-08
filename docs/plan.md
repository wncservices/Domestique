# domestique — design and plan

Two riders, two different head units, one shared set of routes. This is what
was built, why, and what is left.

The original version of this document was research: three options weighed
before any code existed. That research is preserved in
[Appendix: the provider research](#appendix-the-provider-research), because the
constraints it found still govern everything. The plan itself has moved on.

## Where routes live

**A database, by default.** Routes are rows; the GPX itself is a blob in the
row. Riders upload through the web UI or import from Komoot, and nothing needs
a git checkout.

Two engines are supported, for two different jobs:

| Engine | For |
|---|---|
| **PostgreSQL** | The deployed instance. The cluster already runs a CNPG PostgreSQL, so this is one more database rather than a volume |
| **SQLite** | A laptop, or a single container with a volume. A file, no server |

The DSN chooses: a `postgres://` URL (or a `host=… dbname=…` string) means
PostgreSQL, anything else is a SQLite file path. `internal/source/dialect.go`
holds the handful of places they disagree — placeholders, the boolean column,
the blob type — and every query is written once against both.

**A directory of GPX files is still supported**, and is not deprecated. Point
the `fs` source at a checkout of a separate, private routes repo and routes
arrive by commit, with review and history for free. It is deliberately
read-only: in a git-backed library, adding a route *is* the commit.

This started as the git-first design. It changed because the friction was in
the wrong place — asking someone to clone a repo and commit a file to add a
route they just plotted on their phone is a worse trade than losing git
history. `domestique import --from <dir>` moves an existing directory library
into a database one.

Whichever source is in use, **this repository holds no route data**. A GPX
usually starts at somebody's front door.

## How the sync works

Desired state is whatever the source offers; observed state is what each remote
account is recorded as holding. Everything else is a diff.

```
source (db | fs) ──List──> []model.Route ─┐
                                          ├──> BuildPlan ──> Plan ──> Apply ──> targets
state ────────────Open───> state.Store ───┘
```

A route is pushed to the accounts it names in `targets`, or to the library's
`default_targets`. That is what keeps one rider's private routes off the
other's head unit.

A content hash decides what changed. It ignores sub-metre coordinate jitter and
timestamps — otherwise re-exporting the same route from a different planner
churns everything — but includes the name, because the providers display it.

**Sync state lives in the database**, in a `sync_state` table beside the routes.
A deployment therefore needs a database and nothing else — no volume, no file.
The JSON file store still exists for a directory-backed library, which has no
database to borrow.

## Who can do what

domestique authenticates nobody. It sits behind Traefik with an Authelia
forwardAuth middleware and reads the identity Authelia passes down. Roles come
from Authelia groups:

| Role | Can |
|---|---|
| `viewer` | read routes, download GPX and FIT, see the plan |
| `rider` | + upload, import from Komoot, push, edit and delete **their own** routes |
| `admin` | + edit and delete **anyone's** routes |

The scheme rests on one assumption: the app is unreachable except through the
proxy. A browser can set `Remote-User` as easily as Traefik can. Header trust is
therefore opt-in (`auth.mode: proxy`, default `none`), and `trusted_proxies`
discards headers from any other peer.

## Getting routes in

**Upload.** Drag a `.gpx` onto the web UI.

**Komoot.** There is no public Komoot API; this speaks the same undocumented
endpoints their apps use, so expect it to break — Komoot changed hands in 2025.
Failures are contained: the endpoint returns 502 and the rest of the app carries
on. Imported routes carry a `komoot:<id>` tag so re-imports are skipped rather
than silently duplicated.

**A git repo**, via the `fs` source, as above.

## Getting routes out

**FIT conversion works.** `internal/fitcourse` renders a track as a Garmin FIT
course: `file_id`, `course`, `lap`, and a record per point, with optional turn
cues. This was the blocker for Wahoo — their API takes a base64 FIT and will not
accept GPX at all — and it improves Garmin too, where a bare GPX navigates as a
breadcrumb line with nothing said at junctions.

Turn cues are **inferred from the track's geometry** and off by default. The
heuristic knows nothing about roads: it calls a hairpin on an open road and
stays quiet through a junction taken as a gentle curve. A planner that knows the
road network does better, and its cues should win when a route comes from one.

Until the adapters land, `domestique fit <slug>` and `GET /api/fit/<slug>` write
a course out to copy onto a device over USB. That is also the only way to prove
the conversion: no test can establish that a real head unit accepts the file.

## What is left

| Phase | What | Status |
|---|---|---|
| 1 | Library, diff engine, CLI, API, web UI, pluggable sources | ✅ |
| 1b | Database sources (PostgreSQL and SQLite), uploads, Authelia login with roles, Komoot import | ✅ |
| 1c | Sync state in the database, so a deployment needs no volume | ✅ |
| 2 | GPX → FIT course conversion, with inferred turn cues | ✅ |
| 3 | Garmin push | ⬜ stub |
| 4 | Wahoo push | ⬜ stub, **blocked** on API access |
| 5 | Deploy: Helm chart ✅, ArgoCD, Vault-backed credentials, scheduled reconcile | 🟡 |
| 6 | Metrics and staleness alerting | ⬜ |

### Phase 3 — Garmin

There is no self-serve Garmin API. The official Courses API is Connect
Developer Program only, for commercial partners. The workable path is the
unofficial Connect web session: the SSO handshake, then the call the
*Training → Courses → Import* button makes. Confirm the `course-service` path
with devtools first; it is undocumented and moves.

Grey-area and breakable. Acceptable for two personal accounts, not for anything
shared more widely. Tokens last roughly a year, then need a manual re-auth —
which should surface as a metric, not as a surprise at the start of a ride.

### Phase 4 — Wahoo

The API is documented and clean. Access is not: it is approval-gated, with no
self-serve client id. **Requesting that key is the long pole and nothing else
unblocks it.** Everything on our side is ready — the endpoints are known, and
FIT conversion now exists.

If access is refused, the fallback is the ELEMNT companion app importing a `.fit`
from the phone's share sheet, or linking the Wahoo account to Strava/RWGPS and
letting their native sync carry it.

### Phase 5 — deployment

A hand-written Helm chart in the lab repo, following the house pattern: chart
folder, `helm-generator` ApplicationSet at wave 1, namespace in
`tooling-projects.yaml`, Vault registration in `applications.tf`.

Specifics this app needs:

- **PostgreSQL** from the existing CNPG cluster: a `Database` CRD in the app's
  `templates/` with `namespace: postgres-cluster`, per the house rule about
  cross-namespace resources.
- **Credentials from Vault** at `kv2_tooling/domestique/env`: the Komoot login,
  and later the Garmin and Wahoo credentials. Refreshed OAuth tokens must be
  written back with a `PushSecret`, or a pod restart breaks the refresh chain.
- **Authelia forwardAuth** on the IngressRoute, and `auth.mode: proxy` with
  `trusted_proxies` set to the pod CIDR. Getting this wrong is the difference
  between a login and the appearance of one.
- **A reconcile schedule** — a CronJob, or an in-process ticker.

### Phase 6 — knowing it still works

The failure mode that matters is silence: a token expires, pushes stop, and
nobody notices until a route is missing at the start of a ride. A
`last_successful_push` timestamp with an alert on staleness catches that; a
per-target error counter says which half broke.

## Appendix: the provider research

The constraints below are why the architecture looks the way it does. They have
not changed since the first version of this document.

### Wahoo — clean API, gated signup

`POST https://api.wahooligan.com/v1/routes`, scope `routes_write`, full CRUD.
Required fields: `route[file]` (**base64 FIT**), `external_id`,
`provider_updated_at`, `name`, `workout_type_family_id`, `start_lat/lng`,
`distance`, `ascent`. Access is approval-gated.

### Garmin — no self-serve API

Official Courses API is partner-only. Unofficial Connect session is the
practical path. Free-tier alternatives exist: Strava routes starred on a free
account sync to Garmin natively, and RideWithGPS has an official integration.

### The alternative that needs no code

One shared RideWithGPS account (~$60/yr) uploads GPX as routes and syncs
natively to **both** Garmin and Wahoo, with proper turn cues. It remains the
honest baseline: if the appeal were only the riding and not the building, that
is the answer. This project exists because the building is part of the point,
and because the routes stay on hardware we control.

## Sources

- [Wahoo Cloud API](https://cloud-api.wahooligan.com/) · [developer portal](https://developers.wahooligan.com/cloud)
- [Garmin Courses API (partner program)](https://developer.garmin.com/gc-developer-program/courses-api/)
- [RideWithGPS connected services](https://support.ridewithgps.com/hc/en-us/articles/4419008470299-Connected-Services-Garmin-Connect-Strava-Relive-Wahoo-Hammerhead-and-Coros)
- [Strava routes → Garmin](https://support.strava.com/en-us/articles/15401810-syncing-strava-routes-to-your-garmin-device)
- [Authelia trusted header SSO](https://www.authelia.com/integration/trusted-header-sso/introduction/)
- [muktihari/fit](https://github.com/muktihari/fit) — the FIT SDK
