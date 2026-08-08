# domestique Helm chart

Deploys [domestique](https://github.com/wncservices/domestique) — a shared
cycling route library that syncs to Garmin and Wahoo head units.

## Install

From the OCI registry, which needs no repository setup:

```bash
helm install domestique oci://ghcr.io/wncservices/charts/domestique \
  --namespace domestique --create-namespace
```

Or from the Helm repository:

```bash
helm repo add domestique https://wncservices.github.io/domestique
helm repo update
helm install domestique domestique/domestique --namespace domestique --create-namespace
```

With nothing else set you get a single pod, a SQLite route library on a 1 Gi
volume, no ingress and **no authentication**. Port-forward to look at it:

```bash
kubectl -n domestique port-forward svc/domestique 8080:80
```

## Two things to set before exposing it

**Authentication is off by default, which means every visitor is an admin** —
they can upload, delete and push routes. domestique authenticates nobody
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

**Persistence is on by default and should stay on.** Sync state — what each
account is recorded as holding — is a JSON file. Lose it and every route is
pushed to every device again.

## PostgreSQL

The route library runs on PostgreSQL or SQLite. A DSN carries a password, so it
comes from a Secret rather than values:

```yaml
config:
  source:
    kind: db
    # no dsn here
envFrom:
  - secretRef:
      name: domestique      # must contain DOMESTIQUE_SOURCE_DSN
```

Create that Secret however you manage secrets — an ExternalSecret from Vault,
for instance. Keys the app reads:

| Key | For |
|---|---|
| `DOMESTIQUE_SOURCE_DSN` | PostgreSQL connection string; overrides `config.source.dsn` |
| `KOMOOT_EMAIL` | Komoot import, when `config.komoot.enabled` |
| `KOMOOT_PASSWORD` | |

With PostgreSQL the volume holds only the state file, so it can be small.

## Values

| Key | Default | What |
|---|---|---|
| `image.repository` | `ghcr.io/wncservices/domestique` | |
| `image.tag` | chart `appVersion` | |
| `replicaCount` | `1` | Leave at 1 — two replicas race on the state file |
| `config` | see `values.yaml` | Rendered into a ConfigMap as the app's config file. **No secrets** |
| `envFrom` | `[]` | Where credentials come from |
| `persistence.enabled` | `true` | Volume for sync state, and SQLite if used |
| `persistence.size` | `1Gi` | 256Mi is plenty with PostgreSQL |
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
