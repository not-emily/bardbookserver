# Claude Context - bardbookserver

This file provides context for Claude Code sessions.

## Project Overview
Bard is a self-hosted server for ebooks and audiobooks — Plex, but for books.
`bardbookserver` is the server half: a single static Go binary that indexes
directories of book files, enriches them with metadata, and serves them over an
authenticated HTTP API. Native iOS and Android apps will follow as separate
projects; there is no first-party reading client yet.

It runs alongside an existing Plex install on a 16GB i7-7700, so a small
resident footprint and conservative CPU use are hard requirements.

## Tech Stack
- Go 1.27, no cgo anywhere (`CGO_ENABLED=0`)
- `net/http` + `http.ServeMux` — stdlib routing is sufficient, no router dep
- SQLite via `modernc.org/sqlite` (pure Go, FTS5)
- `gopkg.in/yaml.v3` config, `log/slog` logging
- `golang.org/x/crypto/argon2` (Argon2id), `golang.org/x/term`
- Default port 2273, configurable

## Key Patterns & Conventions

**Authorization lives in `internal/policy`.** Ported from the user's
`rails/policies/advanced` pattern. Rules that must hold:
- Action methods return `Result`, never a bare bool
- Build results only via `allow()` / `deny()` / `denyWith()`, never inline
- Role helpers stay unexported and boolean
- `Message()` is the only place user-facing authorization text lives
- Every `Reason` needs a case in `Message()` — a test enforces this
- No dynamic policy resolution; typed constructors only

**Visibility is a query condition, never a post-filter.** Every path returning
more than one record composes through a scope so a filtered or searched listing
cannot bypass it. Post-filtering also breaks pagination.

**Never return a reason code for something the user shouldn't know exists.**
Hidden content yields absence (404); reason codes describe only what is
already visible.

**Fail closed on age gating.** `model.EffectiveMinAge` and
`db.EffectiveMinAgeSQL` are twins held together by
`TestEffectiveMinAgeMatchesSQL`. Change one, change both.

**Explicit DTOs, never serialized models.** Models carry password hashes, token
hashes, `min_age` internals and filesystem paths. A path in a response leaks
the server's layout.

**Comments must be earned.** "Why", not "what". No war stories about what the
code used to be. Prefix a phase reference with the plan version (`v1 phase 9`).

**Migrations are forward-only**, embedded, applied in filename order inside a
transaction. A bad migration is corrected by a later one, never rolled back.

## Important Context

- **Bard must be safe to expose to the internet.** Remote access over cellular
  is a day-one requirement. That is why auth, rate limiting and trusted-proxy
  handling landed in phases 1–2 rather than being added later.
- **`trusted_proxies` is security-critical and fails silently both ways.** Too
  permissive lets a caller spoof `X-Forwarded-For` past login rate limiting;
  too restrictive behind a tunnel collapses every client into one
  `127.0.0.1` bucket. Every deployment recipe must state its value.
- **One library, many sources.** "Library" means the whole collection, which is
  what users browse. A *source* is a directory root, admin-only plumbing that
  carries `default_min_age` and `hide_when_locked`. Never call a source a
  library.
- **Book → Edition → File.** `Book` is what you browse, `Edition` is a
  manifestation (the EPUB, a particular narration, an abridgement), `File` is
  bytes. Series sits above Book; contributors attach to a book (author) or to
  one edition (narrator).
- **No transcoding, ever.** M4B is AAC-in-MP4 and plays natively; `ffprobe` is
  an optional fallback for chapter extraction, never a hard dependency.
- **Bard never renames or moves existing files.** The only place it writes media
  is upload ingest.
- **User spaces are private.** No endpoint exposes one user's progress, ratings
  or annotations to another, including to an admin.

## Project Structure
```
cmd/bard/           entrypoint and CLI verbs (serve, scan, user, export)
internal/
  config/           YAML load, defaults, validation
  db/               open, embedded migrations, query builder, FTS, age-gate SQL
  model/            Source, Book, Edition, File, Series, Contributor, User, ...
  policy/           Result/Reason, messages, per-resource policies and scopes
  auth/             argon2id, device tokens, rate limiting, user/device store
  api/              handlers, middleware, DTOs
scripts/dev         local server control
docs/deploy/        deployment recipes
```

## Helper Scripts
Scripts in `scripts/` are reusable helpers. **Before writing repetitive bash
commands:**
1. Check if a script already exists in `scripts/`
2. If not, consider creating one for sequences you'll run again

This reduces permission prompts and ensures consistency.

`scripts/dev` controls the local server: `build`, `start`, `stop`, `restart`,
`status`, `logs`, `reset`. It tracks the process by PID file and verifies
`/proc/<pid>/comm` before signalling — do not use `pgrep` to find the server,
because the pattern matches the shell running the script and a stop can kill
its own caller.
