# Project Progress - bardbookserver

## Plan Files
Roadmap: [plan.md](../docs/plan/plan.md)
Current Phase: [phase-12.md](../docs/plan/phases/phase-12.md)
Latest Weekly Report: [weekly-2026-W46.md](../docs/reports/weekly-2026-W46.md)

Last Updated: 2026-11-17

## Current Focus
**Phase 12, the last one.** Everything an install needs now moves with it:
`bard export`, `bard import`, `bard snapshot`, and a nightly snapshot that
needs no stored schedule. What remains is how Bard gets onto a machine and
how its operator confirms the deployment is right.

Three shaping decisions, taken 2026-11-17:
- **No goreleaser.** Two `go build` lines in a script, verifiable locally.
- **systemd first, Docker second.** The static binary is the real install.
- **No Cloudflare credential ever touches Bard.** A diagnostics page proving
  `trusted_proxies` is right replaces the tunnel wrapper that was considered.

## Active Tasks
- [DONE] Phases 8, 9, 10, 11 (all four sub-phases)
- [DONE] Phase 12.1: snapshots, export and import
- [DONE] Phase 12.2: build and release scripts, systemd unit, installer,
  Docker image, CI and release workflows
- [DONE] Phase 12.3: `tls: auto` via autocert, and the diagnostics page
- [NEXT] Phase 12.4: README, the three deploy recipes, SECURITY, CHANGELOG

## Test Media
Real books live in `../sample-media`, outside the repo, and never go into it.
Tests that use them skip when the directory is absent, so CI stays green, and
every committed fixture is generated at test time. The set covers three
Gutenberg EPUBs (both nav flavours, no ISBN), two M4Bs with Nero chapters, and
the same audiobook as both an M4B and 47 MP3s — which is what makes the
cross-format duration agreement check possible, and is what the handoff was
verified against.

## Open Questions/Blockers
Nothing is blocked. These are known limitations carried forward.

- **Audnexus cannot enrich the sample audiobooks.** It has no title search, so
  it needs an ASIN, and both sample M4Bs are from Libro.fm and carry none.
  Narrators stay empty for those two files whatever is enabled — they need a
  composer tag or a hand edit. Audnexus works for Audible-sourced files, which
  is where an ASIN comes from.
- The M4B free-form ASIN parser is **unverified against a genuine Audible
  file**, only against a fixture matching the documented atom layout. Worth
  re-checking the first time a real AAX conversion is indexed.
- **Google Books' live response shape is unverified**: the shared keyless quota
  was already exhausted every time it was probed, so the fixtures come from the
  documented shape rather than a recorded response. The breaker handling that
  429 *is* verified live.
- **Open Library pools subjects across every edition of a work**, so The Hobbit
  classifies as fantasy, classics *and* comics — the last from a graphic-novel
  adaptation in the pooled list. Verified against the live service: 40 subjects
  in, 3 genres and 31 raw subjects out. The mapping is right and the source is
  over-broad; locking a book's genres is the per-title fix.
- `raw_subjects.provider` is always NULL. `Resolve` takes one provider's
  subject list wholesale without recording whose.
- The alias table is ~230 entries and will need additions as oddly-tagged books
  arrive. `TestEveryAliasPointsAtACanonicalGenre` catches a typo'd slug, which
  would otherwise fail at scan time rather than in the suite.
- `genres.parent_id` from Phase 3 is unused: the canonical list is flat.
- The EPUB parser produces no page count, but Open Library's
  `number_of_pages_median` now fills it where a provider is enabled, so an
  enriched EPUB lands in a length bucket. Without a provider the EPUBs still
  fall outside every bucket.
- Cover art is not response-cached, only the JSON is. An edition with no
  embedded cover re-downloads its artwork whenever its files change, which is
  rare but not free. The content-addressed cover cache makes the *store*
  idempotent, not the download.
- `metadata.NormalizeLanguage` now folds three-letter codes onto two, which
  changes what Phase 4 produced for a file whose EPUB said "eng". A rescan
  corrects it; there is no migration.
- `grouping.Merge` and `Split` still run several statements without a
  transaction, left from Phase 5. The search-index refresh after them is
  best-effort for the same reason: the regrouping has already committed.
- Progress rows whose device was revoked keep accumulating — they are kept on
  purpose so the reading is not lost, but nothing prunes one that is behind a
  live device's position. Phase 12 maintenance could.
- `db.ReadingStateSQL` interpolates the user id instead of binding it, because
  the expression needs it three times and `db.Query` concatenates arguments
  positionally. Safe as an int64 from the session, but any future facet that
  binds arguments near it needs care.
- A query built without `BookScope.Apply` carries a zero `db.Gate`, which gates
  nothing. Every read path composes through `scopedBooks`, and the leak test is
  what holds that true.
- Annotation positions are stored as the client sends them (CFI, page,
  seconds). Nothing validates that a CFI belongs to the edition it is filed
  under, so a buggy client can store an unresolvable highlight.
- **Initials do not match expanded given names**, so a book Bard knows as
  "L. M. Montgomery" scores 0.30 against Open Library's "Lucy Maud
  Montgomery" — surname agreeing, the rest not — and never matches
  automatically. For a Gutenberg-heavy library that is most books. Fix Match
  settles it in two clicks, but it holds the unattended match rate below the
  plan's 80% target and is worth a scoring follow-up.
- Renaming an entry in `model.fieldNames` silently unlocks that field in every
  sidecar already on disk.
- MP3 durations run ~0.04s long per file where an encoder wrote no gapless
  info. 1.7s over 16 hours, inside the plan's handoff tolerance.
- QuickTime text chapter tracks in M4B are not read, only the Nero `chpl` atom.
  `ffprobe` is the documented fallback, and nothing calls it yet.

## Completed This Week
- [2026-11-17] Phase 12.3: TLS and the diagnostics page
  - `/diagnostics` answers one question — which address rate limiting keys on
    for *this* request — and names the value to set when it is wrong. A page
    rather than a CLI verb, because only a request that came through the
    tunnel can answer it
  - Eight tests cover the verdicts, including both directions
    `trusted_proxies` fails: a proxy in front with nothing trusted, and a
    trusted range so wide it swallows the clients
  - `tls: auto` needs a second listener, since a CA answers TLS-ALPN-01 on 443
    or HTTP-01 on 80 and Bard's default port is neither. `tls.acme_directory`
    makes a staging rehearsal possible without burning a production rate limit
  - autocert's fallback redirect goes to port 443, which is wrong whenever
    Bard serves TLS elsewhere, so the redirect is Bard's own
  - One new module, `golang.org/x/net`, which autocert needs for IDNA. Still
    no cgo
- [2026-11-17] Phase 12.2: build, install and CI
  - `scripts/build`, `scripts/release`, `scripts/install`, `scripts/version`,
    following the hub-core pattern of plain shell over goreleaser
  - **The arm64 artefact is verified, not assumed.** Under qemu-aarch64 it
    applied all 16 migrations, scanned the real library to the same counts as
    amd64, and matched an FTS5 search — Decision 1's pure-Go claim tested
    rather than trusted
  - `release` refuses to ship a dynamically linked binary, since nothing else
    in the build would mention it
  - The installer was run in a clean Debian container: the `bard` system
    account with no supplementary groups ran the unit's exact ExecStart and
    served `/healthz`. Re-running keeps the existing config
  - **Docker needed the data directory in the image, owned by uid 2273.** A
    fresh named volume is seeded from the image including ownership, so
    otherwise it arrives root-owned and the server cannot open its database
  - Docker's NAT is its own `trusted_proxies` trap: a published port makes
    every request arrive from the bridge gateway (`client=172.17.0.1`)
- [2026-11-17] Phase 12.1: snapshots, export and import
  - `bard export` writes a tar.gz of a `VACUUM INTO` snapshot plus the cover
    cache, and deliberately no media; `bard import` refuses a populated data
    directory unless forced
  - A nightly snapshot reads its due time off the snapshot directory listing,
    so a failed night stays due with no state to reset. It polls on a ticker
    because the dedupe index covers `running`, which means a job cannot queue
    its own successor
  - **The cover cache shards by hash prefix, which broke both halves.** The
    export read only the top level and carried zero covers; the restore used
    `path.Base` and would have collided every book's `full.jpg`. Caught by
    exporting the real dev install rather than by a test
  - Snapshots are chmod 0600 — SQLite creates them by umask, and a snapshot is
    a whole copy of the password hashes and device tokens
  - Verified end to end: exported 6 books / 1 account / 14 covers, restored
    into an empty directory, and the restored server accepted the original
    password, served covers from the restored cache, and still had the
    hand-set age rating
- [2026-11-17] Phase 11.4b: Fix Match and the upload form (uncommitted)
  - A match page showing ranked candidates including the ones the automatic
    matcher refused, with a search box for looking up something else entirely
  - Locked fields are listed and unlockable, and **unlocking clears the
    sidecar as well as the index** — a lock left in the sidecar would keep
    outranking the file forever, since the index forgets and a rebuild
    remembers
  - The uploader is about a hundred lines of hand-written JS: `crypto.subtle`
    for the hash, `File.slice` for the pieces, and the CSRF header because a
    cookie-authenticated write needs one. It checks `isSecureContext` and
    explains itself rather than failing on the first click
  - Found by test: clearing a lock as `locked_fields & ~?` overflows SQLite's
    signed integer, since the complement of a FieldSet does not fit. The new
    value is computed in Go
  - **Found live, worth a decision:** Bard has "L. M. Montgomery" where Open
    Library has "Lucy Maud Montgomery". The surname matches but initials
    against expanded given names do not, so the score halves to 0.30 and
    nothing auto-matches. For a Gutenberg-heavy library that is most books.
    Fix Match handles it in two clicks, but Phase 8's unattended match rate is
    lower than the plan's 80% target because of it
  - 498 tests pass, race-clean
- [2026-11-17] Phase 11.4a: the rating queue and genre mapping (committed, fab0f1d)
  - A genre mapping is a rule about strings, not a fact about a book, so there
    is no sidecar to carry it. It joins the other admin decisions already in
    the database and is export-critical for Phase 12
  - Mapping applies to the books already carrying the subject rather than
    waiting for a rescan of 5000 titles, and the subject leaves `raw_subjects`
  - An admin mapping outranks the built-in table, since it exists to correct it
  - Confirming an age is per-row and explicit; a suggestion left alone stays a
    suggestion, and a nonsense age is ignored rather than saved
  - **htmx is not vendored and will not be.** Every page turned out to be a
    plain form, and the one thing needing scripting — `File.slice` plus
    `crypto.subtle` for a chunked upload — is something htmx cannot do
  - 487 tests pass, race-clean
- [2026-11-17] Phase 11.3: sources, accounts and devices (committed, fab0f1d)
  - The source edit form counts the books inheriting its age before you change
    it, which is Decision 5's inheritance made visible rather than silent
  - A moved library is a path edit that rewrites `files.path` in the same
    transaction. Re-adding the source would delete every edition and cascade
    away the progress, ratings and annotations hanging off edition ids
  - Deleting a source is deliberately not offered; disabling is reversible and
    achieves the practical need
  - An admin cannot remove their own admin flag, which would lock the household
    out of the interface with nothing but the CLI to get back in
  - Found while testing: a revoked session on a page navigation got the 401 an
    app would get. It redirects to sign-in and clears the cookie now
  - Verified live: 6 books listed per source, the inheritance notice counting
    6, a managed profile with a PIN and a birth date, the source age raised to
    18, and revoking your own session signing you out
  - 476 tests pass, race-clean
- [2026-11-17] Phase 11.2: the setup wizard (committed, f96c199)
  - Setup state is derived, not stored, so a half-finished wizard resumes where
    it stopped and no flag can be reset by a bad restore
  - The scan is the last page but not a gate: gating on it would trap an
    install that wants to scan later, and a "skip" button is the flag again
  - Path errors are in the terms of someone typing a path, and readability is
    probed rather than inferred from a mode bit
  - TLS is reported rather than written: a form that rewrites `bard.yml`
    destroys the comments YAML was chosen for. `trusted_proxies` gets its
    warning in front of whoever is installing
  - Found while testing: the step redirect ran before authentication, so an
    unauthenticated visitor bounced through a step to reach the sign-in page.
    The three cases are now ordered explicitly
  - Walked against the real sample library: admin, a refused bad path, the
    source, the scan, 6 books / 7 editions / 58 files, refresh stopping on its
    own, and the wizard closed behind it
  - 458 tests pass, race-clean
- [2026-11-16] Phase 11.1: the admin UI shell and its authentication
  (committed, 8a97315)
  - A browser is a device, so there is one revocation surface and a session
    shows up in the authorized list
  - One rule carries the safety: cookie auth needs CSRF on a non-GET, Bearer
    auth never does. Phrased on the source of authentication, not the route,
    so a new route cannot forget it and the apps are untouched
  - `Secure` only where the client actually used TLS, which behind a tunnel
    comes from a trusted proxy's `X-Forwarded-Proto`
  - The setup gate counts users rather than reading a flag, so a bad restore
    cannot reopen an unauthenticated admin-creation endpoint
  - `authenticate` is now shared by the JSON login and the form, so the rate
    limiting and timing equalisation cannot drift apart
  - Verified by neutering the CSRF check and watching the write tests fail
  - 445 tests pass, race-clean
- [2026-11-16] Phase 10: upload (committed, 1335f1f)
  - A chunk is an offset on a boundary, so missing chunks are a set difference
    and a resend is an INSERT OR IGNORE rather than interval arithmetic
  - Bytes assemble in `<source>/.bard-ingest`, which the walker already skips,
    so the final move is a rename rather than a cross-device copy of 700MB
  - Writing a probe file there is how a read-only source is caught at creation:
    a mode bit is not enough, since a read-only mount looks writable
  - Found by test: `Reap` compared SQLite's `datetime('now')` against an
    RFC3339 cutoff, and a space sorts before `T` — so every session looked
    expired and a fresh upload would have been deleted immediately. The cutoff
    is computed in SQL now, so there is one format in play
  - An uploaded title is untrusted input;
    `TestADestinationCannotEscapeTheSource` holds that
  - 430 tests pass, race-clean
- [2026-11-16] Phase 9: kids and age gating (committed, 3e157c9)
  - The gate lives on the query rather than being passed to twenty call sites,
    so forgetting it is not expressible
  - The age is computed once in Go, not twice: deriving it in SQL would be a
    second twin beside `EffectiveMinAge`, and one is enough
  - Decided while testing: an unrated book is **locked, not hidden**. "Ask a
    parent" is a message for a card you can see, and hiding it would make the
    reason code unreachable
  - `required_age` is surfaced only on a locked book, so no reader learns
    another book's `min_age`
  - The leak test was verified by neutering the scope's age condition and
    watching list, search and detail all fail
  - 409 tests pass, race-clean
- [2026-11-16] Phase 8.4: the taxonomy (committed, 3597550)
  - 44 canonical genres, flat, with the row inserted on demand so the table
    cannot drift from the list and the facet lists only what exists
  - One BISAC parser serving both the genre facet and the age suggestion
  - Found and fixed: Google Books was splitting its own headings in 8.2, so
    Open Library's `FICTION / Fantasy / Epic` matched nothing. Splitting moved
    into the taxonomy, which fixes every provider at once
  - Found and fixed: `sidecar.LockedMetadata` never returned `Subjects`, so a
    hand-locked genre had no effect whatsoever
  - A suggested age is written to its own column and never to `min_age`
  - Verified against live Open Library subject data rather than only fixtures
- [2026-11-16] Phase 8.3: matching and correction (committed, 5ce7242)
  - Fix Match resolves to one edition, since a provider record describes one
    manifestation; a book with several needs `edition_id` and a 400 says so
  - Applying pins, locks the fields the record filled, and writes the sidecar —
    three layers because each survives a different loss
  - The chosen record is re-found by re-running the cached search rather than
    held between requests; the query is stored on the pin so a retitled file
    cannot make the pinned id unfindable
  - `applyMetadata` now takes the locks to honour, so a scan can respect a
    correction where Fix Match overwrites one
  - An automatic match is not pinned, leaving a better score free to win
  - Comment pass over everything written this phase, against the convention in
    `~/.claude/CLAUDE.md`: war stories and "what" comments removed, multi-line
    comments collapsed, roughly 40% fewer comment lines
  - 377 tests pass, race-clean
- [2026-11-16] Phase 8.2: Google Books and Audnexus (committed, 81dddfd)
  - Audnexus leads the registry: the only narrator source, and it declines
    every ebook, so Open Library still leads for those
  - Verified live: narrators, four chapters, and "english" folded to "en"
  - Audnexus has no title search, so it is keyed on the ASIN alone — which
    meant teaching `ParseM4B` to read the iTunes free-form atom, since no
    parser produced one
  - Google Books scopes its query with `intitle:`/`inauthor:` and splits BISAC
    headings into parts, so the taxonomy gets three chances to map
    "Fiction / Fantasy / Epic" rather than one string to miss on
  - A shared quota is obeyed rather than counted: a 429 naming a *daily* quota
    stops asking that provider until UTC midnight, while a per-minute rate
    limit is still retried. Found live, where the keyless quota was already
    exhausted by other callers
  - Found and fixed, from Phase 4: the `©cmt` comment outranked the `ldes` long
    description, so Red Rising's blurb was "Purchased from Libro.fm" — and the
    atom read was dead code anyway, because go-mp4 cannot descend into an atom
    it does not model
  - 364 tests pass, race-clean
- [2026-11-16] Phase 8.1: the provider contract, Open Library, caching and
  rate limiting (committed, 2e252df)
  - Every provider off until the config names it; an unknown name is a startup
    error rather than a provider that silently never runs
  - One `client` every provider goes out through: paced at two requests a
    second, retried on 429 and 5xx honouring `Retry-After`, size-bounded, and
    cached on `(provider, url)` with no expiry — a 404 included
  - A provider fills only what the file cannot measure; `Query` carries
    `WantCover`, `WantPageCount` and `WantChapters`
  - Scoring is identifier-first, title-carried, and refuses a contradicted
    author outright; a surname agrees with a full name, which the recommended
    layout needs
  - Found and fixed: the lookup keyed on embedded tags never looked up an
    untagged file, which is the case the path convention exists for
  - Found against the live API: `language` is pooled across every edition (so a
    single entry is the only usable one), and a merged work answers under its
    old key with a redirect record
  - `BARD_LIVE_PROVIDERS=1 go test ./internal/metadata/provider/ -run Live`
    queries the real service; skipped by default so the suite stays offline
  - 337 tests pass, race-clean

Week 46 is captured in
[weekly-2026-W46.md](../docs/reports/weekly-2026-W46.md) and cleared from here.
Week 47 is still in progress, so the entries below stay until it closes and
`/weekly-report` captures them.

- [2026-11-16] Phase 6: read API and media delivery (committed, f10535c)
  - One `policy.BookScope` every multi-record path and every by-id read
    composes through, verified by neutering it and watching the tests fail
  - Nine facets over one registry, filtering with `EXISTS` so two matching
    editions cannot duplicate a book or inflate a count
  - FTS composed onto the same query as a listing, so search cannot bypass the
    scope or the facets
  - Range serving through `http.ServeContent`, cover variants picked from what
    is cached, width in the ETag
  - Found and fixed: `books_fts` had never been populated, because nothing
    called `ReindexBook` outside tests
  - Found and fixed: the file ETag lost its mtime to a date-format mismatch, so
    a file edited in place at the same size kept its tag
  - Sync manifest and two facets deferred to Phase 7, where their inputs exist
- [2026-11-16] Phase 7: user spaces, all four sub-phases (committed, e6e0518)
  - Progress keyed `(user, edition, device)`, because one row cannot diverge
    from itself. Every position returned, furthest first, with a `diverged`
    flag; a revoked device keeps its reading rather than rolling it back
  - `client_seq` compared only within one device, so a report that crossed
    another on the wire is ignored rather than winding a reader back
  - Reading state derived in SQL with a Go twin, pinned by
    `TestReadingStateMatchesSQL` the way the age gate is
  - Shelf, rating and reading state are facets, so `?saved=true&format=m4b`
    composes with the scope, sorts and pagination for free
  - Annotations keyed on a client-supplied uuid with a per-user sequence; a soft
    delete carries a tombstone to a device that synced before it
  - Sync manifest names candidates with a reason each, listing only the edition
    actually in progress but every edition of a shelved book
  - Handoff aligns chapters, interpolates inside the match, falls back to
    percentage — verified on the real library, where Red Rising's two editions
    agree within a second
  - Found and fixed: `normaliseTitle("1984")` erased a numeric title as
    decoration, the same number-vs-title ambiguity the Phase 4 path parser hit
  - 301 tests pass, race-clean

## Next Session
Phase 12: packaging and release. The last phase, and the one that makes this
installable by someone other than its author.

What is ready: the binary is already static and cgo-free, so the cross-compile
matrix is a goreleaser config rather than a porting job. Every deployment
decision the recipes have to document is already settled and written down —
Cloudflare Tunnel as the recommended default, `trusted_proxies` as the setting
that fails silently both ways, TLS reported rather than written by the wizard.
`docs/providers.md` and `docs/upload.md` are the shape the recipes should take.

What Phase 12 has to decide: what `bard export` covers. The rule that fell out
of 11.4a is the one to follow — everything *authored* rather than derived.
That is `genre_aliases`, `book_overrides`, `provider_matches`, the PINs, and
every account's progress, ratings, shelves, reading state and annotations. The
index itself is rebuildable from disk and sidecars, so it does not need
backing up; the user state does, and nothing currently says so to the user.

Worth considering alongside it: the author-initials scoring gap above, which is
a small change to `namesAgree` with real consequences for the match rate. It is
independent of Phase 12 and could go either before or after.

## Carried over from Phase 10
- An EPUB without the spec-required `mimetype` entry is refused on upload,
  although a scan of the same file on disk indexes it fine. Deliberate — the
  alternative is accepting any zip as a book — but it is an inconsistency worth
  remembering if a real file is ever rejected.
- `upload_sessions` rows are kept after a successful ingest, and only
  abandoned *open* ones are reaped. They are a small audit trail; Phase 12
  maintenance could prune them.
- A single uploaded MP3 becomes a one-part audiobook. Uploading a folder of
  MP3s as one edition is not expressible: each file would be its own session,
  and the scanner groups them by directory afterwards.

## Carried over from Phase 9
Nothing blocking. Worth knowing:

- `BookPolicy.View` and `Save` are the same check today, since the only thing
  that forbids viewing is a deny override. They are separate methods because
  Decision 7 splits them, and Phase 11's rating queue may give `View` more to
  do.
- A deny override removes a book from the scope, so `ReasonExplicitlyDenied`
  is never reached through a read path — the policy returns it, and the policy
  test covers it, as defence in depth.
- Ratings and reading state can still be set on a locked book. Only the paths
  that hand over contents — file, progress, annotations, handoff — check
  `Read`. A rating on a book you cannot open is harmless and arguably useful.

