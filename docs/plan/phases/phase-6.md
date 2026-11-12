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
- `GET /api/v1/sync` — per-user manifest of what a device should pre-cache
- `BookScope` applied to every multi-record path

## Files to Create

- `internal/api/book_handlers.go`, `search_handlers.go`, `collection_handlers.go`
- `internal/api/media_handlers.go` — range serving
- `internal/api/sync_handlers.go`
- `internal/api/dto.go` — response shapes
- `internal/policy/book.go` — `BookPolicy` and `BookScope`
- `internal/media/serve.go` — range, ETag, conditional requests
- `internal/media/covervariants.go`
- `internal/api/book_handlers_test.go`

## Dependencies

**Internal:** Phase 2 auth middleware and policy package; Phase 5 grouped books; Phase 3 query builder.

**External:** none new. `http.ServeContent` handles ranges and conditional requests.

## Implementation Notes

**Every path returning more than one record goes through `BookScope`.** Browse, search, series, collections, facet counts, and the sync manifest included. Phase 9 adds age gating inside that one scope; a handler that builds its own query bypasses the restriction silently. This is the structural reason the scope exists rather than a per-handler check.

Use `http.ServeContent` for media rather than hand-rolling range parsing — it handles multi-range, `If-Range`, and 206 correctly. Set a strong ETag from the file's content hash so clients can revalidate cached downloads cheaply.

Facet filters compose onto the Phase 3 query builder alongside the scope, per Decision 8. Facet *counts* must respect the scope too, or a count leaks the existence of hidden books.

The sync manifest is the whole server side of offline support, per Decision 15: active reads, next unread in any series the user is reading, and the saved shelf. Flat list of editions with file ids, sizes, and hashes. No server-side eviction, expiry, or caps.

Response DTOs are explicit structs, not models serialized directly. Models carry fields clients must never see — token hashes nearby in the user case, `min_age` internals, source paths. Sending a filesystem path to a client leaks the server's layout.

Cover variants are generated on first request and cached on disk, not pre-generated for 5000 books during a scan.

## Validation

- [ ] Listing, search, series, and collection endpoints all route through `BookScope`
- [ ] A range request returns 206 with correct `Content-Range` and byte count
- [ ] A full request returns 200 with the whole file
- [ ] `If-None-Match` on a cover returns 304
- [ ] Facet filters compose: format plus genre plus reading state in one query
- [ ] Facet counts match the number of records the same filters return
- [ ] Pagination is stable across pages with a filter applied
- [ ] The sync manifest lists active reads and saved books with file sizes and hashes
- [ ] No response contains a filesystem path
- [ ] An unauthenticated request to any `/api/v1` path returns 401
