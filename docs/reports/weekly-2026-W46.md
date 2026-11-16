# Weekly Report - bardbookserver - Week of 2026-11-09

> ISO week 2026-W46 (Mon 2026-11-09 – Sun 2026-11-15). Work landed on the 12th
> and 13th. No daily reports exist for this week, so the narrative below is
> drawn from `PROJECT_PROGRESS.md`, `DECISIONS.md` and the commit history.

## Week Overview

Bard went from nothing to a server that indexes a real library from disk. The
week covered planning and the first five phases of the twelve-phase plan: the
foundation, auth and the policy core, the library schema, the scanner with its
embedded metadata parsers and sidecars, and automatic grouping of editions into
books. By the end of it a cold scan of the sample library produced six books
from seven editions, with Red Rising's M4B and its 47-part MP3 set correctly
sitting on one book as two editions.

The order was deliberate: auth and trusted-proxy handling landed in phase 2
rather than being added later, because internet exposure is a day-one
requirement and both failure modes are silent.

## Key Accomplishments

### Foundation and packaging
- Config loading with defaults and validation; a missing config file still
  serves, so an install with no config works
- SQLite opened with the pragmas Bard relies on, plus embedded forward-only
  migrations applied in filename order inside a transaction
- HTTP server with graceful shutdown, and `scripts/dev` for
  build/start/stop/restart/status/logs/reset, tracking the process by PID file
  rather than `pgrep`
- Trusted-proxy middleware reading `X-Forwarded-For` only from an allowlisted
  CIDR set, with the rightmost untrusted hop winning

### Auth and authorization
- Argon2id passwords and revocable opaque per-device tokens, looked up on every
  request so revocation is immediate
- Managed profiles derived from `owner_id`, with a PIN gating the switch back
  into the owner's profile
- Login and PIN rate limiting
- The `internal/policy` package ported from the user's
  `rails/policies/advanced` pattern: structured `Result` over bool,
  `allow`/`deny` helpers only, unexported boolean role helpers, single-point
  message resolution
- Fixed an age bug where a day-of-year comparison delayed birthdays across leap
  years

### Library schema
- Book → Edition → File with Series above and Contributors across, plus Genre,
  collections and an FTS5 index
- A query builder that lets policy scopes, facet filters, sorts and pagination
  each add clauses without knowing about the others
- `EffectiveMinAge` in Go with a SQL twin, held together by a fixture test

### Scanner and embedded metadata
- Durable job queue in SQLite with a bounded worker pool as the compute
  throttle; claiming is one `UPDATE ... RETURNING` so two workers cannot take
  one job
- Directory walker, format detection and the disk-to-index reconciler, with
  `bard scan`
- Pure-Go EPUB, PDF, M4B and MP3-folder parsers, validated against the real
  sample set
- Content-addressed cover cache, so the M4B and MP3 editions of one book share
  a cached file
- `metadata.Resolve` as the single precedence function, with `locked_fields`
  honoured per field so a hand correction survives a rescan
- `bard.json` sidecars per directory, keyed by edition, plus
  `Author/Series/## - Title` path parsing with author un-inversion
- A clean rescan writes nothing — asserted on the result ledger, on the rows,
  and on sidecar mtimes

### Grouping
- Tiered matcher — identifier, then title plus author, then subtitle-stripped —
  recording a confidence per edition and queueing anything below 0.9
- Manual merge and split, pinned by a group key stored in the sidecar so a
  correction outlives the database being deleted
- Editionless books reaped

## Decisions This Week

Seventeen decisions were logged. The ones that shaped the most code:

1. **Pure-Go SQLite** (`modernc.org/sqlite`) — accepts 2–3× slower writes to get
   a genuinely static binary and a trivial cross-compile → `CGO_ENABLED=0`
   everywhere; reversible in one line if a benchmark justifies it
2. **No policy registry in the Go port** — the Rails registry guards against
   `.constantize` resolving an attacker-influenced string (CWE-470), and Go has
   no such lookup here → typed constructors only; documented in the package
   comment
3. **Reason coverage is a test, not a convention** — the Rails pattern degrades
   silently to "Not authorized" when a reason has no message → adding a
   `Reason` without a message breaks CI
4. **Effective min age inherits from editions, not "the book's source"** — a
   book has no single source, since `source_id` lives on editions → takes the
   most restrictive, and the SQL twin needs `COUNT(*) = COUNT(default_min_age)`
   because `MAX` ignores NULLs and would fail open
5. **Managed profiles derive from `owner_id`; PIN gates the switch back** — one
   fewer table and no way for the list to drift → phase 9's age gating is only
   as strong as the owner's PIN
6. **Shutdown requeues a job for free; a crash charges it an attempt** — a
   SIGTERM is not the job's fault, but a job that killed the process must
   eventually park → `Release` and `Reclaim` are separate operations with
   deliberately different arithmetic
7. **MP3s group on the set, M4Bs never group** — a chapter-per-file audiobook
   and a drop folder are identical file by file, and the two formats need
   opposite defaults → two accepted misfilings, both fixable by a manual merge
8. **Covers are content-addressed, JPEG only** — dedup falls out for free, which
   the sample library proves worthwhile → orphaned covers are not collected yet
9. **MP3 duration comes from the VBR header, not from summing frames** — a cold
   scan of 5000 titles cannot read every byte → exact where a Xing count
   exists, otherwise ~0.04s long per file
10. **Narrator comes from the composer tag, not the album artist** — album artist
    is ambiguous and would file some narrators as authors → narrators stay empty
    until phase 8, which is the right outcome: an empty field is recoverable
11. **The sidecar records decisions, and only locked fields outrank the file** —
    a sidecar that simply won would freeze the library against parser
    improvements → consulted twice, at the highest and lowest precedence
12. **One keyed sidecar per directory, not one per edition** — the plan's
    assumption only holds under the path convention, and flat layouts are
    supported too → entries for vanished editions are pruned on the next scan
13. **A leading number is a series position only inside a series folder** —
    found while testing: `Marquez/100 Years of Solitude.epub` parsed as position
    100 → the ordinal is conditional on there being a series directory
14. **A differing ISBN does not prevent a group** — an ISBN identifies a
    manifestation, not a work, so the planned veto would have broken the two
    groupings the same checklist demanded → precision comes from title plus
    author
15. **Transactions begin immediate** (`_txlock=immediate`) — a deferred
    transaction that reads first cannot upgrade, and WAL reports that as
    `SQLITE_BUSY` without waiting → writers serialize, which at this scale is
    wanted anyway
16. **`bard source add` lands in phase 4** — sources live in the database, so
    without the verb `bard scan` has nothing to scan
17. **Default port 2273 kept** despite an IANA assignment to a MySQL tool
    removed in 2010

## Challenges Encountered

- **An intermittent `SQLITE_BUSY` under `-race`.** Phase 5 made the scanner read
  before it writes, and roughly one run in ten failed with "database is
  locked". Worth chasing rather than re-running: a deferred transaction cannot
  upgrade to a writer once another connection has written, and it would have
  failed in production as soon as phase 7 added progress writes alongside a
  scan. Resolved with `_txlock=immediate`.
- **A contradiction inside the phase 5 checklist.** One validation line asked
  for differing ISBNs to prevent a group; the two lines above it asked for
  exactly the groupings that rule would break. Resolved by dropping the veto
  and documenting why.
- **A leap-year age bug** in the day-of-year birthday comparison, found and
  fixed in phase 2 before anything depended on it.
- **Path parsing over-reaching**, turning `100 Years of Solitude` into position
  100 of an unnamed series. Fixed by making the ordinal conditional on a series
  directory; the three-digit cap alone was not enough.
- **Narrators and genres left empty** at the end of the week. Not a bug — the
  standard tags are absent from the samples and the taxonomy does not exist
  yet — but it leaves two facets with no values until phase 8.

## Metrics

- Commits: 10 (2 initial, 8 phase commits)
- Files changed: 99, +14,323 / -22 lines
- Go files at week's end: 84
- Test functions at week's end: 189
- Architecture decisions logged: 17
- Migrations: 7 (`0001_init` through `0007_grouping`)

## Next Week Priorities

1. **Phase 6 — read API and media delivery.** Browse, search, detail and
   streaming, with every multi-record path composing through one `BookScope` so
   phase 9's age gating is a change in one function.
2. **Phase 7 — user spaces.** Progress, reading state, ratings, the saved shelf,
   history, annotations and the read-to-listen handoff.
3. **Phase 8 — metadata providers and taxonomy.** The unblock for the empty
   narrator and genre facets, and the first thing that is independent of the
   critical path.
