# Bard: Plex for ebooks and audiobooks

> **Status:** Planning complete | Last updated: 2026-11-12
>
> Phase files: [phases/](phases/)

## Overview

Bard is a self-hosted media server for ebooks and audiobooks. `bardbookserver` is the server half: a single static Go binary that indexes a directory of book files, enriches them with metadata, and serves them to clients over an authenticated HTTP API. Native iOS and Android apps will follow as separate projects.

The immediate motivation is getting a personal ebook library off iCloud and onto a family home server, with DRM-free audiobooks joining it shortly. The server runs alongside an existing Plex install on a 16GB i7-7700, so a small resident footprint and conservative CPU use are hard requirements, not aspirations.

Bard borrows Plex's shape — self-hosted, files stay on disk, one server many clients, no cloud account — and deviates in two places. First, every user gets a **private space**: their own shelves, progress, ratings, and annotations, invisible to other users, set against a shared maximalist library they can browse freely. Second, Bard models the **book** as the thing you browse and the **edition** as the thing you consume, so the ebook and the audiobook of the same title appear as one card with two ways to open it.

## Core Vision

- **Small and quiet** — one static binary, no runtime dependencies, no transcoding, bounded background work. Bard is the light tenant on a box that is already doing other jobs.
- **Files stay yours** — the library on disk is authoritative. Bard never renames or moves files, writes metadata as sidecars beside the media, and can rebuild its entire index from a scan.
- **Private spaces, shared library** — one big library everyone browses; what you're reading, what you rated, and what you highlighted is yours alone.
- **One book, many editions** — format is a property of the edition, never of the thing you browse.
- **Safe to expose** — remote access over cellular is a day-one requirement, so the server is built to sit on the open internet from Phase 1 rather than hardened later.

## Requirements

### Must Have

- Scan one or more **sources** (directory roots) into a single unified library; detect EPUB, PDF, M4B, and folder-of-MP3 audiobooks
- Extract embedded metadata (EPUB OPF, MP4/ID3 tags), cover art, and audiobook chapter timings
- Write metadata sidecars beside the media (per-source toggle); never rename or move files
- Group editions into books automatically on normalized title + author, assisted by ISBN/ASIN
- Manual merge/split of groupings, persisted so a rescan cannot undo a correction
- Series (ordered, from metadata), server collections (admin-curated), user collections (private)
- Per-server local accounts, no cloud identity. Argon2id passwords
- Revocable per-device opaque tokens with a visible authorized-devices list
- Managed users (no password, optional PIN) for profile switching on shared family devices
- Age-gated access, with per-source defaults, per-book overrides, and per-user exceptions
- Faceted filtering across the whole library: genre, format, author, narrator, series, language, length, reading state
- Normalized genre taxonomy mapped from provider subject strings, with unmapped subjects kept as raw tags
- Private per-user state: reading progress, reading state, ratings, saved shelf, history, annotations
- Cross-device progress sync with explicit divergence surfacing (never silent last-write-wins)
- Chapter-level and proportional cross-edition position handoff (read → listen)
- HTTP range requests on all media; a per-user sync manifest telling clients what to pre-cache
- Online metadata lookup (Open Library, Google Books, Audnexus), off by default, with Fix Match and field locking
- Resumable upload from a phone or tablet, admin-only
- Embedded web admin UI covering setup, sources, users, devices, match fixing, and the rating queue
- Installable from a public GitHub repo: static binaries for amd64/arm64, systemd unit, Docker image

### Nice to Have

- Suggestions driven by ratings and history
- Full-text search inside book contents (metadata search only in v1)
- mDNS/LAN discovery
- Comics/CBZ as an additional edition format

### Out of Scope

- **Audio or ebook transcoding** — M4B is AAC-in-MP4 and MP3 is universal; both play natively on iOS and Android at 32–64kbps. There is nothing to transcode and adding it would spend the compute budget we are protecting.
- **Forced alignment / word-level read-listen sync** — the precise version of cross-edition sync needs ASR over every audiobook, which is hours of CPU per title. Explicitly dropped, not stubbed; adding it later is a migration.
- **Relay or NAT traversal** — Plex Relay works because Plex Inc. pays for egress. Bard documents Cloudflare Tunnel, reverse proxy, and Tailscale instead.
- **Cloud accounts** — accounts are per-server. Avoiding a hosted identity service is most of the point.
- **OPDS catalog feed** — its value was third-party and e-ink clients; nobody in the household uses either. Deferred, not designed out.
- **Cross-user visibility** — no shared shelves, no recommendations between family members. Spaces are private.
- **Audio splitting for omnibus files** — single files containing several books get split externally before import.
- **Apple Books import tooling** — the initial migration is a manual file copy.
- **Client-side rendering concerns** — pagination, blur-on-locked, themes and playback UI belong to the apps. The server provides data.

## Constraints

- **Hardware**: shares an i7-7700 / 16GB host with Plex. Target idle footprint ~50–100MB RSS and bursty, throttled background work.
- **Scale**: ~5000 titles, a handful of family users, 3–4 concurrent listeners. Well inside SQLite's comfort zone.
- **Tech stack**: Go, pure-Go dependencies only, no cgo, no Node build step for the admin UI.
- **Network**: must be safe to expose to the internet; must work behind a proxy that terminates TLS and originates every request from localhost.
- **Team**: one developer, sessions assisted by Claude. Phases are sized for a single session.
- **License**: MIT.

## Success Metrics

- Single `go build` produces a static binary; cross-compiles to linux/arm64 with no toolchain
- Cold scan of 5000 titles completes without starving co-resident Plex (throttled, bounded workers)
- Idle RSS under 150MB with the library loaded
- Metadata match rate above 80% unattended, with Fix Match covering the rest
- Full index rebuildable from disk after deleting the database, with manual corrections intact
- Reading position survives a phone → tablet handoff with no lost progress
- An age-restricted account cannot reach a restricted book through *any* read path, verified by test

## Architecture Decisions

### 1. Go with pure-Go dependencies only

**Choice:** Go, standard-library HTTP routing, and `modernc.org/sqlite` over the cgo-based `mattn/go-sqlite3`.

**Rationale:** `GOOS=linux GOARCH=arm64 go build` produces a genuinely static binary with no C toolchain, which makes the GitHub Releases matrix trivial and lets contributors build with nothing but Go installed. Go 1.22+ `http.ServeMux` handles method and wildcard routing, so no router dependency is needed.

**Trade-offs:** pure-Go SQLite is roughly 2–3× slower on writes than the cgo build. Writes occur during scans and progress updates; a 40-second scan becoming 100 seconds is not observable. Both are `database/sql` drivers, so this is a one-line reversal if a benchmark ever justifies it.

### 2. Book → Edition → File, with Series above and Contributors across

**Choice:** three levels. **Book** is the browsable title. **Edition** is a manifestation — the EPUB, the Jim Dale narration, the Stephen Fry narration, a large-print PDF. **File** is bytes on disk, possibly many per edition for chapter-per-MP3 audiobooks. **Series** sits above Book (ordered, one per book, from metadata). **Contributors** attach with roles (author, narrator, translator, illustrator).

**Rationale:** this is Plex's Movie → Media → Part with better names. Naming the top level `Book` rather than `Work` avoids library-science jargon, reads the way people speak ("this book has two editions"), and dodges the `title.title` field collision that `Title` would create. Edition as the general axis means two narrations of one book are representable; making format the only axis would break on the first duplicate narration.

**Trade-offs:** one more join than a flat model, and auto-grouping becomes a thing that can be wrong — hence mandatory merge/split.

### 3. Disk is authoritative; SQLite is a rebuildable index

**Choice:** library metadata lives in sidecar files beside the media (per-source toggle) and is mirrored into SQLite, which also holds all user state. Bard writes sidecars and ingests uploads but never renames or moves existing files.

**Rationale:** contrary to common belief, Plex keeps a SQLite database and a blob cache in its application-support directory rather than metadata beside the media; its portability comes from that directory being copyable. Sidecars improve on it — metadata survives a lost database and travels with the files to another machine. SQLite remains necessary as the queryable index and as the only sane home for progress, shelves, and ratings.

**Trade-offs:** writing into media directories is a real intrusion, so it is per-source and opt-out. Two stores means a sync path to keep correct, with the scan as the reconciler.

### 4. Policy objects with reason codes, ported from the Rails advanced policy pattern

**Choice:** one policy type per resource, action methods returning `Result{Allowed, Reason, Meta}` rather than a bare bool, built only via `allow()`/`deny()` helpers. Unexported boolean role helpers. An explicit policy registry — never reflection-based class lookup. Message resolution in exactly one place. A `Scope` contributing SQL `WHERE` clauses for every listing path.

**Rationale:** adapted from `rails/policies/advanced` and the `Scope` half of `rails/policies/basic` in the user's pattern library, which exist because scattered permission checks are unmaintainable and generic denials are unhelpful. Bard needs the advanced variant specifically: age gating produces materially different denials ("you can read this at 11", "a parent blocked this one", "unrated — ask a parent") and the client must distinguish them.

Three upgrades in the port:

1. Reason codes are typed Go constants, and a test enumerates them to assert every one has a message. The Rails pattern notes a missing case degrades silently to "Not authorized"; in Go that becomes a test failure.
2. **Visibility is a query condition, never a post-filter.** Search is the classic leak — hidden from browse, present in results — and a post-filter also breaks pagination.
3. **No reason code for content the user shouldn't know exists.** Scope filtering yields absence (404); reason codes only ever describe things already visible.

**Trade-offs:** one registry line per new resource, and a denial path slightly more verbose than `if !ok`.

### 5. One library, many sources — and the source carries the age default

**Choice:** there is exactly one **library**: the whole collection, which every user browses. A **source** is a directory root Bard scans, and it is admin-only plumbing that no browsing user ever sees. Each source carries its path, its sidecar toggle, a `default_min_age`, and a `hide_when_locked` flag. Books inherit `min_age` from their source unless set explicitly.

**Rationale:** Plex's "library" conflates four separate things — a directory root, a content-type declaration, a permission unit, and a browsing section — and that conflation is a known annoyance there. Bard separates them: **sources** handle storage and policy defaults, the **library** is what users browse, and **collections** are the user-facing grouping. Multiple sources are needed regardless for mundane reasons: audiobooks are large and may live on a different disk from ebooks, drives get added, and one tree may want sidecars while another is read-only.

Hanging `default_min_age` on the source is what makes age gating cheap. Dropping adult fiction under a source with `default_min_age: 18` restricts thousands of titles **without rating a single book individually**. The storage decision and the access decision collapse into one natural act: where you put the file.

**Trade-offs:** inheritance means moving a file between sources can change its restriction, which needs to be visible in the admin UI rather than silent.

### 6. Age on the book, birth date on the user

**Choice:** `users.birth_date` plus a `restricted` flag. A restricted user may read a book when its effective `min_age` (explicit, else inherited from its source) is set and is `<= age`. Per-user allow/deny overrides handle exceptions. An effective `min_age` of `NULL` denies.

**Rationale:** books have no universal content rating — Google Books' `maturityRating` is binary and spotty, and BISAC codes are absent on anything from Project Gutenberg. So an auto-populated rating cannot be the gate, because a missing rating would fail *open*.

Storing a birth date rather than an age is what makes this maintenance-free: age is computed at query time, so a 5-year-old becomes 11 and age-appropriate books unlock with no admin action ever. Putting the rating on the *book* rather than on the user-book relationship means rating a title once applies it to every current and future child — the "first child is the pioneer, the others inherit" goal, achieved by having nothing to inherit.

Source inheritance also makes the fail-closed `NULL` path nearly unreachable: an unrated book takes its source's default rather than vanishing, so the only way to hit it is a source with no default, which the admin UI requires. The policy still handles it defensively.

**Trade-offs:** titles that differ from their source's default still need individual attention. Mitigated by provider-suggested ages (BISAC / `maturityRating`) surfaced as a bulk-confirm queue — **suggested, never auto-applied**, for the fail-open reason above.

### 7. Visible-but-locked, hidden per source

**Choice:** books above a restricted user's age appear in listings annotated `locked` with their unlock age, sorted below readable ones, and can still be saved to a user's space — where they unlock on their own. A source with `hide_when_locked` set removes them from that user's listings entirely.

**Rationale:** two behaviors for two situations. The general source shows locked books, which gives an eleven-year-old something to look forward to rather than a book that does not exist. The adult source hides them, so a five-year-old is not scrolling past four thousand locked titles. Putting the switch on the source means it follows the same decision as the age default.

Requires `unlocked` to be a **SQL-computed** sort key rather than a Go-side annotation, since sorting and pagination both depend on it, and requires the policy to split into `View` / `Read` / `Save`.

### 8. Faceted filtering over a normalized genre taxonomy

**Choice:** a canonical genre list plus a mapping table from provider subject strings, with unmapped subjects retained as raw tags rather than discarded. Facets span the entire library: genre, format, author, narrator, series, language, publication year, length bucket, personal rating, reading state, and unlocked status.

**Rationale:** the single-library model is what makes this possible. Plex's sections trap taxonomy inside them; one unified library means "unabridged fantasy audiobooks over 15 hours I haven't started" is a single query across every source. This is a direct payoff of Decision 5, not an incidental feature.

Normalization is the real work, because provider subject data is filthy: the same book returns `FICTION / Fantasy / Epic`, `Fantasy fiction`, `fantasy`, and `Juvenile Fiction / Fantasy & Magic` depending on source. Keeping unmapped subjects as raw tags means a mapping gap degrades to a less useful facet rather than losing data.

Useful overlap: BISAC prefixes (`JUV`, `YAF`) are simultaneously the genre signal and the suggested-age signal, so the taxonomy parser and the age-suggestion queue in Decision 6 share one implementation.

**Trade-offs:** the canonical list is a maintained artifact, and the mapping table will need additions as new providers or oddly-tagged books arrive. Facet filters must compose with the policy scope, so filtering and authorization share a query builder.

### 9. Opaque device tokens, not JWTs

**Choice:** a random 256-bit token per device, stored hashed alongside user, device name, last-seen IP, and creation time. Looked up on every request.

**Rationale:** the feature wanted is Plex's authorized-devices screen — see every signed-in device, revoke one instantly. A JWT cannot be revoked; faking it needs a blocklist checked per request, which is the database lookup JWTs exist to avoid. In SQLite that lookup is microseconds at this scale, so the stateless advantage buys nothing and costs revocation.

**Trade-offs:** a database read per request, and token hashes to rotate if the store leaks.

### 10. Managed users rather than a separate profile entity

**Choice:** every person is a `User` with a private space. A managed user is a `User` with no password, enterable only from a device already authenticated as its owner, with an optional PIN. A guest is a managed user whose data can be wiped.

**Rationale:** the Plex Home model. Introducing a distinct "profile" concept would need its own progress, shelves, ratings, and annotation tables — duplicating everything for no gain. Whether a shared device shows a picker or auto-selects is a client setting, but the server must support a device token switching between a declared set of managed users without re-auth.

### 11. No transcoding, no ffmpeg dependency

**Choice:** serve original bytes with HTTP range requests. Pure-Go parsers for EPUB, PDF, MP4 and ID3; `ffprobe` used only as an optional fallback for chapter extraction on files pure Go cannot read.

**Rationale:** Plex transcodes because video codecs vary, clients are picky, and 4K does not fit through hotel wifi. None transfers: M4B is AAC-in-MP4 and plays natively everywhere, MP3 is universal, and audiobook bitrates make bandwidth a non-issue. Four concurrent streams is ~256 kbps total. A default install should need nothing but the binary.

### 12. Chapter-level and proportional cross-edition handoff only

**Choice:** match EPUB nav/TOC entries to M4B chapters by order and title similarity, then interpolate proportionally within a chapter. Fall back to percentage-of-book when chapter counts disagree.

**Rationale:** both artifacts already exist — chapter data is extracted regardless, because the audio player needs it for navigation. So the handoff reads load-bearing data and adds nothing speculative. Error inside a chapter is roughly ±1–2 minutes, which is not annoying. The precise alternative is forced alignment, explicitly out of scope.

**Trade-offs:** fails on audiobooks that merge or split chapters relative to print, or count front matter as chapter one. The user can nudge.

### 13. Transport-agnostic remote access, with trusted-proxy handling in Phase 1

**Choice:** Bard serves plain HTTP by default, optionally terminates TLS itself via built-in ACME, and reads forwarded headers **only** from an explicit trusted-proxy CIDR allowlist. Three documented deployment recipes; Cloudflare Tunnel is the recommended default.

**Rationale:** Cloudflare Tunnel makes an outbound connection and forwards to Bard over localhost HTTP, terminating TLS at Cloudflare's edge — so Let's Encrypt is neither possible (no inbound path for ACME challenges) nor needed there. Built-in ACME serves the port-forward case instead. Two caveats to document: Cloudflare's self-serve terms discourage proxying large volumes of non-HTML content, and Cloudflare sees the traffic.

The forwarded-header allowlist is Phase 1 and not negotiable. Behind a tunnel every request arrives from `127.0.0.1`, so rate limiting on the remote address would put the whole family and any attacker in one bucket — while blindly trusting `X-Forwarded-For` lets an attacker spoof it to evade limits entirely. Both failure modes are silent and miserable to retrofit.

### 14. In-process durable job queue

**Choice:** a `jobs` table in SQLite with a bounded in-process worker pool and configurable concurrency. No Redis, no external broker.

**Rationale:** scanning, metadata fetching, and cover extraction must survive a restart mid-run and must never saturate a box also running Plex. A bounded pool with a configurable worker count is the throttle that protects the compute budget.

### 15. Server-dumb offline sync

**Choice:** range-requestable original files, ETags, plus a per-user sync manifest naming what a device should hold (active reads, next in series, saved shelf). Clients decide what to cache and evict. No server-side expiry or download caps.

**Rationale:** only the client knows its storage, battery, and network state. And unlike Libby there is no licensing to enforce — these files are owned outright, so there is nothing to revoke. Keeps server cost at effectively zero.

## Project Structure

```
bardbookserver/
├── cmd/bard/                  # main; CLI verbs (serve, scan, user, export)
├── internal/
│   ├── config/                # YAML config load, defaults, validation
│   ├── db/                    # sqlite open, embedded SQL migrations, queries
│   ├── model/                 # Source, Book, Edition, File, Series, Contributor, Genre, User, ...
│   ├── policy/                # Result/Reason, registry, messages, per-resource policies + scopes
│   ├── auth/                  # argon2id, device tokens, managed users, rate limiting
│   ├── scan/                  # directory walk, format detection, naming fallback, reconcile
│   ├── metadata/
│   │   ├── embedded/          # epub (OPF), pdf, mp4/m4b, id3
│   │   ├── sidecar/           # read/write bard.json beside media
│   │   └── provider/          # openlibrary, googlebooks, audnexus + match scoring
│   ├── grouping/              # normalization, auto-group, merge/split persistence
│   ├── taxonomy/              # canonical genres, BISAC/subject mapping, facet definitions
│   ├── media/                 # range serving, cover cache, chapter maps, handoff
│   ├── space/                 # progress, reading state, ratings, shelves, annotations
│   ├── jobs/                  # durable queue + bounded worker pool
│   ├── api/                   # handlers, middleware, DTOs
│   └── web/                   # admin UI templates + embedded assets (htmx)
├── docs/
│   ├── plan/                  # this plan
│   └── deploy/                # cloudflare-tunnel, reverse-proxy, tailscale recipes
└── .github/workflows/         # build, test, goreleaser
```

### Key Files

- `cmd/bard/main.go` — entrypoint and CLI dispatch
- `internal/config/config.go` — the single source of defaults, including port 2273 and `tls: off`
- `internal/db/migrations/*.sql` — embedded, applied in order against a version table
- `internal/policy/policy.go` — `Result`, `Reason` constants, registry, `Message()`
- `internal/policy/book.go` — `BookPolicy.View/Read/Save` and `BookScope`, where age gating lives
- `internal/api/middleware.go` — trusted-proxy parsing, auth, rate limiting
- `internal/scan/scanner.go` — the reconciler between disk and index

## Core Interfaces

### Authorization contract

Every policy action and any subsystem participating in a decision returns the same shape.

```go
type Reason string

const (
    ReasonExplicitlyDenied Reason = "explicitly_denied"
    ReasonBelowMinAge      Reason = "below_min_age"
    ReasonUnrated          Reason = "unrated"
    ReasonNotAdmin         Reason = "not_admin"
    ReasonUploadNotAllowed Reason = "upload_not_allowed"
)

type Result struct {
    Allowed bool
    Reason  Reason
    Meta    map[string]any // e.g. {"required_age": 11}
}

func allow() Result                                 { return Result{Allowed: true} }
func deny(r Reason) Result                           { return Result{Allowed: false, Reason: r} }
func denyWith(r Reason, m map[string]any) Result     { return Result{Allowed: false, Reason: r, Meta: m} }
```

Action methods chain early returns and never construct a `Result` inline:

```go
func (p BookPolicy) Read() Result {
    if p.deniedByOverride()  { return deny(ReasonExplicitlyDenied) }
    if p.allowedByOverride() { return allow() }
    if !p.user.Restricted    { return allow() }

    minAge := p.book.EffectiveMinAge() // explicit, else inherited from its source
    if minAge == nil         { return deny(ReasonUnrated) }
    if p.user.Age() < *minAge {
        return denyWith(ReasonBelowMinAge, map[string]any{"required_age": *minAge})
    }
    return allow()
}
```

Note there is no reason code for a book hidden by its source's `hide_when_locked`. Hiding is the scope's job and yields absence, per the third upgrade in Decision 4 — a reason code would confirm the book exists.

### Listing scope contract

Every read path that returns more than one record composes its query through a scope. This is the mechanism that prevents a restriction leak via search or pagination.

```go
type Scope interface {
    // Apply contributes WHERE/ORDER clauses for what this user may see.
    // Locked rows are annotated and sorted below readable ones; rows from a source
    // with hide_when_locked are removed outright.
    Apply(q *db.Query, u *model.User) *db.Query
}
```

Facet filters from Decision 8 compose onto the same query builder, so a filtered listing cannot bypass the scope.

### Metadata provider contract

```go
type Provider interface {
    Name() string
    Search(ctx context.Context, q Query) ([]Candidate, error)
    Fetch(ctx context.Context, id string) (*Metadata, error)
}
```

Providers are registered explicitly, individually toggleable, off by default, and expected to fail — a provider error degrades to embedded metadata rather than failing a scan.

### Progress contract

Progress is keyed on `(user, edition)`, never `(user, book)`: the ebook position and the audio position are genuinely different places.

```go
type Position struct {
    EditionID  int64
    CFI        *string  // EPUB canonical fragment identifier
    Page       *int     // PDF
    Seconds    *float64 // audio
    FileIndex  *int     // multi-file audiobooks
    Percent    float64  // always populated; the cross-format fallback
    ClientSeq  int64    // client monotonic counter
    UpdatedAt  time.Time
}
```

Divergence is surfaced, never resolved silently: the API returns all known positions for an edition so the client can offer "your iPad is further ahead — jump there?". Annotations are append-only with soft deletes and a sync cursor, so two offline devices merge instead of colliding.

## Implementation Phases

| Phase | Name | Scope | Depends On | Key Outputs |
|-------|------|-------|------------|-------------|
| 1 | Foundation | Config, SQLite + migrations, HTTP server, trusted-proxy middleware, CLI skeleton | — | Static binary that serves `/healthz` and owns a database |
| 2 | Auth & policy core | Users, Argon2id, device tokens, managed users, login rate limiting, policy package | 1 | Login, revocable devices, `Result`/`Reason` with coverage test |
| 3 | Library schema | Source, Book/Edition/File, Series, Contributor, Genre, collections, FTS5 | 1 | Migrations and models, no scanning yet |
| 4 | Scanner & embedded metadata | Walk, format detection, EPUB/PDF/M4B/MP3 parsing, covers, chapters, sidecars, job queue | 3 | A real library indexed from disk |
| 5 | Grouping | Normalization, auto-group into books, merge/split persisted to sidecar | 4 | Ebook + audiobook of one title as one book, two editions |
| 6 | Read API & media delivery | Browse/search/detail through scopes, facet filtering, range requests, cover cache, sync manifest | 2, 5 | Clients can list, filter, and stream |
| 7 | User spaces | Progress, reading state, ratings, saved, history, collections, annotations | 6 | Private per-user state syncing across devices |
| 8 | Metadata providers & taxonomy | Open Library, Google Books, Audnexus, match scoring, Fix Match, field locking, genre normalization | 4 | Unattended enrichment, manual correction, usable facets |
| 9 | Kids & age gating | birth_date, min_age, overrides, View/Read/Save split, unlocked-first sort, suggestion queue | 6, 8 | Age-restricted accounts, verified leak-free |
| 10 | Upload | Resumable chunked upload, ingest, admin-only permission | 4, 6 | Add a book from a phone |
| 11 | Web admin UI | Setup wizard, sources, users/devices, Fix Match, rating queue, genre mapping, job status | 8, 9, 10 | Install and configure without SSH |
| 12 | Packaging & release | goreleaser, static binaries, systemd, Docker, deploy recipes, backup/export | 11 | Installable from a public repo |

### Critical Path

**1 → 2 → 3 → 4 → 5 → 6 → 7** is strictly sequential and carries the core product. Phase 2 lands early on purpose: internet exposure is a stated requirement, so auth, rate limiting, and trusted-proxy handling are foundations rather than hardening.

Parallelizable once Phase 6 exists: **8** (providers) is independent of **7** (spaces) and **10** (upload). **9** wants 8 for suggested ages but can be built and tested against hand-set ratings, so it need not block on it. **11** consumes 8, 9, and 10 and should come after them; **12** is last.

Phases 2, 4, and 7 are the largest and carry sub-phases in their files.

### Phase Details

- [Phase 1: Foundation](phases/phase-1.md)
- [Phase 2: Auth & Policy Core](phases/phase-2.md)
- [Phase 3: Library Schema](phases/phase-3.md)
- [Phase 4: Scanner & Embedded Metadata](phases/phase-4.md)
- [Phase 5: Grouping](phases/phase-5.md)
- [Phase 6: Read API & Media Delivery](phases/phase-6.md)
- [Phase 7: User Spaces](phases/phase-7.md)
- [Phase 8: Metadata Providers](phases/phase-8.md)
- [Phase 9: Kids & Age Gating](phases/phase-9.md)
- [Phase 10: Upload](phases/phase-10.md)
- [Phase 11: Web Admin UI](phases/phase-11.md)
- [Phase 12: Packaging & Release](phases/phase-12.md)

## Tech Stack

| Category | Choice | Notes |
|----------|--------|-------|
| Language | Go 1.27 | Single static binary; no cgo anywhere |
| HTTP | stdlib `net/http` + `http.ServeMux` | Go 1.22+ method and wildcard routing is sufficient; no router dep |
| Database | SQLite via `modernc.org/sqlite` | Pure Go, FTS5 available. See Decision 1 |
| Migrations | Embedded `.sql` files + version table | Hand-rolled, ~60 lines; avoids a migration framework |
| Search | SQLite FTS5 | Metadata only in v1 |
| Passwords | `golang.org/x/crypto/argon2` (Argon2id) | |
| EPUB | `archive/zip` + `encoding/xml` | Stdlib only — OPF is just XML in a zip |
| PDF | `pdfcpu` | Pure Go; title and page count |
| MP4 / M4B | `abema/go-mp4` | Chapters (`chpl`), tags |
| MP3 / ID3 | `dhowden/tag` | Pure Go |
| Audio fallback | `ffprobe` (optional, external) | Chapter extraction only, never required |
| Images | `golang.org/x/image/draw` | Cover thumbnail variants, cached on disk |
| Config | YAML (`gopkg.in/yaml.v3`) | Familiar to self-hosters; supports comments, unlike JSON |
| Logging | stdlib `log/slog` | |
| Jobs | SQLite table + bounded worker pool | Durable across restarts; the compute throttle |
| Admin UI | Go templates + vendored htmx | No Node, no build step, embedded via `embed` |
| TLS | `golang.org/x/crypto/acme/autocert` (optional) | Only for the direct port-forward case |
| Release | goreleaser + GitHub Actions | linux amd64/arm64, systemd unit, Docker image |
| Default port | **2273** | "BARD" on a phone keypad. Registered range, clear of ephemeral, no collision with common self-hosted services. IANA lists it as `mysql-im` (MySQL Instance Manager, 2004), a tool removed from MySQL in 2010 — accepted, since the port is configurable |

## Future Considerations

Deferred but architecturally supported:

- **OPDS catalog feed** — read-only GET over existing data; the path to e-ink readers if the household ever wants one
- **Suggestions** — ratings and history are being captured from Phase 7 specifically so this has training data
- **Comics / CBZ** — another edition format; the model does not need to change
- **Full-text search inside contents** — a second FTS table; metadata search ships first
- **mDNS / LAN discovery** — a convenience, since a remote hostname is the primary path anyway
- **Nightly SQLite snapshot and `bard export`** — scoped into Phase 12
- **"Request access" on a locked book** — the reason-code plumbing already carries what a request UI would need
- **Per-user upload permission** — the permission check exists in Phase 10; only the default is admin-only
- **Audio splitting for omnibus files** — would need ffmpeg as a hard dependency; handled externally for now

## Open Assumptions

Flagged rather than guessed; cheap to change.

- Sidecar filename is `bard.json`, one per edition directory
- Default on-disk layout convention for the naming fallback is `Author/Series/## - Title/`
- Age-restricted accounts are created by an admin setting `birth_date` and `restricted`; there is no self-service signup
- The canonical genre list is seeded from BISAC top-level fiction/non-fiction headings and trimmed by hand; it is a maintained artifact, not derived at runtime
- Sources live in the database and are managed from the admin UI, not declared in the config file
