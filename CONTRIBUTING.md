# Contributing

## The short version

```bash
just up            # PostgreSQL + the app on :8080, only Docker needed
just docker-check  # gofmt, vet, go test, web typecheck — what CI runs
```

Then open a pull request.

## Licence

Domestique is under the [GNU AGPL-3.0](LICENSE).

**Before a pull request can be merged you will be asked to confirm, in a
comment, that you license your contribution for relicensing — including under
commercial terms.** You keep the copyright in what you write, and your work
stays available to everyone under the AGPL.

There is no bot and no form; it is one sentence, asked once. The reason rather
than a shrug: this may one day be offered as a hosted service with paid
features, and that door closes permanently the first time a patch is merged
without it, because relicensing would then need every past contributor's
agreement. Asking now is far easier than finding people three years from now.

If you would rather not, open an issue describing the change instead — a good
bug report is worth a great deal and needs no paperwork.

## What the code expects of you

The repository's conventions are in [AGENTS.md](AGENTS.md), which is worth
skimming before a first change. The parts that come up most:

- **Run against PostgreSQL, not just SQLite.** `just docker-test` does. The
  two engines differ in placeholders, blob types and booleans, and a green
  SQLite run says nothing about the one that ships.
- **A skipped test reads exactly like a passing one.** If a test can silently
  not run, make it fail instead.
- **No secrets in the repository**, including things that look like
  placeholders. Credentials come from the environment.
- **Say why, not what.** The code says what it does; a comment earns its place
  by explaining a decision, a constraint, or a bug that is easy to reintroduce.

## Reporting a security issue

Please do not open a public issue. Use GitHub's private vulnerability
reporting on the repository's Security tab.
