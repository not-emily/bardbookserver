# Phase 6: Read API & Media Delivery

> **Depends on:** Phase 2, Phase 5
> **Enables:** Phase 7 (spaces), Phase 9 (age gating slots into the scope), Phase 10, Phase 11
>
> See: [Full Plan](../plan.md)

## Goal

Give clients an authenticated API to browse, filter, search, and stream the library, with every listing path running through one policy scope.

## Key Deliverables

- `GET /api/v1/books` — paginated listing, facet filters, sorts
- `GET /api/v1/books/{id}` — detail with editions, files, chapters, contributors
- `GET /api/v1/search` — FTS5 over title, author, narrator, series
- `GET /api/v1/series/{id}`, `/collections`, `/collections/{id}`
- Facet value endpoints for populating client filter UIs
- `GET /api/v1/files/{id}/content` — range-request media delivery
- `GET /api/v1/books/{id}/cover` — cached resized variants, ETag
- `BookScope` applied to every multi-record path

**Moved to Phase 7.2:** `GET /api/v1/sync`, and the reading-state and
personal-rating facets. All three read user state Phase 7 creates; see the
decision log.

## Files to Create

- `internal/api/book_handlers.go`, `search_handlers.go`, `collection_handlers.go`,
  `facet_handlers.go`
- `internal/api/media_handlers.go` — range serving
- `internal/api/dto.go` — response shapes
- `internal/policy/book.go` — `BookScope` (`BookPolicy` lands with Phase 9's
  age ladder)
- `internal/policy/collection.go` — `CollectionScope`
- `internal/db/browse.go`, `detail.go`, `facets.go`, `series.go`,
  `collections.go`, `media.go` — the composed read queries
- `internal/media/serve.go` — range, ETag, conditional requests
- `internal/media/covervariants.go`
- `internal/api/book_handlers_test.go`, `search_handlers_test.go`,
  `facet_handlers_test.go`, `media_handlers_test.go`

## Dependencies

**Internal:** Phase 2 auth middleware and policy package; Phase 5 grouped books; Phase 3 query builder.

**External:** none new. `http.ServeContent` handles ranges and conditional requests.

## Implementation Notes

**Every path returning more than one record goes through `BookScope`.** Browse, search, series, collections, facet counts, and the sync manifest included. Phase 9 adds age gating inside that one scope; a handler that builds its own query bypasses the restriction silently. This is the structural reason the scope exists rather than a per-handler check.

Use `http.ServeContent` for media rather than hand-rolling range parsing — it handles multi-range, `If-Range`, and 206 correctly. Set a strong ETag from the file's content hash so clients can revalidate cached downloads cheaply.

Facet filters compose onto the Phase 3 query builder alongside the scope, per Decision 8. Facet *counts* must respect the scope too, or a count leaks the existence of hidden books.

Response DTOs are explicit structs, not models serialized directly. Models carry fields clients must never see — token hashes nearby in the user case, `min_age` internals, source paths. Sending a filesystem path to a client leaks the server's layout.

Cover variants stay pre-generated at scan time, which is what Phase 4.3 already does; the read path picks the smallest cached width that covers the request. See the decision log.

## Validation

- [x] Listing, search, series, and collection endpoints all route through `BookScope`
- [x] A range request returns 206 with correct `Content-Range` and byte count
- [x] A full request returns 200 with the whole file
- [x] `If-None-Match` on a cover returns 304
- [x] Facet filters compose: format plus genre plus length in one query
      (reading state arrives with Phase 7)
- [x] Facet counts match the number of records the same filters return
- [x] Pagination is stable across pages with a filter applied
- [x] No response contains a filesystem path
- [x] An unauthenticated request to any `/api/v1` path returns 401

Moved to Phase 7.2 with the sync manifest: the manifest's own validation line,
and reading state as a facet.
