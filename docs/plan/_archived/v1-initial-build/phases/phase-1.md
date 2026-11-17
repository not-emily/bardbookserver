# Phase 1: Foundation

> **Depends on:** None
> **Enables:** Every subsequent phase
>
> See: [Full Plan](../plan.md)

## Goal

Produce a static Go binary that loads config, owns a migrated SQLite database, serves HTTP on port 2273, and handles forwarded headers correctly behind a proxy.

## Key Deliverables

- `bard` binary with CLI verbs stubbed: `serve`, `scan`, `user`, `export`
- YAML config loading with defaults, validation, and a generated example file
- SQLite connection with pragmas set (WAL, foreign keys, busy timeout)
- Embedded SQL migration runner against a `schema_version` table
- HTTP server with graceful shutdown and `log/slog` structured logging
- Trusted-proxy middleware: parse `X-Forwarded-For` / `X-Forwarded-Proto` **only** from allowlisted CIDRs
- `/healthz` endpoint

## Files to Create

- `cmd/bard/main.go` — entrypoint, CLI dispatch
- `internal/config/config.go` — struct, defaults, validation
- `internal/config/example.yml` — commented reference config
- `internal/db/db.go` — open, pragmas, close
- `internal/db/migrate.go` — embedded migration runner
- `internal/db/migrations/0001_init.sql` — `schema_version` only
- `internal/api/server.go` — mux, server lifecycle
- `internal/api/middleware.go` — trusted proxy, request logging, panic recovery
- `go.mod`, `LICENSE` (MIT), `.gitignore`

## Dependencies

**Internal:** none.

**External:**
- `modernc.org/sqlite` — pure-Go SQLite driver
- `gopkg.in/yaml.v3` — config parsing

## Implementation Notes

Config defaults that matter: `port: 2273`, `tls: off`, `trusted_proxies: []`, `workers: 2`. Verify 2273 against the IANA registry before locking it in; see the Tech Stack note in the plan.

**The trusted-proxy middleware is the security-critical piece of this phase.** With an empty allowlist, forwarded headers are ignored entirely and `RemoteAddr` is used as-is. Only when the immediate peer falls inside an allowlisted CIDR may `X-Forwarded-For` be read, and then only its rightmost untrusted hop. Getting this wrong in either direction is bad: trusting the header unconditionally lets a caller spoof its address to evade the Phase 2 rate limiter, while ignoring it behind Cloudflare Tunnel collapses every family member and attacker into one `127.0.0.1` bucket. Both failures are silent.

Migrations are forward-only, applied in filename order inside a transaction, recorded in `schema_version`. No down migrations — a bad migration is fixed by a new one.

CLI verbs are stubs here; they gain behavior in the phases that own them.

## Validation

- [ ] `go build` produces a binary with no cgo; `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build` succeeds
- [ ] First run creates the database and applies migrations; second run is a no-op
- [ ] `GET /healthz` returns 200
- [ ] With `trusted_proxies: []`, a request carrying `X-Forwarded-For` logs the real peer address, not the header
- [ ] With the peer's CIDR allowlisted, the same request logs the forwarded address
- [ ] SIGTERM drains in-flight requests before exit
- [ ] Malformed config fails at startup with a message naming the bad field
