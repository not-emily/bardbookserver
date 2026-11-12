# Phase 4: Scanner & Embedded Metadata

> **Depends on:** Phase 3
> **Enables:** Phase 5 (grouping), Phase 8 (providers enrich what this produces), Phase 10 (upload triggers a scan)
>
> See: [Full Plan](../plan.md)

## Goal

Walk configured sources, identify editions and files, extract embedded metadata, covers and chapters, and reconcile all of it into the index — restartably and without starving a co-resident Plex.

## Key Deliverables

- Durable job queue: `jobs` table plus a bounded worker pool sized by config
- Directory walker with format detection and audiobook-folder grouping
- EPUB parser: OPF metadata, cover, nav/TOC as chapters
- PDF parser: title, author, page count, first-page cover
- M4B parser: MP4 tags, `chpl` chapters, embedded cover
- MP3 folder parser: ID3 tags, per-file durations, files as chapter ordinals
- Path-convention fallback parser for files with no usable embedded metadata
- Sidecar read/write (`bard.json`), gated per source
- Incremental rescan: skip unchanged files by size and mtime
- Cover extraction into the on-disk cache
- `bard scan` CLI

## Files to Create

- `internal/jobs/queue.go`, `internal/jobs/worker.go`
- `internal/db/migrations/0005_jobs.sql`
- `internal/scan/scanner.go` — walk and reconcile
- `internal/scan/detect.go` — format detection, audiobook folder grouping
- `internal/scan/pathparse.go` — convention fallback
- `internal/metadata/embedded/epub.go`, `pdf.go`, `mp4.go`, `id3.go`
- `internal/metadata/sidecar/sidecar.go`
- `internal/media/cover.go` — extract and cache
- `cmd/bard/scan.go`

## Dependencies

**Internal:** Phase 3 schema and models.

**External:**
- `archive/zip` + `encoding/xml` (stdlib) — EPUB OPF
- `pdfcpu` — PDF metadata
- `abema/go-mp4` — MP4 atoms, chapters
- `dhowden/tag` — ID3
- `golang.org/x/image/draw` — cover variants
- `ffprobe` — optional external fallback only; never required

## Implementation Notes

**Metadata precedence, highest first:** sidecar → embedded → path convention. Phase 8 inserts providers between sidecar and embedded. Write this precedence once, in one function; every parser feeds it rather than deciding for itself.

Worker count is the compute throttle that protects the Plex co-tenancy requirement. Default it low (2) and make scans fully resumable, so a conservative default costs only wall-clock time. Jobs are rows, so a restart mid-scan resumes rather than restarting.

An audiobook is a *folder* of MP3s, which means the walker cannot treat files independently — it has to recognize a directory as one edition with ordered parts. Detect on directory contents (several audio files with a common prefix or track numbers), not on filename patterns alone. This is the single fiddliest piece of the phase.

EPUB nav documents come in two flavors: EPUB 3 `nav.xhtml` and EPUB 2 `toc.ncx`. Handle both — Project Gutenberg files skew old.

Chapter extraction is load-bearing beyond navigation: Phase 7's read-to-listen handoff reads exactly this data, which is why that feature adds no speculative storage.

Sidecars are written only when the source permits it. A read-only source must degrade cleanly, keeping everything in SQLite, with no scan failure and no repeated write attempts.

Never rename or move a file in this phase or any other. Filing uploads into the convention happens in Phase 10, on ingest only.

## Sub-phases

### 4.1: Job queue
**Goal:** durable, bounded, restart-safe background work.
**Deliverables:** `jobs` table, queue, worker pool, config-driven concurrency.

### 4.2: Walker and format detection
**Goal:** turn a directory tree into candidate editions and files.
**Deliverables:** `scanner.go`, `detect.go`, audiobook folder grouping, incremental skip.

### 4.3: Embedded parsers
**Goal:** pull metadata, covers, and chapters out of real files.
**Deliverables:** EPUB, PDF, M4B, MP3 parsers behind one precedence function.

### 4.4: Sidecars and path fallback
**Goal:** persist metadata beside the media; cope with Gutenberg-style naming.
**Deliverables:** `sidecar.go`, `pathparse.go`, per-source gating.

## Validation

- [ ] Scanning a fixture tree produces the expected sources, editions, and files
- [ ] A folder of MP3s becomes one edition with correctly ordered parts, not N editions
- [ ] M4B chapters are extracted with correct titles and offsets
- [ ] EPUB 2 (`toc.ncx`) and EPUB 3 (`nav.xhtml`) both yield chapters
- [ ] Covers are extracted for all four formats and cached
- [ ] A second scan with no filesystem changes performs no writes
- [ ] Touching one file's mtime rescans that file alone
- [ ] Sidecars are written when the source allows and absent when it does not
- [ ] A read-only source scans without error
- [ ] Killing the process mid-scan and restarting resumes without duplicate rows
- [ ] A file with no embedded metadata gets title and author from its path
- [ ] No file is renamed or moved during any scan
