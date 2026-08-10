# Domestique

> The rider who fetches the bottles so the others can just ride.

Two riders, two different head units, one shared set of routes. Domestique keeps a route library
in sync with each rider's Garmin Connect and Wahoo account, so a route added once shows up on a
Garmin Edge *and* a Wahoo ELEMNT.

Free software under the [GNU AGPL-3.0](LICENSE): use it, change it, self-host it, for anything at
all. The one condition that matters — **if you run a modified version as a network service, its
users must be able to get your source.** That is section 13, and it is the reason for this licence
rather than a permissive one.

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

**This repository holds no route data** — see [Where routes live](#where-routes-live).

## Status

Early. The library, diff engine, CLI, HTTP API and web UI work end to end, and routes convert to
Garmin FIT courses. **The provider adapters are stubs** — see [Roadmap](#roadmap). Today you can
add routes, browse them, see exactly what would be pushed where, and export a FIT to copy onto a
device by hand; automatic pushing is not wired up yet.

## Where routes live

**In a database.** Routes are rows and the GPX is a blob in the row.

| Engine | For | DSN |
|---|---|---|
| **PostgreSQL** | A deployment | `postgres://user:pass@host/domestique` |
| **SQLite** | A laptop | `data/domestique.db` |

The DSN picks the engine — a `postgres://` URL means PostgreSQL, anything else is a SQLite file
path. Both are tested against the same suite.

Routes get in three ways: **uploaded** through the web UI, **imported from Komoot**, or loaded
in bulk from a folder of files:

```bash
just import ./some-folder-of-gpx
```

That last one is a one-off. Nothing keeps reading that folder afterwards.

GPX files are personal location data — a route usually starts at somebody's front door — so
**this repository holds none**, and the database is yours.

## Quick start

Only Docker installed:

```bash
just up            # PostgreSQL + the app on :8080
just logs
just down          # `just reset` also drops the database
```

That runs against the same PostgreSQL a deployment uses, so local and deployed
differ as little as possible. `just docker-test`, `just docker-check` and
`just docker-build` do the rest without a local toolchain either.

Komoot import is **on** in that stack: open the app and sign in to Komoot from
the panel. The compose file carries a throwaway encryption key so that works
out of the box.

With Go and Node installed, which is quicker:

```bash
just install
just build
just demo          # a local SQLite library with the example route loaded
```

Either way, open <http://localhost:8080>.

For frontend work run `just api` and `just web` side by side and use the Vite server on :5173.

Without the UI:

```bash
just validate                    # read the library, report problems
just plan                        # what would change on each account
just push -- --dry-run           # same, in push's own words
```

## Logging in

Domestique has no login of its own. Put it behind Traefik with an Authelia
forwardAuth middleware and it reads the identity Authelia passes down.

```yaml
auth:
  mode: proxy
  trusted_proxies: [10.0.0.0/8]
  roles:
    admin: [route-admins]
    rider: [riders]
    viewer: [guests]
```

| Role | Can |
|---|---|
| `viewer` | read routes, download GPX, see what would be pushed |
| `rider` | + upload, import from Komoot, link **their own** head units, push, edit and delete **their own** routes |
| `admin` | + edit and delete **anyone's** routes and head units |

> **The app must not be reachable except through the proxy.** With `mode: proxy`
> it believes the `Remote-User` header — and so would anyone who can talk to it
> directly. `trusted_proxies` narrows that; leave it empty only for a
> ClusterIP-only service.

With `mode: none` (the default) there is no login at all and every visitor is
an admin. That is right for a laptop and wrong for anything else; the UI says
so in the header.

## Linking a head unit

Nothing about riders or devices is configured. Each rider links their own head
unit from Settings, keyed to their Authelia username. A route with no targets
of its own goes to every linked head unit.

**Garmin is linked by signing in.** Enter your Garmin Connect email and
password on the Settings page: the password is used for that one sign-in and
discarded, and what Garmin gives back is stored encrypted in its place. That
needs an encryption key (`domestique keygen`, as below) and the OAuth1
consumer pair Connect's own clients use:

```bash
GARMIN_OAUTH_CONSUMER_KEY=...
GARMIN_OAUTH_CONSUMER_SECRET=...
```

Those are deliberately **not in this repository** — baking scraped credentials
into a source-available project invites them to be treated as ours to publish.
Without them the Settings page says the sign-in is unavailable instead of
offering a form that cannot work.

Two limits worth knowing before you try:

- **An account with two-factor authentication cannot be signed in to this
  way.** There is no code challenge to answer — Garmin offers no other route
  for an app like this one, and the UI says so rather than blaming your
  password.
- **The sign-in lasts about a year**, then it stops working and Settings shows
  when that will be. Signing in again replaces it.

Wahoo is still a stub — see [Roadmap](#roadmap).

## Importing from Komoot

```yaml
komoot:
  enabled: true
```

Each rider then signs in to their own Komoot from the web UI and imports from
their own account. That needs an encryption key, because a sign-in has to be
kept somewhere:

```bash
domestique keygen        # prints a DOMESTIQUE_ENCRYPTION_KEY
```

Without one the sign-in form is not offered at all — a session is stored
encrypted or not stored. `KOMOOT_EMAIL` and `KOMOOT_PASSWORD` remain an
alternative: one shared account for the whole deployment, which a rider's own
sign-in overrides. On the command line, which has no session to sign in with:

```bash
just komoot list
just komoot import          # everything not already here
```

Komoot has **no public API**. This uses the same undocumented endpoints their
apps do, so it will break from time to time — treat it as a convenience, not a
dependency. Already-imported tours are skipped, so running it twice is safe.

## Getting a route onto a device today

The provider adapters are still stubs, but the conversion they will use works.
Write a route out as a Garmin FIT course and copy it over USB:

```bash
just fit kemmelberg-loop
just fit kemmelberg-loop --cues     # add inferred turn cues
```

Or download one from the running app: `GET /api/fit/<slug>` (add `?cues=1`).

Turn cues are **inferred from the shape of the track**, not from a road map.
They are off by default and worth checking before you rely on them at a
junction — a route planner that knows the roads does this better.

## Deploying it

There is a Helm chart in `charts/domestique`, published on every change:

```bash
helm repo add domestique https://wncservices.github.io/Domestique
helm repo update
helm install domestique domestique/domestique \
  --namespace domestique --create-namespace
```

Out of the box that is one pod, a SQLite library on a volume, no ingress and
**no authentication** — every visitor an admin. Set `config.auth.mode: proxy`
and put it behind Authelia before exposing it. For PostgreSQL, supply
`DOMESTIQUE_SOURCE_DSN` from a Secret rather than writing a password into
values.

[`charts/domestique/README.md`](charts/domestique/README.md) has the detail,
and `charts/domestique/ci/full-values.yaml` is a complete worked example.

## Releases

The image and the chart release **separately**, because they change for
different reasons — a values default is not an app change, and an app change
usually needs no chart edit.

| Track | Trigger | Produces |
|---|---|---|
| Container image, dev | every merge to `main` | `:dev`, `:sha-<short>` |
| Container image, release | tag `v<x.y.z>` | `:x.y.z`, `:x.y`, `:x`, `:latest` |
| Binaries | tag `v<x.y.z>` | a GitHub Release with tarballs |
| Helm chart | any change under `charts/` on `main` | a GitHub Release + the Helm repo at `wncservices.github.io/Domestique` |

Each image is pushed to **two registries from one build**, under the same tags:

```
ghcr.io/wncservices/domestique     # canonical — what the chart pulls
docker.io/wilant/domestique        # mirror
```

Same digest either way, so they cannot drift apart.

`:dev` moves under you. Pin `:sha-<short>` when you want to know exactly what is
running. Every image is built for amd64 and arm64 and carries a provenance
attestation.

The chart's `appVersion` is the image tag it deploys — bumping it is how a chart
release picks up an app release.

## Configuration

Copy `domestique.example.yaml` to `domestique.yaml`. It is deliberately small: where the database
is, and how to recognise a user. **No routes, no accounts, no riders, no credentials** — routes
and head units live in the database, and credentials come from the environment (in a cluster,
Vault → ExternalSecret → `envFrom`).

A route is pushed to the head units it names in `targets`, or to every linked one when it names
none. Naming targets is what keeps one rider's private routes off the other's device.

## Layout

| Path | What |
|---|---|
| `apps/api/` | Go service: CLI and HTTP API |
| `apps/web/` | Vue 3 + Vite frontend |
| `examples/routes/` | A sample .gpx, so the demo has something to import |
| `charts/domestique/` | The Helm chart |
| `docs/plan.md` | Why the providers work the way they do — read before touching an adapter |

## Roadmap

| Phase | What | Status |
|---|---|---|
| 1 | Library, diff engine, CLI, API, web UI | ✅ |
| 2 | GPX → FIT course conversion, with inferred turn cues | ✅ |
| 3 | Garmin: sign-in ✅, FIT course upload ✅, push not wired to the library yet | 🟡 |
| 4 | Wahoo push (Cloud API) | ⬜ stub, needs approved API access |
| 5 | Deploy: Helm chart ✅, scheduled reconcile, Vault-backed tokens | 🟡 |
| 6 | Metrics + staleness alerting | ⬜ |

Phases 2–4 are where this succeeds or fails. Neither provider offers a self-serve route API:
Garmin has none at all, and Wahoo's is approval-gated and wants FIT rather than GPX.
`docs/plan.md` has the detail, including what to do if Wahoo says no.

## Contributing

`just check` runs the typecheck, vet and tests. Keep the Go side close to the standard library —
the dependencies today are a YAML parser, a pure-Go SQLite driver and a FIT SDK, and that is the
budget.
