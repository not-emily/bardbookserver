# Metadata providers

Bard can enrich what it reads from your files with metadata from online
services. **Every provider is off until you turn it on**, and that is a
deliberate default rather than an oversight: a lookup sends the title and
author of a book you own to a third party, who may log it.

With no provider enabled, Bard makes no outbound request during a scan. It
indexes your library from the files, their sidecars and the directory layout
alone.

## Configuration

```yaml
metadata:
  providers:
    openlibrary: true
    googlebooks: false
    audnexus: false
  timeout_seconds: 10
```

An unrecognised provider name is a startup error, because a provider that
silently never runs is worse than one that refuses to start.

| Provider | Covers | Needs a key |
|----------|--------|-------------|
| `openlibrary` | Titles, authors, descriptions, subjects, covers, page counts | No |
| `googlebooks` | *(v1 phase 8.2)* | Optional |
| `audnexus` | *(v1 phase 8.2)* | No |

`timeout_seconds` bounds a single request. A provider that times out costs that
book its enrichment and nothing else: the scan still succeeds on what the files
said.

## What a provider is allowed to change

The file wins on anything the file can measure — its own page count, its
chapter marks and its embedded cover art. A provider is asked to fill only what
is genuinely missing, so enabling one cannot replace a PDF's real page count
with an estimate or an EPUB's own artwork with a scan of a different edition.

For bibliographic fields — title, author, description, publisher, subjects —
the provider is preferred over the file, because file tags are frequently
sloppy or absent. The order is:

1. Fields you locked by hand
2. The provider
3. The file's own metadata
4. The directory layout (`Author/Series/## - Title/`)
5. The rest of the sidecar

A field you have corrected and locked is never overwritten by a refresh.

## Rate limiting and caching

Each provider is asked at most twice a second, and every response is cached in
the database forever, keyed on the request that produced it. A 5000-book
backfill therefore happens once, in under an hour, and a rescan asks nothing at
all. A "not found" is cached like any other answer, so the books no provider
has are not re-requested on every scan.

Nothing expires. If you need Bard to ask again — a provider has fixed a record,
or you want a different match — that is what Fix Match is for.

## Matching

A provider's answer is accepted only when it is clearly the same book. An ISBN
or ASIN match is conclusive. Otherwise the title carries the match and the
author confirms it: an exact title with no author on either side is accepted,
and an exact title with a *contradicting* author is refused, because a wrong
match takes a person to notice and undo.

An author folder named with a surname alone agrees with a provider's full name,
since that is how the recommended layout is usually written.

## Checking against the live services

The recorded responses in the test suite are only as good as the day they were
recorded. To query the real services:

```sh
BARD_LIVE_PROVIDERS=1 go test ./internal/metadata/provider/ -run Live -v
```

These tests are skipped by default so the suite stays offline and a provider
having a bad day is not a build failure.
