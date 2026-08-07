# Contributing

Thanks for looking. This is a small personal project, so the bar is "does it keep
working and stay understandable", not ceremony.

## Getting set up

```bash
just install
just check      # typecheck + vet + tests — the same gate CI runs
just demo       # serve the bundled example routes at :8080
```

For frontend work, run `just api` and `just web` side by side and use the Vite
server on :5173.

## Before opening a PR

- `just check` passes, and `golangci-lint run ./apps/api/...` is clean.
- New behaviour has a test that **fails without the change**. Worth actually
  checking: a couple of tests in this repo originally passed for the wrong
  reason, and only deleting the code they covered revealed it.
- Comments explain *why*. The code already says what.

## Things that will get a PR sent back

- **A real GPX file.** Routes are personal location data and never belong in this
  repository. `examples/routes/` holds one synthetic route and stays that way.
- **A credential**, including a placeholder that looks real.
- Making the filesystem source writable. It is read-only on purpose: in a
  git-backed library, adding a route is a commit, which is where review and
  history come from.
- Flipping `targets.Implemented` for a provider whose adapter still returns
  errors. That flag is what makes the UI honest about what works.

`AGENTS.md` is the fuller version of all this, and is the file to read before
changing anything structural.

## Dependencies

The Go side is standard library plus a YAML parser and a pure-Go SQLite driver.
That is the budget. Adding to it needs a reason that a reviewer would agree is
genuinely hard to do by hand — FIT encoding would qualify; a helper library
would not.
