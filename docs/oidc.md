# Plan: generic OIDC as a second auth mode

**Status: shipped.** `mode: oidc` runs in production, against an Auth0 tenant
(`lab/auth0/` in the infra repo), with Google added as a social sign-in
alongside the database connection. The design below is kept as the record of
*why* it looks the way it does — most of it held, a few things changed once
real data hit it:

- **Group claims** work as planned (see below), through a post-login Action
  (`lab/auth0/actions.tf`) rather than a promise to add one later.
- **The rider-identity migration** is `domestique rename-rider` —
  `docs/rider-migration.md` is the runbook, not just the design intent.
- **One thing this plan didn't anticipate**: opening sign-in to a second
  identity provider (Google) surfaced a real question this document doesn't
  answer — what to do when an email invited through the People page already
  has an identity on the issuer. It is deliberately **not** auto-linked to an
  existing account (a Google identity and a database identity for the same
  address stay two separate Auth0 users); the People page grants the invited
  role to whichever identity already exists instead of creating a second one.
  See `internal/auth0mgmt`'s package doc and `internal/api/people.go`.
- **`sub`'s fallback chain** ended up longer than `preferred_username` →
  `sub`: Auth0's database connection populates neither reliably, so
  `identityFromToken` (`internal/api/sso.go`) falls through
  `preferred_username` → `name` → `nickname` → `sub`, each checked against
  the same rider pattern.

The rest of this document is unchanged from before any of it was built.

## Why this rather than picking a provider

Today Domestique authenticates nobody. It reads `Remote-User` from a proxy it
is configured to trust, and **must be unreachable except through that proxy**
(see the warning in the README). That is the right shape for a homelab behind
Authelia and the wrong shape for two other things we have talked about:

- **Social logins.** Authelia authenticates against LDAP. It is an OIDC
  *provider*, not a broker, so "sign in with Google" is not something it can be
  configured into. Federating strangers needs an IdP that brokers — Auth0,
  Keycloak, Zitadel, Clerk, and so on.
- **Anyone else running this.** The app is AGPL and meant to be self-hosted.
  Wiring one vendor's SDK in means nobody can run it without an account with
  that vendor.

Both are solved by the same thing: speak **OIDC**, the protocol, and let the
operator point it at whatever issuer they already have. Auth0 then becomes one
configuration rather than a dependency, and the choice stays reversible — which
matters, because the honest answer to "which IdP" today is "we do not know yet
whether this ever has users beyond two".

**The trigger for building this was a third person who is not in our
LDAP** — it arrived, which is why this moved from plan to shipped, and
Google as an additional sign-in came once the People page existed to grant
that person access without an invite email. The reasoning below stands as a
record of why it waited: `mode: proxy` was less code and less attack surface
right up until that trigger.

## What changes, and what does not

`internal/auth` already has the shape for this: a `Mode`, an `Identity`, and
roles derived from groups. The work is a third mode alongside `none` and
`proxy`, not a rewrite.

| Stays | Changes |
|---|---|
| `Identity{User, Name, Email, Groups, Role}` — everything downstream keys off this | Where those fields come from |
| Role mapping from groups, `required_group`, `default_role` | Groups arrive in a token claim rather than a header |
| Ownership from the session, never the request body | — |
| Route, account and sign-in storage | — |

The interesting consequence: with `mode: oidc` the app authenticates people
itself, so **"must be unreachable except through the proxy" stops being true**.
That is a security improvement, but it is also the sort of change that quietly
invalidates a warning written elsewhere — the README and AGENTS.md both say it,
and both need editing in the same commit.

## Shape of it

```yaml
auth:
  mode: oidc
  oidc:
    issuer: https://app.domestique.dev/auth   # discovery does the rest
    client_id: domestique
    # client_secret from DOMESTIQUE_OIDC_CLIENT_SECRET, never the config file
    redirect_url: https://app.domestique.dev/sso/callback
    scopes: [openid, profile, email, groups]
    groups_claim: groups
  roles:
    admin: [domestique-admins]
    rider: [cyclists]
```

Four endpoints, all in `internal/api`:

- `GET /sso/login` — start the flow: PKCE verifier and `state` into a signed,
  short-lived cookie, redirect to the authorization endpoint.
- `GET /sso/callback` — verify `state`, exchange the code, **verify the ID
  token signature against the issuer's JWKS**, build the identity, set a session
  cookie.
- `POST /sso/logout` — drop the session; RP-initiated logout at the issuer if
  it advertises one.
- `GET /api/me` — unchanged, which is the point.

`/sso` and not `/auth`: in the current deployment the Authelia portal is served
at `app.domestique.dev/auth`, routed to a different service before a request
ever reaches this app. Those two modes are mutually exclusive in practice, but
picking a prefix that collides with a live route is a debugging session nobody
needs.

## Decisions worth making deliberately

**Sessions.** A server-side session keyed by an opaque cookie, in the database
next to everything else — not a JWT in a cookie. Logout that does not actually
end a session is the classic JWT-in-a-cookie mistake, and this app already has
a database and an encryption key.

**Group claims are not universal.** Authelia sends `groups`; Auth0 needs a
custom claim added by an Action and namespaced; Google sends none at all. So
`groups_claim` is configurable, and the fallback when an issuer supplies no
groups is `default_role` — which is why that setting already exists. A
deployment federating Google alone would give everyone `viewer` and promote
people by hand; that is the correct outcome, not a bug to work around.

**`sub` is the identity, not the email.** Emails get reused and changed.
Riders are currently keyed by Authelia username, so a migration needs a stable
`rider` value — probably `preferred_username`, falling back to `sub`. **This is
the one genuinely hard part**: routes, accounts and provider sign-ins are all
keyed on the rider string, and changing what that string means orphans them.
Either keep a mapping table from the day OIDC lands, or accept a one-off
rename migration and write it before anyone else has data.

**Verify the ID token properly.** Signature against JWKS with rotation, plus
`iss`, `aud`, `exp` and the nonce. Use `github.com/coreos/go-oidc`; the
dependency budget in AGENTS.md is for exactly this case — a spec where writing
it yourself means writing the vulnerabilities yourself.

**Keep `mode: proxy`.** This deployment has since moved to `mode: oidc`
because it needed to face the public, but `mode: proxy` stays supported and
is the right shape for anyone behind their own SSO — it is one HTTP header of
trust and less code than speaking OIDC at all.

## Rough order

1. `internal/auth`: `ModeOIDC`, config, discovery, token verification. Tested
   against a fake issuer — a real one in tests is a flake generator.
2. Session store in `internal/sessions`, same encryption box as
   `internal/providerlink`.
3. The four endpoints, plus CSRF on the callback (`state`) and PKCE.
4. Rider identity migration, decided before anything is written.
5. UI: a sign-in page for `mode: oidc` — the landing page's button, pointed at
   `/sso/login` instead of across origins.
6. Docs: the "must be unreachable except through the proxy" warning is
   mode-specific from then on.

Steps 1–3 are a couple of days. Step 4 is the one that decides whether this is
pleasant or a data migration under pressure.
