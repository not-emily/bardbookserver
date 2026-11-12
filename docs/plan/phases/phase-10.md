# Phase 10: Upload

> **Depends on:** Phase 4 (ingest triggers a scan), Phase 6 (API scaffolding)
> **Enables:** Phase 11 (upload UI)
>
> See: [Full Plan](../plan.md)

## Goal

Add a book from a phone or tablet over cellular without SSH, resumably.

## Key Deliverables

- Resumable chunked upload: create session, `PUT` chunks at offsets, finalize
- Server-side reassembly with a hash check
- `can_upload` permission, defaulting to admin-only
- Ingest: land in the source's ingest directory, scan, match, file into the naming convention
- Upload session cleanup for abandoned transfers
- Format and size validation before accepting bytes

## Files to Create

- `internal/db/migrations/0010_uploads.sql` — sessions, chunk state
- `internal/api/upload_handlers.go`
- `internal/upload/session.go`, `internal/upload/ingest.go`
- `internal/policy/upload.go`
- `internal/upload/session_test.go`

## Dependencies

**Internal:** Phase 4 scanner and job queue; Phase 2 policy and auth.

**External:** none new.

## Implementation Notes

**Resumability is the requirement, not a refinement.** A 700MB M4B over cellular will fail partway through, and a non-resumable upload means starting over — which means the feature goes unused. Session records a declared total size and a content hash; chunks arrive at explicit offsets and are idempotent, so a client that loses its response can safely resend.

This is the only place Bard writes new media files. Decision 3's promise holds everywhere else: existing files are never renamed or moved. Filing an upload into the convention happens on ingest, before the file is part of the library.

Validate the declared format against actual content, not the filename or the client's `Content-Type`. Enforce a configurable max size and reject at session creation rather than after accepting a gigabyte.

Ingest writes into the target source's directory, so the source must be writable — surface a clear error for a read-only source rather than failing mid-transfer.

Abandoned sessions leak disk. Reap them on a schedule through the Phase 4 job queue, with the TTL configurable.

The `can_upload` permission exists from the start and defaults to admin-only; opening it per-user later is a default change, not new plumbing.

## Validation

- [ ] A large file uploads successfully in chunks
- [ ] Interrupting mid-upload and resuming completes without re-sending delivered chunks
- [ ] Resending an already-delivered chunk is idempotent
- [ ] A hash mismatch on finalize rejects the upload and leaves no partial file in the library
- [ ] A non-admin upload attempt is denied with `upload_not_allowed`
- [ ] An uploaded EPUB appears in the library with metadata after ingest
- [ ] A file whose contents do not match its declared format is rejected
- [ ] Exceeding the configured max size is rejected at session creation
- [ ] Targeting a read-only source fails with a clear error before bytes are sent
- [ ] Abandoned sessions are reaped and their partial data removed
