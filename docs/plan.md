# Shared cycling route sync — research & plan

**Goal:** a shared place where two riders drop GPX routes, and those routes land on a
Garmin Edge/watch and a Wahoo ELEMNT without either of us doing manual file copying.

---

## 1. What already exists (hostable)

**Nothing off-the-shelf does the whole job.** The self-hosted GPX apps solve *storage and
sharing*; none of them push to Garmin/Wahoo accounts.

| Project | What it gives you | What's missing |
|---|---|---|
| [wanderer](https://github.com/Flomp/wanderer) (AGPL, docker compose, ActivityPub federation) | Self-hosted trail/GPX database, upload, tag, search, share by URL, GPX export | No device/account sync at all |
| [FitTrackee](https://github.com/SamR1/FitTrackee) | Self-hosted activity tracker | Activities, not routes; no push to devices |
| A plain **git repo of `.gpx` files** | Free, versioned, PR review, fits this homelab's GitOps habits | No push |

So the realistic shapes are: **(A)** pay a SaaS that already syncs both ways, **(B)** self-host
storage + write the ~300 lines of push glue yourself, or **(C)** hybrid.

## 2. The constraint that decides everything: the write path to each device

Both head units pull routes from *their own cloud account*, not from a file server. So the
question is only "how do I get a route into Garmin Connect Courses and into the Wahoo route
library".

### Wahoo — clean API, gated signup
- Public **Wahoo Cloud API**: `POST https://api.wahooligan.com/v1/routes`, scope `routes_write`.
  Required fields: `route[file]` (**base64 FIT**, not GPX), `external_id`,
  `provider_updated_at`, `name`, `workout_type_family_id`, `start_lat/lng`, `distance`, `ascent`.
  Full CRUD (`GET`/`PUT`/`DELETE`) — good enough for idempotent sync and deletes.
- **But:** Wahoo limits API access to approved apps; you submit a request at
  [developers.wahooligan.com/cloud](https://developers.wahooligan.com/cloud) and only then get a
  client id/secret. A two-person personal app may or may not be approved. **This is the single
  biggest schedule risk — request the key on day 1, before writing anything.**
- Fallback if not approved: the ELEMNT companion app imports a `.gpx`/`.fit` opened from the
  phone (share sheet → ELEMNT). A self-hosted page listing routes with download links makes that
  a 2-tap manual step. Also works: link the Wahoo account to Strava/RWGPS/Komoot and let their
  native sync do it.

### Garmin — no self-serve API
- The official [Courses API](https://developer.garmin.com/gc-developer-program/courses-api/)
  is part of the Connect Developer Program: commercial partners only, no personal signup.
- Practical path is the **unofficial Connect web session**: [`garth`](https://github.com/matin/garth)
  for auth (handles SSO + MFA, tokens last roughly a year) and then Connect's own
  course import endpoint — the same call the website's *Training → Courses → Import* button makes.
  [`python-garminconnect`](https://github.com/cyberjunky/python-garminconnect) already wraps auth
  and activity upload; the course import call needs adding by hand (verify the exact
  `course-service` path with devtools before committing to it).
- This is ToS-grey and can break on any Garmin deploy. Acceptable for a two-person homelab, not
  for anything public.
- Fallback: courses starred in Strava sync to Garmin natively **on a free Strava account**, and
  RideWithGPS has an official Garmin Connect integration.

### The GPX → FIT problem
Wahoo needs FIT; Garmin accepts GPX but a **FIT course carries turn-by-turn cues and a GPX doesn't**.
Plan on generating a FIT course server-side (`gpsbabel`, or Python `fit-tool`/`fitdecode`+writer)
and, if we want real navigation cues rather than a breadcrumb, running the GPX through a routing
engine (Valhalla/BRouter) to derive turn instructions. **Do not underestimate this** — it is the
one genuinely fiddly piece of the build.

## 3. Options

### Option A — no code: one shared RideWithGPS account (or club)
RWGPS uploads GPX as routes, and has native, automatic route sync to *both* Garmin Connect and
Wahoo — pin a route to your library and it appears on the device after a sync.
Cost: Basic ~$60/yr (saving/creating routes needs a paid tier); one shared account, or one paid
account per rider plus a club/shared collection.

- **Pros:** works today, zero maintenance, official integrations that don't break, handles turn cues properly.
- **Cons:** costs money, not self-hosted, routes live in someone else's cloud.
- **This is the honest baseline.** If the appeal of the project is the riding and not the building,
  stop here.

### Option B — full self-host: git repo + in-cluster sync service *(recommended if you want to build)*
Source of truth is a git repo of GPX files; a small service in this K3s cluster reconciles that
repo into both riders' Garmin and Wahoo accounts. Fits the existing GitOps/ArgoCD/Vault pattern
exactly, and "shared repo" is literally what you asked for.

### Option C — hybrid
wanderer (nice UI, map preview, ActivityPub sharing) as the library, plus the same push service
from Option B reading wanderer's API instead of git. Costs an extra moving part; buy it only if
you actually want the browsing/map UI. Start with B, bolt wanderer on later if the CLI-ish
workflow annoys you.

---

## 4. Plan for Option B

### Repo layout (new repo, e.g. `wncservices/routes`)
```
routes/
  <rider-or-shared>/<slug>/route.gpx
  <rider-or-shared>/<slug>/route.yaml   # name, description, targets: [garmin:wilant, wahoo:friend], tags
```
Git is the state: a route added in a PR gets pushed, a route deleted in a PR gets deleted from both
clouds. `route.yaml` keeps per-route targeting so "my private test route" doesn't hit his ELEMNT.

### Service: `route-sync` (new Helm chart in this repo)
Hand-written chart (no upstream exists) — copy the shape of `bitwarden/`:
`Deployment`, `Service`, `ServiceAccount`, `_helpers.tpl`, plus `templates/` with
`ExternalSecret`, `IngressRoute` (internal middleware, `certResolver: cloudflare`),
`ServiceMonitor`, and a `CronJob`.

Reconcile loop, per rider account:
1. Clone/pull the routes repo (or receive a GitHub webhook → immediate run; CronJob every 15 min as the safety net).
2. Parse + validate GPX, compute distance/ascent/start point, dedupe by content hash.
3. Convert to FIT course (+ optional turn cues from a routing engine).
4. Desired-state diff against a local state table (`route slug → garmin courseId, wahoo routeId, hash`).
5. Push: create/update/delete via Wahoo `/v1/routes` and via the Garmin Connect course endpoints.
6. Emit Prometheus metrics: `route_sync_last_success_timestamp`, per-target error counters, routes in sync.

State: SQLite on a PVC (`nfs-client`) is enough for two riders; if you'd rather not have a PVC,
a CNPG `Database` CRD in `postgres-cluster` is the house pattern.

Language: Python — `garth`/`python-garminconnect`, `gpxpy`, `fit-tool`, `httpx`, FastAPI for the
webhook + a status page.

### Secrets (per this repo's rules — every value lives in Vault)
`kv2_tooling/route-sync/env`:
`WAHOO_CLIENT_ID`, `WAHOO_CLIENT_SECRET`, `WAHOO_REFRESH_TOKEN_<rider>`,
`GARMIN_TOKENS_<rider>` (garth token blob), `GITHUB_APP_KEY` / deploy key, `WEBHOOK_SECRET`.
Reaching pods via **Vault → ExternalSecret → K8s Secret → `envFrom`** only.
Refreshed OAuth tokens are written back with a `PushSecret` (same pattern as authelia/pgadmin) —
otherwise a pod restart loses the refresh chain.

### Wiring checklist (repo conventions)
1. `route-sync/` chart as above.
2. Add to the `helm-generator` ApplicationSet, **wave 1**.
3. Add `route-sync` namespace to `tooling-projects.yaml` destinations.
4. Register in `vault/terraform/applications.tf` — `tooling_default_ns` (it *does* authenticate to
   Vault for the PushSecret round-trip; if you drop PushSecret, use `external_apps` instead).
5. Write the secret values into Vault by hand.
6. LAN-only hostname → add the alias in `omada/dns.tf`, not Cloudflare.
   **Exception:** the GitHub webhook and the Wahoo OAuth callback need to be reachable —
   do the OAuth dance from the LAN (browser-side redirect, so internal is fine), and for the
   webhook either expose one public host in `cloudflare/dns.tf` or poll git on the CronJob and
   skip the webhook entirely. **Polling is the simpler, safer default — start there.**

### Phasing
| Phase | Deliverable | Exit criteria |
|---|---|---|
| 0 | **Request Wahoo API access** (partnerships@wahoofitness.com / developer portal). Verify the Garmin course endpoint by hand with devtools + a throwaway `garth` session. | You know which of the two write paths actually exist for you |
| 1 | Repo layout + local CLI: `route-sync push --dry-run` printing the diff | Correct desired state from a git checkout |
| 2 | GPX → FIT course conversion | A generated FIT loads and navigates on the actual ELEMNT/Edge |
| 3 | Garmin push (create/update/delete, idempotent) | Route in repo appears in Connect Courses, re-run is a no-op |
| 4 | Wahoo push | Same, on the ELEMNT after a WiFi sync |
| 5 | Chart + ArgoCD + Vault + CronJob + ServiceMonitor | Two riders, one repo, both devices, unattended |
| 6 | Optional: wanderer as UI, Grafana dashboard, alert on `last_success` staleness | — |

Phases 0–2 are where this succeeds or fails; 3–6 are routine.

### Risks
- **Wahoo API key refused** → whole Wahoo half degrades to the manual share-sheet import, or to
  routing through RWGPS/Komoot. Decide before building whether that's acceptable.
- **Garmin unofficial endpoint changes / auth hardening** → periodic breakage, occasional manual
  MFA re-auth. Alert on the staleness metric so you find out at home, not at the start of a ride.
- **Turn-by-turn quality** — a naive FIT course navigates as a breadcrumb line. RWGPS/Komoot cues
  are genuinely better; matching them is real work.
- Credentials for two personal accounts sit in one cluster. The `internal` middleware is an IP
  allow-list, not authentication — don't expose the status page publicly.

## 5. Recommendation

Run **Option A** now (shared RideWithGPS, riding uninterrupted this weekend) and build **Option B**
in parallel, gated on the Phase 0 answer from Wahoo. If Wahoo declines API access, Option B is only
worth finishing for the Garmin half and A stays the Wahoo path.

## Sources
- [Wahoo Cloud API reference](https://cloud-api.wahooligan.com/) · [developer portal](https://developers.wahooligan.com/cloud) · [3rd-party route sync](https://support.wahoofitness.com/hc/en-us/articles/115000368190-Sync-download-third-party-routes-to-ELEMNT-computers) · [GPX/FIT import](https://support.wahoofitness.com/hc/en-us/articles/20582927265042-Import-and-delete-routes-to-ELEMNT-computers)
- [Garmin Courses API (partner program)](https://developer.garmin.com/gc-developer-program/courses-api/) · [python-garminconnect](https://github.com/cyberjunky/python-garminconnect) · [garmin-uploader](https://github.com/La0/garmin-uploader) · [gimporter](https://github.com/gimportexportdevs/gimporter)
- [RideWithGPS connected services](https://support.ridewithgps.com/hc/en-us/articles/4419008470299-Connected-Services-Garmin-Connect-Strava-Relive-Wahoo-Hammerhead-and-Coros) · [pricing](https://ridewithgps.com/pricing) · [upload GPX](https://support.ridewithgps.com/hc/en-us/articles/4419024044827-Upload-Activities-Routes-GPS-Files)
- [Strava routes → Garmin](https://support.strava.com/en-us/articles/15401810-syncing-strava-routes-to-your-garmin-device) · [free for all users](https://www.bikeradar.com/news/garmin-direct-route-syncing-from-strava)
- [wanderer](https://github.com/Flomp/wanderer)
