# Quorum

Self-hostable availability polls: propose dates, share a link, guests
vote yes / if need be / no without an account, you finalize, everyone
gets a calendar invite.

A single static Go binary. SQLite by default. No Node, no Redis, no
external services.

**Status: early development (M0 — foundations).** Not usable yet.

## Build and run

Requires Go 1.26+ (`templ` and the Tailwind CLI are fetched
automatically by the Makefile).

```sh
make run           # build and start on :8080
make dev           # live-reload development loop
make test lint     # what CI runs
```

## Configuration

Everything is optional; defaults in parentheses.

| Variable | Purpose |
|---|---|
| `QUORUM_ADDR` | Listen address (`:8080`) |
| `QUORUM_BASE_URL` | Public URL of the instance (`http://localhost:8080`) |
| `QUORUM_DB_PATH` | SQLite database file (`quorum.db`) |
| `QUORUM_LOG_LEVEL` | `debug`, `info`, `warn`, `error` (`info`) |
| `QUORUM_LOG_FORMAT` | `json` or `text` (`json`) |

## License

Not chosen yet.
