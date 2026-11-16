# Project Progress - bardbookserver

## Plan Files
Roadmap: [plan.md](../docs/plan/plan.md)
Current Phase: [phase-8.md](../docs/plan/phases/phase-8.md)
Latest Weekly Report: [weekly-2026-W46.md](../docs/reports/weekly-2026-W46.md)

Last Updated: 2026-11-16

## Current Focus
Phase 8 is nearly done. 8.1 through 8.3 are complete: all three providers are
implemented, Open Library and Audnexus are verified live, the narrator facet
has a source for the first time, and a wrong match is now correctable and
sticky. Only the taxonomy (8.4) is left.

## Active Tasks
- [IN PROGRESS] Phase 8: Metadata providers & taxonomy
  - ✅ 8.1: `Provider` interface, explicit registry, config toggles off by
    default, Open Library, response caching, rate limiting, match scoring
  - ✅ 8.2: Google Books with a daily-quota breaker, Audnexus with narrators
    and chapters, plus the M4B ASIN extraction Audnexus cannot work without
  - ✅ 8.3: candidate ranking, Fix Match over `/api/v1/books/{id}/match`,
    pinned matches honoured by a rescan, locks and sidecar written on apply
  - ⏭ 8.4: canonical genre list, BISAC parser, subject mapping, `raw_subjects`
    retention, suggested `min_age`
  - ⏭ Genres are still empty; they need 8.4. Subjects now arrive from all
    three providers and are waiting on the mapping table
- [LATER] Phase 9 (age gating) — extends `BookScope.Apply`; wants Phase 8's
  suggested ages but can be built against hand-set ones
- [LATER] Phase 10 (upload) — independent of 8 and 9

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
- Genres are still empty; nothing maps a subject string onto `book_genres`
  until 8.4. Subjects now arrive from all three providers — Open Library capped
  at 40, Google Books' BISAC headings split into parts and capped at 20.
- `googleVolumeInfo.MaturityRating` and `audnexusBook.IsAdult` are parsed and
  unused; they are 8.4's age-suggestion inputs.
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
- Phase 9 inserts the age ladder into `BookScope.Apply` and adds `BookPolicy`
  beside it. Every read path — listings, search, facets, by-id detail, file
  content, covers, progress, annotations and the sync manifest — resolves
  through that scope, so the change is in one function.
- Annotation positions are stored as the client sends them (CFI, page,
  seconds). Nothing validates that a CFI belongs to the edition it is filed
  under, so a buggy client can store an unresolvable highlight.
- Unlocking a field in the Phase 11 admin UI must also drop it from `locked` in
  the sidecar, or the old value keeps outranking the file forever. Renaming an
  entry in `model.fieldNames` silently unlocks that field in every sidecar
  already on disk.
- MP3 durations run ~0.04s long per file where an encoder wrote no gapless
  info. 1.7s over 16 hours, inside the plan's handoff tolerance.
- QuickTime text chapter tracks in M4B are not read, only the Nero `chpl` atom.
  `ffprobe` is the documented fallback, and nothing calls it yet.

## Completed This Week
- [2026-11-16] Phase 8.3: matching and correction (uncommitted)
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
Phase 8.4: taxonomy — the canonical genre list, the BISAC parser, the subject
mapping table, `raw_subjects` retention, and suggested ages.

What is ready: all three providers already return subjects, normalised and
capped. Google Books' BISAC headings are split into parts, so
"Fiction / Fantasy / Epic" offers three strings to map rather than one.
`genres`, `book_genres` and `raw_subjects` exist from Phase 3 and are empty.
`googleVolumeInfo.MaturityRating` and `audnexusBook.IsAdult` are parsed and
waiting for the age-suggestion consumer.

What 8.4 has to add: `internal/taxonomy` does not exist yet. The BISAC parser
serves both the genre facet and Phase 9's suggested age (`JUV` and `YAF`
prefixes) — one parser, two readers. A suggested age goes to its own column,
never to `books.min_age`, because an auto-applied wrong rating fails open.

Also worth doing in or near 8.4: `metadata.NormalizeLanguage`'s three-letter
table lives in `internal/metadata` and arguably belongs with the taxonomy.
