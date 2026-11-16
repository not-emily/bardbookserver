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
  google_books_key: ""
  timeout_seconds: 10
```

An unrecognised provider name is a startup error, because a provider that
silently never runs is worse than one that refuses to start.

| Provider | Covers | Needs a key |
|----------|--------|-------------|
| `audnexus` | **Narrators**, chapter titles and timings, series, abridgement, audiobook descriptions and covers | No |
| `openlibrary` | Titles, authors, descriptions, subjects, covers, page counts | No |
| `googlebooks` | Descriptions, BISAC categories, page counts, covers | Recommended |

`timeout_seconds` bounds a single request. A provider that times out costs that
book its enrichment and nothing else: the scan still succeeds on what the files
said.

The order in that table is the order they are consulted, and the first provider
to know a field owns it. Audnexus leads because it is the only source of
narrators, and it answers nothing but an audiobook — so for ebooks Open Library
leads anyway, with Google Books filling what the others left empty.

### Audnexus needs an ASIN

Audnexus has no title search. It answers `/books/{asin}` and nothing else, so
it can only enrich an audiobook whose file carries an Audible ASIN — which Bard
reads from the iTunes free-form atom that Audible and AAX conversions write.

**An audiobook from a DRM-free store carries no ASIN, so Audnexus cannot help
it.** Libro.fm and Downpour files, for instance, have none. For those the
narrator has to come from the file's composer tag or from a hand edit. Audnexus
also serves one region at a time and answers `REGION_UNAVAILABLE` for titles
outside it, which Bard treats as "no match" rather than as an error.

### Google Books and its daily quota

Google Books allows roughly 1000 queries a day. Without an API key that quota
is **shared with every other anonymous caller**, so in practice it is often
already exhausted — a lookup then comes back `429 Too Many Requests` saying
"Queries per day".

Bard cannot count a quota it shares, so it does not try. When a provider
refuses for the day, Bard stops asking it until the next UTC midnight rather
than retrying every remaining book. Set `google_books_key` to a key of your own
if you want the provider to be dependable.

## What a provider is allowed to change

The file wins on anything the file can measure — its own duration, page count,
chapter marks and embedded cover art. A provider is asked to fill only what is
genuinely missing, so enabling one cannot replace a PDF's real page count with
an estimate, an EPUB's own artwork with a scan of a different edition, or an
M4B's chapter marks with Audible's. Duration is never taken from a provider at
all: the file that actually plays is the authority on how long it is.

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

Set `BARD_GOOGLE_BOOKS_KEY` as well to exercise Google Books with a key; the
live Google Books test skips rather than fails when the shared quota is gone,
since that is not a fault in Bard.

These tests are skipped by default so the suite stays offline and a provider
having a bad day is not a build failure.
