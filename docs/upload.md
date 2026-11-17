# Uploading a book

Adding a book from a phone over cellular has to survive a dropped connection,
so an upload is a session the client resumes rather than a request it repeats.

## Configuration

```yaml
upload:
  max_size_mb: 4096
  chunk_size_mb: 8
  session_ttl_hours: 48
```

`max_size_mb` is refused at session creation, before any bytes are sent.
`chunk_size_mb` is what the server tells the client to send and is the unit of
resume. Abandoned sessions are reaped after `session_ttl_hours` by a background
job, which is what keeps a half-sent 700MB audiobook from holding the disk.

Uploading requires `can_upload` on the account, which only admins have by
default.

## The flow

```
POST /api/v1/uploads
     {source_id, filename, format, total_bytes, sha256}
  -> 201 {id, chunk_bytes, missing_chunks, ...}

PUT  /api/v1/uploads/{id}/chunks/{offset}      the bytes, as the body
  -> 200 {missing_chunks, ...}

GET  /api/v1/uploads/{id}                      what is still missing
POST /api/v1/uploads/{id}/finalize             hash check, then ingest
```

The offset must be a multiple of `chunk_bytes`. Every chunk is exactly
`chunk_bytes` except the last, and sending the wrong length is an error rather
than a silent truncation.

**Resuming** means asking `GET /api/v1/uploads/{id}` for `missing_chunks` and
sending only those. **Resending** a chunk that already arrived is harmless: the
same offset rewrites the same bytes, so a client that lost the response rather
than the bytes can simply retry.

## What is checked, and when

| Check | When |
|-------|------|
| Size against `max_size_mb` | At session creation, before any bytes |
| The source is writable | At session creation, by writing a probe file |
| Contents match the declared format | On the first chunk |
| `sha256` of the whole file | On finalize |

The format is decided by **reading the bytes**, not the filename or the
client's `Content-Type`, either of which a client can get wrong. A declared
EPUB whose contents are a PDF is refused on the first chunk and the partial
file is deleted.

One consequence worth knowing: Bard requires the `mimetype` entry that the
EPUB specification says must come first and uncompressed. A tool that produced
a zip without it will be refused on upload, even though a *scan* of the same
file on disk would index it.

A hash mismatch on finalize deletes the partial file. Nothing that failed a
check is ever left where it could be mistaken for a book.

## Where the file lands

Bytes assemble in a hidden `.bard-ingest` directory inside the target source,
which the scanner skips, so a transfer in progress never looks like a book. On
finalize the file is read for metadata and moved into the documented layout:

```
<source>/Pierce Brown/Red Rising Saga/02 - Golden Son.epub
<source>/Frank Herbert/Dune.epub
<source>/uploaded-filename.epub          when nothing could be read
```

A name is stripped of path separators and control characters, so a title read
out of an uploaded file cannot write outside the source. A collision never
overwrites: the second book gets ` (2)` instead.

**This is the only place Bard writes new media.** Decision 3's promise holds
everywhere else — existing files are never renamed or moved — and a file being
ingested is not part of the library until it lands.

A scan of the source is queued afterwards, which is what indexes the book,
applies metadata providers and groups it with any editions it belongs to.
