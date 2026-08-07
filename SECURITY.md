# Security

## Reporting a vulnerability

Report privately through [GitHub Security Advisories](https://github.com/wncservices/domestique/security/advisories/new).
Please do not open a public issue for anything exploitable.

This is a small personal project — expect a reply within a week or so, not within
hours.

## What this project handles

Two kinds of sensitive data, worth knowing about if you self-host it:

**Route files are personal location data.** A GPX usually starts at somebody's
front door. domestique holds no routes in this repository, and the route source
(a private git repo, or a database) is yours to protect. There is **no
authentication in the app itself** — put it behind something that authenticates,
or on a network only you can reach. Anyone who can reach the HTTP API can read
every route and, on a writable source, delete them.

**Provider credentials.** Garmin and Wahoo credentials come from the environment
and are never read from the repository. Do not put them in `domestique.yaml`;
that file holds account ids and labels only.

## Known, accepted limitations

- The Garmin adapter is planned against an unofficial Connect session. That is a
  deliberate trade-off for a two-person tool, not an oversight — see `AGENTS.md`.
- Uploads are bounded (20 MiB) and parsed, but a route source is trusted input:
  anyone who can upload can store arbitrary bytes as a route blob.
- `--web-dir` and `--config` are operator-supplied paths and are not sandboxed.
  Route slugs, which do come from users, are validated against the library root.
