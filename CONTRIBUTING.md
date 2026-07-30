# Contributing to Quorum

## Build and test

Go ≥ 1.26 is the only prerequisite; every other tool (templ, sqlc,
Tailwind CLI, golangci-lint) is version-pinned and fetched by the
Makefile into `.tools/`.

```sh
make dev             # live-reload development loop on :8080
make test lint       # what CI runs on every push
make test-postgres   # the same suite against PostgreSQL (needs Docker)
make generate css    # regenerate templ/sqlc code and the CSS
```

Generated artifacts (`*_templ.go`, `internal/store/sqlite/`,
`web/static/css/app.css`) are committed; CI fails if they drift from
their sources.

## Rules that are enforced by tests

- **One SQL source for both engines.** Migrations and sqlc queries are
  written once, in the SQLite dialect restricted to the
  SQLite/PostgreSQL common subset. Never add a second migrations
  directory or query set — `make test-postgres` is the referee. See
  `CLAUDE.md` for the exact substitution rules.
- **English and French move together.** Every catalog key must exist
  in both `translations/*.toml` files and be referenced somewhere in
  the source; dedicated tests fail otherwise.
- Domain logic lives in `internal/{poll,space,auth,...}` and is tested
  without HTTP; handlers stay thin.

## Commits

Conventional Commits (`feat(scope): …`, `fix: …`), one logical change
per commit, `make lint test` green before each.

## Releases (maintainer only)

Releases are cut manually from the *Release* GitHub Action: it mints a
CalVer tag (`YYYY.MM.DD.HHmmss`), publishes cross-compiled binaries
with checksums, and pushes the multi-arch image to GHCR. Never create
release tags by hand; nothing is published on push.
