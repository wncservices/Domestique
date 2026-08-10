# Domestique Helm chart

Deploys [Domestique](https://github.com/wncservices/Domestique) — a shared
cycling route library that syncs to Garmin and Wahoo head units.

## Install

```bash
helm repo add domestique https://wncservices.github.io/Domestique
helm repo update
helm install domestique domestique/domestique \
  --namespace domestique --create-namespace
```

The URL carries the repository's capital D — the lowercase one 404s.

**PostgreSQL is required** — see below; the chart refuses to render without
one. With nothing else set you get a single pod, no ingress and **no
authentication**. Port-forward to look at it:

```bash
kubectl -n domestique port-forward svc/domestique 8080:80
```

## Two things to set before exposing it

**Authentication is off by default, which means every visitor is an admin** —
they can upload, delete and push routes. Domestique authenticates nobody
itself; it reads the identity a reverse proxy passes down. Behind Traefik with
an Authelia forwardAuth middleware:

```yaml
config:
  auth:
    mode: proxy
    trusted_proxies:
      - 10.0.0.0/8          # your pod CIDR
    roles:
      admin: [route-admins]
      rider: [riders]
      viewer: [guests]
```

`mode: proxy` makes the app trust the `Remote-User` header, so **the Service
must not be reachable except through that proxy**. `trusted_proxies` narrows it
further; leave it empty only for a ClusterIP-only Service.

## PostgreSQL, and no volume

Routes, sync state, linked head units and Komoot sign-ins are all rows in one
database, so the pod keeps nothing of its own. There is no `persistence` block
any more and no PVC: with PostgreSQL there was nothing left to put on a volume,
and an empty one that silently did nothing was worse than none.

The usual case is a [CloudNativePG](https://cloudnative-pg.io) cluster in the
same namespace:

```yaml
postgresql:
  cluster: domestique-db
```

The operator publishes `Secret/domestique-db-app` holding a ready-made
connection string that already points at the read-write service. The chart
reads it directly, so there is no copy of the password anywhere and nothing to
change when the operator rotates it or the cluster fails over.

Any other PostgreSQL works too:

```yaml
postgresql:
  existingSecret: domestique-db
  secretKey: uri
```

**SQLite is not a deployment option.** It still runs a laptop (`just up`,
`domestique serve`), but as a deployment it means one replica pinned to one
node holding the only copy of the library on a disk nothing backs up. The
chart used to offer it and defaulted to it, which is the wrong default to
reach for by accident.

## Komoot sign-in

Riders connect their own Komoot account from the UI. The session is stored
encrypted, so it needs a key:

```bash
domestique keygen
```

Put it in a Secret and name it:

```yaml
encryptionKey:
  existingSecret: domestique
  secretKey: DOMESTIQUE_ENCRYPTION_KEY
```

Without it the sign-in form is not offered and everything else works as
normal. Replacing the key invalidates every stored sign-in; riders sign in
again.

## Other credentials

A DSN carries a password, so it
comes from a Secret rather than values:

```yaml
config:
  source: {}          # no dsn here
envFrom:
  - secretRef:
      name: domestique      # must contain DOMESTIQUE_SOURCE_DSN
```

Create that Secret however you manage secrets — an ExternalSecret from Vault,
for instance. Keys the app reads:

| Key | For |
|---|---|
| `KOMOOT_EMAIL` | one shared Komoot account, as an alternative to riders signing in themselves |
| `KOMOOT_PASSWORD` | |
| `GARMIN_OAUTH_CONSUMER_KEY` | the OAuth1 consumer Garmin Connect's own clients use — one pair for the whole deployment. **Optional**: an admin can paste the pair into Settings instead, stored encrypted, which takes precedence. Supply it here to keep it in Vault rather than in the database. Not shipped with this chart or its source |
| `GARMIN_OAUTH_CONSUMER_SECRET` | |

`DOMESTIQUE_SOURCE_DSN` and `DOMESTIQUE_ENCRYPTION_KEY` are set by the chart
from the Secrets named above; they do not go in `envFrom`.

## Values

| Key | Default | What |
|---|---|---|
| `image.repository` | `ghcr.io/wncservices/domestique` | |
| `image.tag` | chart `appVersion` | |
| `replicaCount` | `1` | Leave at 1 — two replicas race on the state file |
| `config` | see `values.yaml` | Rendered into a ConfigMap as the app's config file. **No secrets** |
| `envFrom` | `[]` | Where credentials come from |
| `postgresql.cluster` | `""` | CloudNativePG cluster in this namespace. **Required**, unless `existingSecret` |
| `postgresql.existingSecret` | `""` | Any Secret holding a PostgreSQL URL; wins over `cluster` |
| `postgresql.secretKey` | `uri` | Key within that Secret |
| `encryptionKey.existingSecret` | `""` | Enables Komoot sign-in from the UI |
| `ingressRoute.enabled` | `false` | Traefik `IngressRoute` |
| `ingress.enabled` | `false` | Plain `Ingress`, as an alternative |
| `serviceAccount.name` | release name | Vault's Kubernetes auth binds to this |
| `podDisruptionBudget.enabled` | `true` | `maxUnavailable: 1`, so node drains still work with one replica |
| `automountServiceAccountToken` | `true` | Needed if the app authenticates to Vault |
| `revisionHistoryLimit` | `3` | |

`ci/full-values.yaml` is a complete worked example: PostgreSQL, a proxy doing
authentication, Traefik ingress and Secret-backed credentials. The values in it
are placeholders.

## What this chart does not do

- **Create the database.** Point `DOMESTIQUE_SOURCE_DSN` at one that exists.
- **Create the Secret.** That is your secret manager's job.
- **Provide a ServiceMonitor.** The app exposes no metrics yet.
