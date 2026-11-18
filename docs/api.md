# Bard API

<!-- Generated from docs/api/openapi.yaml by scripts/docs. Do not edit. -->

A self-hosted server for ebooks and audiobooks.

Version `0.1.0`.

Bard serves its admin UI, this JSON API and the media itself from one
origin on one port. A client points at the host and nothing else.

**Authentication is a per-device bearer token.** `POST /auth/login`
exchanges a username and password for one, and that token is presented as
`Authorization: Bearer <token>` on every later request. Tokens are opaque
and revocable: there is nothing to verify offline, because revocation that
takes effect on the next request is worth more than statelessness here.

**CSRF does not apply to this API.** It guards the cookie-authenticated
browser session only, since a cross-site form cannot set an
`Authorization` header. A native client never sends a CSRF token.

**Content a reader may not see is absent rather than refused.** Where a
source hides age-locked books, requesting one gives `404`, not `403` with
a reason — a reason code would confirm the book exists.

## Servers

- `https://bard.example.com` — Your own server, behind whichever transport you chose.

## Endpoints

- [`GET /api/v1/annotations`](#get-apiv1annotations) — Annotations in one edition
- [`GET /api/v1/annotations/changes`](#get-apiv1annotationschanges) — What changed since a cursor
- [`DELETE /api/v1/annotations/{uuid}`](#delete-apiv1annotationsuuid) — Delete an annotation
- [`PUT /api/v1/annotations/{uuid}`](#put-apiv1annotationsuuid) — Create or update an annotation
- [`POST /api/v1/auth/login`](#post-apiv1authlogin) — Exchange credentials for a device token
- [`POST /api/v1/auth/logout`](#post-apiv1authlogout) — Revoke this device
- [`GET /api/v1/auth/me`](#get-apiv1authme) — The current profile, device, and who else this device may become
- [`POST /api/v1/auth/switch`](#post-apiv1authswitch) — Read as a different profile on this device
- [`GET /api/v1/books`](#get-apiv1books) — Browse the library
- [`GET /api/v1/books/{id}`](#get-apiv1booksid) — One book, with its editions and files
- [`GET /api/v1/books/{id}/cover`](#get-apiv1booksidcover) — Cover image
- [`GET /api/v1/books/{id}/handoff`](#get-apiv1booksidhandoff) — Where you would land in each other edition
- [`DELETE /api/v1/books/{id}/match`](#delete-apiv1booksidmatch) — Unpin a match
- [`GET /api/v1/books/{id}/match`](#get-apiv1booksidmatch) — Metadata candidates for a book
- [`POST /api/v1/books/{id}/match`](#post-apiv1booksidmatch) — Apply a candidate
- [`DELETE /api/v1/books/{id}/rating`](#delete-apiv1booksidrating) — Clear a rating
- [`PUT /api/v1/books/{id}/rating`](#put-apiv1booksidrating) — Rate a book
- [`DELETE /api/v1/books/{id}/saved`](#delete-apiv1booksidsaved) — Remove from the saved shelf
- [`PUT /api/v1/books/{id}/saved`](#put-apiv1booksidsaved) — Add to the saved shelf
- [`PUT /api/v1/books/{id}/state`](#put-apiv1booksidstate) — Mark finished or abandoned
- [`GET /api/v1/collections`](#get-apiv1collections) — List collections
- [`POST /api/v1/collections`](#post-apiv1collections) — Create a collection
- [`DELETE /api/v1/collections/{id}`](#delete-apiv1collectionsid) — Delete a collection
- [`GET /api/v1/collections/{id}`](#get-apiv1collectionsid) — One collection and its books
- [`PATCH /api/v1/collections/{id}`](#patch-apiv1collectionsid) — Rename or describe a collection
- [`DELETE /api/v1/collections/{id}/books/{bookID}`](#delete-apiv1collectionsidbooksbookid) — Remove a book from a collection
- [`PUT /api/v1/collections/{id}/books/{bookID}`](#put-apiv1collectionsidbooksbookid) — Add a book to a collection
- [`GET /api/v1/devices`](#get-apiv1devices) — List authorized devices
- [`DELETE /api/v1/devices/{id}`](#delete-apiv1devicesid) — Revoke a device
- [`DELETE /api/v1/editions/{id}/group`](#delete-apiv1editionsidgroup) — Split an edition onto a book of its own
- [`POST /api/v1/editions/{id}/group`](#post-apiv1editionsidgroup) — Move an edition onto another book
- [`POST /api/v1/editions/{id}/handoff`](#post-apiv1editionsidhandoff) — Carry a position into another edition
- [`GET /api/v1/editions/{id}/progress`](#get-apiv1editionsidprogress) — Where every device is in this edition
- [`PUT /api/v1/editions/{id}/progress`](#put-apiv1editionsidprogress) — Report this device's position
- [`GET /api/v1/facets`](#get-apiv1facets) — Every facet and its values
- [`GET /api/v1/facets/{name}`](#get-apiv1facetsname) — One facet's values
- [`GET /api/v1/files/{id}/content`](#get-apiv1filesidcontent) — Stream or download a book file
- [`GET /api/v1/groupings/uncertain`](#get-apiv1groupingsuncertain) — Groupings Bard is unsure about
- [`GET /api/v1/history`](#get-apiv1history) — Books this reader has finished
- [`GET /api/v1/search`](#get-apiv1search) — Search the library
- [`GET /api/v1/series`](#get-apiv1series) — List series
- [`GET /api/v1/series/{id}`](#get-apiv1seriesid) — One series and its books
- [`GET /api/v1/sync`](#get-apiv1sync) — What this reader would want held offline
- [`POST /api/v1/uploads`](#post-apiv1uploads) — Begin a resumable upload
- [`GET /api/v1/uploads/{id}`](#get-apiv1uploadsid) — Session state, and which chunks are still missing
- [`PUT /api/v1/uploads/{id}/chunks/{offset}`](#put-apiv1uploadsidchunksoffset) — Send one chunk
- [`POST /api/v1/uploads/{id}/finalize`](#post-apiv1uploadsidfinalize) — Assemble and ingest
- [`GET /api/v1/users/{id}/overrides`](#get-apiv1usersidoverrides) — A reader's per-book age exceptions
- [`DELETE /api/v1/users/{id}/overrides/{bookID}`](#delete-apiv1usersidoverridesbookid) — Clear an exception
- [`PUT /api/v1/users/{id}/overrides/{bookID}`](#put-apiv1usersidoverridesbookid) — Grant or refuse one book

## `GET /api/v1/annotations`

**Annotations in one edition**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `edition_id` | query | yes |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | This reader's annotations in that edition. |
| `400` | [`Error`](#error) | A missing or unparseable edition_id. |

## `GET /api/v1/annotations/changes`

**What changed since a cursor**

The offline sync feed. Pass the `cursor` from the previous reply;
`more` says whether to ask again straight away. Deletions come back
as tombstones with `deleted` set, so a device that was away learns
about them rather than resurrecting what it still holds.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `since` | query |  | The last cursor seen. Omit or send 0 for everything. |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Changes in sequence order. |

## `DELETE /api/v1/annotations/{uuid}`

**Delete an annotation**

Leaves a tombstone, so other devices learn of the deletion.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Deleted. |
| `404` | [`Error`](#error) | No such annotation of this reader's. |

## `PUT /api/v1/annotations/{uuid}`

**Create or update an annotation**

The UUID is chosen by the client, which is what lets an offline device
create an annotation and sync it later without negotiating an id.
Sending the same UUID again updates it.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

[`AnnotationRequest`](#annotationrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Annotation`](#annotation) | The stored annotation. |
| `400` | [`Error`](#error) | A bad UUID, an unknown kind, or a malformed body. |
| `404` | [`Error`](#error) | No such edition, or not visible to this reader. |

## `POST /api/v1/auth/login`

**Exchange credentials for a device token**

The only unauthenticated endpoint. Rate limited per resolved client
address, so a server whose `trusted_proxies` is wrong throttles every
client as one — see `/diagnostics` in the admin UI.

`device_name` is what the account holder sees in their authorized
devices list, so it should name the device rather than the app.

No authentication required.

### Request body

[`LoginRequest`](#loginrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`LoginResponse`](#loginresponse) | A new device token. Store it; it is not retrievable again. |
| `400` | [`Error`](#error) | Username or password missing, or a malformed body. |
| `401` | [`Error`](#error) | Invalid credentials. Deliberately not distinguished from an unknown username. |
| `429` | [`Error`](#error) | Too many attempts. `Retry-After` carries the wait in seconds. |

## `POST /api/v1/auth/logout`

**Revoke this device**

Revokes the token that made the request, so logging out is permanent
rather than a client-side forget. Discard the token afterwards.

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `204` | — | Revoked. The token is dead from the next request. |

## `GET /api/v1/auth/me`

**The current profile, device, and who else this device may become**

`user` is who the device is reading as now, which may be a managed
profile rather than the account that signed in. `profiles` is what
`POST /auth/switch` will accept, and is how a client draws a profile
picker without a second request.

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Session`](#session) | The session as the server sees it. |

## `POST /api/v1/auth/switch`

**Read as a different profile on this device**

Switches which profile the device is reading as. No new token is
issued — the device is the identity, and the active profile is a
property of it.

A profile with `has_pin` needs `pin`. PIN attempts are rate limited
per device and profile.

A profile that does not exist is refused exactly as one belonging to
another household would be, so a probe cannot tell the two apart.

### Request body

[`SwitchRequest`](#switchrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The profile now active on this device. |
| `400` | [`Error`](#error) | A malformed body. |
| `403` | [`Denied`](#denied) | Not switchable from this device, or a missing or wrong PIN. |
| `429` | [`Error`](#error) | Too many PIN attempts. |

## `GET /api/v1/books`

**Browse the library**

The main listing. Filters are facet names repeated as query
parameters: values of one facet are OR'd, different facets are AND'd,
so `?genre=fantasy&genre=sci-fi&format=epub` means *(fantasy or
sci-fi) and epub*.

An unknown parameter is a `400` rather than a filter that silently
matches everything.

What a restricted reader may not see never appears here, because
visibility is part of the query rather than a filter applied after
paging. A book that is locked but visible comes back with `locked`
and `required_age` set.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`BookPage`](#bookpage) | One page of books. |
| `400` | [`Error`](#error) | An unknown filter, an out-of-range limit, or an unknown sort. |

## `GET /api/v1/books/{id}`

**One book, with its editions and files**

The detail a reader opens: description, genres, tags, and every
edition with its files and chapters. `editions[].files[].content_url`
is what to stream.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`BookDetail`](#bookdetail) | The book. |
| `404` | [`Error`](#error) | No such book — or one hidden from this reader. The two are deliberately indistinguishable. |

## `GET /api/v1/books/{id}/cover`

**Cover image**

Served from a cache of pre-scaled variants. `width` picks the smallest
variant at least that wide; omitting it gives the full-size image.
Carries an `ETag`, so a conditional request is cheap.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `width` | query |  | Desired width in pixels, 1 to 4000. Omit for full size. |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The image. |
| `304` | — | Unchanged since the `ETag` you presented. |
| `400` | [`Error`](#error) | Width out of range. |
| `404` | [`Error`](#error) | That book has no cover, or is not visible to this reader. |

## `GET /api/v1/books/{id}/handoff`

**Where you would land in each other edition**

For drawing "continue in…": every edition of the book with the
position this reader would arrive at, so a client can offer the switch
before committing to it.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Each edition and the position it would open at. |
| `404` | [`Error`](#error) | No such book, or not visible to this reader. |

## `DELETE /api/v1/books/{id}/match`

**Unpin a match**

Unpins and unlocks, **including in the sidecar** — a lock left there
would keep outranking the file forever.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `edition_id` | query |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Unpinned. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |
| `404` | [`Error`](#error) | No such book or edition. |

## `GET /api/v1/books/{id}/match`

**Metadata candidates for a book**

Ranked candidates from the enabled providers, **including the ones the
automatic matcher refused** — `would_match_automatically` says which
those are, because a human can recognise a match a score cannot.

`q` and `author` search for something else entirely, for when the
indexed metadata is too wrong to find the right book.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `q` | query |  | Search for a different title instead of the indexed one. |
| `author` | query |  |  |
| `edition_id` | query |  | Which edition to match; defaults to the book's primary. |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Ranked candidates. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |
| `404` | [`Error`](#error) | No such book. |
| `409` | [`Error`](#error) | No metadata provider is enabled. |

## `POST /api/v1/books/{id}/match`

**Apply a candidate**

Pins the chosen candidate and **locks the fields it set**, so the next
scan cannot overwrite the correction. The lock is written to the
sidecar as well, which is what makes it survive rebuilding the index.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

[`ApplyMatchRequest`](#applymatchrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Applied and locked. |
| `400` | [`Error`](#error) | A malformed body, or a candidate that no longer resolves. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |
| `404` | [`Error`](#error) | No such book or edition. |
| `409` | [`Error`](#error) | No metadata provider is enabled. |

## `DELETE /api/v1/books/{id}/rating`

**Clear a rating**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`ReaderState`](#readerstate) | The book's state for this reader. |
| `404` | [`Error`](#error) | No such book, or not visible to this reader. |

## `PUT /api/v1/books/{id}/rating`

**Rate a book**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`ReaderState`](#readerstate) | The book's state for this reader. |
| `400` | [`Error`](#error) | A rating outside 1 to 5. |
| `404` | [`Error`](#error) | No such book, or not visible to this reader. |

## `DELETE /api/v1/books/{id}/saved`

**Remove from the saved shelf**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`ReaderState`](#readerstate) | The book's state for this reader. |
| `404` | [`Error`](#error) | No such book, or not visible to this reader. |

## `PUT /api/v1/books/{id}/saved`

**Add to the saved shelf**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`ReaderState`](#readerstate) | The book's state for this reader. |
| `404` | [`Error`](#error) | No such book, or not visible to this reader. |

## `PUT /api/v1/books/{id}/state`

**Mark finished or abandoned**

Only the two states a person sets by hand. `unread` and `reading`
derive from progress, so there is nothing to set them to — send
`null` to clear a mark and let progress speak again.

`reading_state` in the reply is the derived answer, which is not
always what was marked.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`ReaderState`](#readerstate) | The book's state for this reader. |
| `400` | [`Error`](#error) | A state other than finished, abandoned or null. |
| `404` | [`Error`](#error) | No such book, or not visible to this reader. |

## `GET /api/v1/collections`

**List collections**

Curated collections, plus this reader's own.

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The collections visible to this reader. |

## `POST /api/v1/collections`

**Create a collection**

`curated` is admin-only; a reader creating one gets a private collection.

### Request body

[`CollectionRequest`](#collectionrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `201` | [`Collection`](#collection) | The new collection. |
| `400` | [`Error`](#error) | A missing or empty name. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |

## `DELETE /api/v1/collections/{id}`

**Delete a collection**

Removes the shelf, never the books on it.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Deleted. |
| `403` | [`Denied`](#denied) | Not this reader's to delete. |
| `404` | [`Error`](#error) | No such collection. |

## `GET /api/v1/collections/{id}`

**One collection and its books**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The collection and one page of its books. |
| `404` | [`Error`](#error) | No such collection, or not this reader's to see. |

## `PATCH /api/v1/collections/{id}`

**Rename or describe a collection**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

[`CollectionRequest`](#collectionrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Collection`](#collection) | The updated collection. |
| `400` | [`Error`](#error) | An empty name. |
| `403` | [`Denied`](#denied) | Not this reader's to change. |
| `404` | [`Error`](#error) | No such collection. |

## `DELETE /api/v1/collections/{id}/books/{bookID}`

**Remove a book from a collection**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Collection`](#collection) | The collection, with its count updated. |
| `403` | [`Denied`](#denied) | Not this reader's to change. |
| `404` | [`Error`](#error) | No such collection or book. |

## `PUT /api/v1/collections/{id}/books/{bookID}`

**Add a book to a collection**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Collection`](#collection) | The collection, with its count updated. |
| `403` | [`Denied`](#denied) | Not this reader's to change. |
| `404` | [`Error`](#error) | No such collection or book. |

## `GET /api/v1/devices`

**List authorized devices**

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Every device holding a live token for this account. |

## `DELETE /api/v1/devices/{id}`

**Revoke a device**

Takes effect on that device's next request. Revoking your own logs you out.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `204` | — | Revoked. |
| `403` | [`Denied`](#denied) | Not your device to revoke. |
| `404` | [`Error`](#error) | No such device. |

## `DELETE /api/v1/editions/{id}/group`

**Split an edition onto a book of its own**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Grouping`](#grouping) | The new book the edition was moved to. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |
| `404` | [`Error`](#error) | No such edition. |
| `409` | [`Error`](#error) | That edition is already alone on its book. |

## `POST /api/v1/editions/{id}/group`

**Move an edition onto another book**

A manual decision, which a rescan will not undo.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Grouping`](#grouping) | Where the edition now sits. |
| `400` | [`Error`](#error) | A malformed body. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |
| `404` | [`Error`](#error) | No such edition or book. |

## `POST /api/v1/editions/{id}/handoff`

**Carry a position into another edition**

Read, then listen. Maps a position in this edition onto another one —
by aligning chapter lists where both have them, and falling back to
proportion where they cannot be aligned. `method` says which happened,
which is worth showing: a proportional jump in a sixteen-hour
audiobook can be minutes out.

Omit `percent` to hand off from this reader's own stored position,
which is the usual case.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

[`HandoffRequest`](#handoffrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Handoff`](#handoff) | Where to open the target edition. |
| `400` | [`Error`](#error) | A malformed body, or a target that is not an edition of the same book. |
| `404` | [`Error`](#error) | No such edition, or not visible to this reader. |

## `GET /api/v1/editions/{id}/progress`

**Where every device is in this edition**

Positions are kept **per device and never resolved into one**. A
client shows where this device was, and surfaces the others when they
disagree rather than silently adopting the newest.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Progress`](#progress) | Every device's position in this edition. |
| `404` | [`Error`](#error) | No such edition, or not visible to this reader. |

## `PUT /api/v1/editions/{id}/progress`

**Report this device's position**

Send the position for whichever kind of edition this is: `cfi` and
`page` for an ebook, `seconds` and `file_index` for an audiobook.
`percent` is required either way and is what cross-edition handoff and
sorting use.

`client_seq` is the device's own counter, incremented per save. It is
what makes divergence detectable: a device reporting a sequence that
does not follow from what the server last saw from it means the two
were edited apart, and the reply comes back with `diverged` set and
**both** positions kept.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Request body

[`ProgressRequest`](#progressrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Progress`](#progress) | The stored positions, including this one. |
| `400` | [`Error`](#error) | A malformed body, or a percent outside 0 to 1. |
| `404` | [`Error`](#error) | No such edition, or not visible to this reader. |

## `GET /api/v1/facets`

**Every facet and its values**

What a filter UI is drawn from. Counts are computed over the books the
reader may already see and over any filters supplied, so refining a
search updates the remaining counts — and no count can reveal a book
that is hidden.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Facet names mapped to their values. |

## `GET /api/v1/facets/{name}`

**One facet's values**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `name` | path | yes | One of the facet names. |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The facet's values. |
| `404` | [`Error`](#error) | No such facet. |

## `GET /api/v1/files/{id}/content`

**Stream or download a book file**

The bytes, exactly as they are on disk — **nothing is ever
transcoded**. M4B is AAC in an MP4 container and plays natively.

Range requests are supported and are how an audio player seeks;
`Accept-Ranges: bytes` is always sent. `ETag` and `If-Range` work, so
a resumed download cannot splice two different files together.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `Range` | header |  | A byte range, e.g. `bytes=0-1048575`. |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The whole file. |
| `206` | — | The requested range. |
| `404` | [`Error`](#error) | No such file, or not visible to this reader. |
| `416` | — | The range lies outside the file. |

## `GET /api/v1/groupings/uncertain`

**Groupings Bard is unsure about**

The review queue for editions matched below the confidence threshold.

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Uncertain groupings, least confident first. |
| `403` | [`Denied`](#denied) | Not allowed to curate. |

## `GET /api/v1/history`

**Books this reader has finished**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Finished books, most recent first. |

## `GET /api/v1/search`

**Search the library**

Full-text search over **metadata** — titles, authors, series,
narrators — not the text inside books. Accepts the same filters,
sorting and paging as `GET /books`, so a search can be narrowed by
facet.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `q` | query | yes | The search terms. |
| `` |  |  |  |
| `` |  |  |  |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`BookPage`](#bookpage) | Matching books, in the same envelope as a listing. |
| `400` | [`Error`](#error) | A missing query, an unknown filter, or a bad page. |

## `GET /api/v1/series`

**List series**

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Every series with at least one visible book. |

## `GET /api/v1/series/{id}`

**One series and its books**

Books come back in series order, which is what `position` is for.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | The series and one page of its books. |
| `404` | [`Error`](#error) | No such series. |

## `GET /api/v1/sync`

**What this reader would want held offline**

A manifest, not an instruction. The server says what is worth caching
and why; **the client decides what to hold**, because only the device
knows its storage, battery and network — and nothing here is licensed,
so there is nothing to revoke.

`total_bytes` is the whole manifest, for showing a cost before
downloading.

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`SyncManifest`](#syncmanifest) | Editions worth pre-caching. |

## `POST /api/v1/uploads`

**Begin a resumable upload**

Opens a session and tells you the chunk size to use. Send `sha256` of
the whole file up front; finalizing verifies it.

The size limit is refused here, before any bytes are sent. An
abandoned session is reaped after the configured TTL, which is what
stops a half-sent audiobook holding the disk.

### Request body

[`CreateUploadRequest`](#createuploadrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `201` | [`Upload`](#upload) | The session. |
| `400` | [`Error`](#error) | A malformed body, an unsupported format, or a size over the limit. |
| `403` | [`Denied`](#denied) | This account may not upload. |
| `404` | [`Error`](#error) | No such source. |
| `409` | [`Error`](#error) | That source cannot be written to. |

## `GET /api/v1/uploads/{id}`

**Session state, and which chunks are still missing**

What a resuming client asks for before sending anything.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Upload`](#upload) | The session. |
| `404` | [`Error`](#error) | No such session of this account's. |

## `PUT /api/v1/uploads/{id}/chunks/{offset}`

**Send one chunk**

The body is the raw bytes. `offset` must land on a chunk boundary —
an arbitrary range is refused, because resume is defined in whole
chunks. Re-sending a chunk already held is accepted and changes
nothing, so a client that lost its reply can simply repeat it.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `offset` | path | yes | Byte offset, which must be a multiple of `chunk_bytes`. |

### Request body

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Upload`](#upload) | The session, with this chunk no longer missing. |
| `400` | [`Error`](#error) | An offset off a boundary, or a chunk of the wrong length. |
| `404` | [`Error`](#error) | No such session of this account's. |
| `409` | [`Error`](#error) | The session is finalized or failed. |

## `POST /api/v1/uploads/{id}/finalize`

**Assemble and ingest**

Verifies the SHA-256 over the assembled bytes, then moves the file
into the source. The move is a rename within the same filesystem, so
a scan never sees a half-written book.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Upload`](#upload) | Ingested. |
| `400` | [`Error`](#error) | Chunks are still missing. |
| `404` | [`Error`](#error) | No such session of this account's. |
| `409` | [`Error`](#error) | The bytes do not match the declared SHA-256, or the name is taken. |

## `GET /api/v1/users/{id}/overrides`

**A reader's per-book age exceptions**

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Every book this reader has an exception for. |
| `403` | [`Denied`](#denied) | Not yours to see. |
| `404` | [`Error`](#error) | No such account. |

## `DELETE /api/v1/users/{id}/overrides/{bookID}`

**Clear an exception**

The book falls back to the ordinary age gate.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | — | Cleared. |
| `403` | [`Denied`](#denied) | Not yours to clear. |
| `404` | [`Error`](#error) | No such account or book. |

## `PUT /api/v1/users/{id}/overrides/{bookID}`

**Grant or refuse one book**

An exception for a single book, which outranks the age gate in both
directions: `allowed` true opens a book the reader's age would lock,
false closes one it would not.

| Parameter | In | Required | Description |
| --- | --- | --- | --- |
| `` |  |  |  |
| `` |  |  |  |

### Request body

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`Override`](#override) | The override. |
| `400` | [`Error`](#error) | A malformed body. |
| `403` | [`Denied`](#denied) | Not yours to set. |
| `404` | [`Error`](#error) | No such account or book. |

## Schemas

### Annotation

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cfi` | string |  |  |
| `cfi_end` | string |  | The far end of a highlight. |
| `created_at` | string (date-time) | yes |  |
| `deleted` | boolean |  | A tombstone, so a device that was away stops holding it. |
| `edition_id` | integer (int64) | yes |  |
| `kind` | string | yes |  |
| `note` | string |  |  |
| `page` | integer |  |  |
| `percent` | number |  |  |
| `quote` | string |  | The text as it stood when highlighted. |
| `seconds` | number |  |  |
| `seq` | integer (int64) | yes | Server-assigned order, and what `since` walks. |
| `updated_at` | string (date-time) | yes |  |
| `uuid` | string (uuid) | yes |  |

### AnnotationRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cfi` | any |  |  |
| `cfi_end` | any |  |  |
| `edition_id` | integer (int64) | yes |  |
| `kind` | string | yes |  |
| `note` | string |  |  |
| `page` | any |  |  |
| `percent` | any |  |  |
| `quote` | string |  |  |
| `seconds` | any |  |  |

### ApplyMatchRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `author` | string |  |  |
| `edition_id` | integer (int64) |  |  |
| `id` | string | yes |  |
| `provider` | string | yes |  |
| `q` | string |  | The search that produced the candidate, for re-finding it. |

### Book

A book as it appears in a listing. Per-reader fields — `reading_state`,
`percent`, `saved`, `rating` — belong to whoever is asking and are
never another profile's.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `added_at` | string (date-time) | yes |  |
| `authors` | string[] | yes |  |
| `duration_seconds` | number |  | Longest audiobook edition, when there is one. |
| `edition_count` | integer | yes |  |
| `formats` | string[] | yes | e.g. epub, pdf, m4b, mp3. |
| `has_cover` | boolean | yes |  |
| `id` | integer (int64) | yes |  |
| `kinds` | string[] | yes | ebook, audiobook, or both when a book has editions of each. |
| `languages` | string[] |  |  |
| `locked` | boolean |  | Visible but age-gated for this reader. Where a source hides locked books instead, they are absent entirely rather than marked. |
| `narrators` | string[] |  |  |
| `page_count` | integer |  |  |
| `percent` | number |  | Furthest position reached, 0 to 1. Absent when never opened. |
| `published_on` | string |  | The earliest edition's date; may be a year alone. |
| `rating` | integer |  |  |
| `reading_state` | string | yes |  |
| `required_age` | integer |  | Present only when `locked`, so a client can say what it unlocks at. |
| `saved` | boolean | yes |  |
| `series` | [`SeriesRef`](#seriesref) |  |  |
| `sort_title` | string | yes | Articles moved to the end, which is what to sort on. |
| `subtitle` | string |  |  |
| `title` | string | yes |  |
| `uncertain_grouping` | boolean |  | Bard grouped these editions by guess, so say so rather than present it as settled. |

### BookDetail

### BookPage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `books` | [`Book`](#book)[] | yes |  |
| `limit` | integer | yes |  |
| `offset` | integer | yes |  |
| `total` | integer | yes | Matches before paging, counting only what this reader may see. |

### Candidate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `asin` | string |  |  |
| `authors` | string[] |  |  |
| `cover_url` | string |  |  |
| `id` | string | yes | Provider-local, and what to send back to apply it. |
| `isbn` | string |  |  |
| `provider` | string | yes |  |
| `score` | number | yes |  |
| `subtitle` | string |  |  |
| `title` | string | yes |  |
| `would_match_automatically` | boolean | yes | False for candidates the matcher refused. They are listed anyway, because a person can recognise a match a score cannot. |
| `year` | integer |  |  |

### Chapter

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `href` | string |  | EPUB chapters. A link into the book's own contents. |
| `ordinal` | integer | yes |  |
| `start_seconds` | number |  | Audiobook chapters. Offset within the edition, not the file. |
| `title` | string |  |  |

### Collection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_count` | integer | yes | Counts only books this reader may see. |
| `created_at` | string (date-time) | yes |  |
| `curated` | boolean | yes | Admin-made and visible to everyone. Otherwise it is this reader's own. |
| `description` | string |  |  |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |

### CollectionRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `curated` | boolean |  | Admin-only. Ignored otherwise. |
| `description` | any |  |  |
| `name` | any |  |  |

### CreateUploadRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `filename` | string | yes |  |
| `format` | string | yes |  |
| `sha256` | string | yes | Hex digest of the whole file, verified on finalize. |
| `source_id` | integer (int64) | yes | Which source directory to ingest into. |
| `total_bytes` | integer (int64) | yes |  |

### Denied

An authorization failure that the reader is allowed to know about.
`reason` is the stable code to branch on; extra properties carry
whatever that reason needs, such as the age a book requires.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string | yes |  |
| `reason` | string | yes | A stable code; `error` is the sentence for a person. A reason is only ever given for something the reader already knows exists — hidden content is a `404` with no reason at all. |

### Device

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `active_user_id` | integer (int64) | yes | The profile this device is currently reading as, which may be a managed user. |
| `created_at` | string (date-time) | yes |  |
| `id` | integer (int64) | yes |  |
| `last_seen_at` | string (date-time) |  |  |
| `last_seen_ip` | string |  | Recorded for the owner's devices list. Never used as identity. |
| `name` | string | yes |  |

### Edition

One manifestation of a book — the EPUB, a particular narration, an abridgement.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `abridged` | boolean | yes |  |
| `asin` | string |  |  |
| `chapters` | [`Chapter`](#chapter)[] |  |  |
| `duration_seconds` | number |  |  |
| `files` | [`File`](#file)[] | yes | More than one for an audiobook split across tracks, in `ordinal` order. |
| `format` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `isbn` | string |  |  |
| `kind` | string | yes |  |
| `language` | string |  |  |
| `narrators` | string[] |  |  |
| `page_count` | integer |  |  |
| `published_on` | string |  |  |
| `publisher` | string |  |  |
| `uncertain_grouping` | boolean |  |  |

### Error

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string | yes | Human-readable, and not intended for matching on. |

### FacetValue

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `count` | integer | yes |  |
| `label` | string | yes | What to show. |
| `value` | string | yes | What to send back as a filter. Not always human-readable — an author is an id. |

### File

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `content_url` | string | yes | Path to stream from, relative to the server. |
| `duration_seconds` | number |  |  |
| `etag` | string | yes | Use with `If-Range` so a resumed download cannot splice two files. |
| `id` | integer (int64) | yes |  |
| `ordinal` | integer | yes | Playback order within the edition, from 1. |
| `size_bytes` | integer (int64) | yes |  |

### Genre

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes |  |
| `slug` | string | yes | What the `genre` facet filters on. |

### Grouping

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_id` | integer (int64) | yes |  |
| `edition_id` | integer (int64) | yes |  |

### Handoff

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `chapter_href` | string |  | Where to open, for an EPUB target. |
| `chapter_ordinal` | integer |  |  |
| `chapter_title` | string |  |  |
| `from_edition_id` | integer (int64) | yes |  |
| `method` | string | yes | `chapter` aligned both chapter lists and interpolated inside the match. `percent` is the fallback, and is the less precise of the two — worth saying so in a long audiobook. |
| `percent` | number | yes |  |
| `seconds` | number |  | Where to start, for an audiobook target. |
| `to_edition_id` | integer (int64) | yes |  |

### HandoffRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `chapter_ordinal` | any |  | Hand off from a chapter rather than a proportion. |
| `chapter_percent` | number |  | How far into that chapter, 0 to 1. |
| `percent` | any |  | Omit to hand off from this reader's stored position. |
| `to_edition_id` | integer (int64) | yes |  |

### HandoffTarget

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `chapters` | integer | yes | How many chapters are known, which is what decides chapter alignment. |
| `edition_id` | integer (int64) | yes |  |
| `format` | string | yes |  |
| `kind` | string | yes |  |
| `percent` | number | yes | Where this reader would arrive in this edition. |

### HistoryEntry

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_id` | integer (int64) | yes |  |
| `finished_at` | string (date-time) |  |  |
| `rating` | integer |  |  |
| `title` | string | yes |  |

### LoginRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device_name` | string |  | Shown in the authorized devices list, e.g. "Emily's iPhone". |
| `password` | string (password) | yes |  |
| `username` | string | yes |  |

### LoginResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device` | [`Device`](#device) | yes |  |
| `token` | string | yes | Opaque. Shown once, stored hashed, and revocable. |
| `user` | [`User`](#user) | yes |  |

### Override

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed` | boolean | yes |  |
| `book_id` | integer (int64) | yes |  |
| `user_id` | integer (int64) | yes |  |

### Position

One device's place in an edition.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cfi` | string |  | EPUB position. |
| `client_seq` | integer (int64) | yes | The reporting device's own counter. |
| `device_id` | integer (int64) | yes |  |
| `device_name` | string | yes | So a divergence can name the device rather than a number. |
| `edition_id` | integer (int64) | yes |  |
| `file_index` | integer |  | Which file of a multi-file audiobook. |
| `page` | integer |  | PDF page, or an EPUB's rendered page where one is known. |
| `percent` | number | yes | 0 to 1, and the only field every edition kind has. |
| `seconds` | number |  | Audiobook offset within the edition, not within the file. |
| `this_device` | boolean | yes | Whether this is the position of the device that asked. |
| `updated_at` | string (date-time) | yes |  |

### Progress

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `diverged` | boolean | yes | Two or more devices hold positions that did not follow from one another. Both are kept; resolving them is the client's to offer, and is deliberately never done silently. |
| `edition_id` | integer (int64) | yes |  |
| `positions` | [`Position`](#position)[] | yes |  |

### ProgressRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cfi` | any |  |  |
| `client_seq` | integer (int64) | yes | This device's counter, incremented on every save. |
| `file_index` | any |  |  |
| `page` | any |  |  |
| `percent` | number | yes |  |
| `seconds` | any |  |  |

### ReaderState

One book as it stands for the reader asking. Never another profile's.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_id` | integer (int64) | yes |  |
| `finished_at` | string (date-time) |  |  |
| `marked` | string |  | What was set by hand, when anything was. |
| `percent` | number |  |  |
| `rating` | integer |  |  |
| `reading_state` | string | yes | Derived from progress and any hand-set mark. |
| `saved` | boolean |  |  |
| `saved_at` | string (date-time) |  |  |

### Series

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_count` | integer | yes | Counts only what this reader may see. |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |

### SeriesRef

A book's place in a series, as carried on the book itself.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `position` | number |  | A number so a novella can sit at 1.5. |

### Session

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device` | [`Device`](#device) | yes |  |
| `profiles` | [`User`](#user)[] | yes | Who this device may switch to, including the current profile. |
| `user` | [`User`](#user) | yes |  |

### SwitchRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `pin` | string |  | Required when the target profile has one. |
| `user_id` | integer (int64) | yes |  |

### SyncEdition

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_id` | integer (int64) | yes |  |
| `edition_id` | integer (int64) | yes |  |
| `files` | [`SyncFile`](#syncfile)[] | yes |  |
| `format` | string | yes |  |
| `kind` | string | yes |  |
| `reason` | string | yes | Why this is worth holding, so a client can prioritise. |
| `size_bytes` | integer (int64) | yes |  |
| `title` | string | yes |  |

### SyncFile

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `content_url` | string | yes |  |
| `etag` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `ordinal` | integer | yes |  |
| `size_bytes` | integer (int64) | yes |  |

### SyncManifest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `editions` | [`SyncEdition`](#syncedition)[] | yes |  |
| `reasons` | string[] | yes | Every reason this server may give, in priority order. |
| `total_bytes` | integer (int64) | yes |  |

### UncertainGrouping

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `book_id` | integer (int64) | yes |  |
| `book_title` | string | yes |  |
| `confidence` | number | yes | How sure the matcher was, below the threshold for certain. |
| `edition_id` | integer (int64) | yes |  |
| `format` | string | yes |  |
| `reason` | string |  |  |

### Upload

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `chunk_bytes` | integer (int64) | yes | The size to send. Resume is defined in whole chunks of this. |
| `error` | string |  | Why it failed, when state is failed. |
| `filename` | string | yes |  |
| `format` | string | yes |  |
| `id` | string | yes |  |
| `missing_chunks` | integer (int64)[] | yes | Byte offsets still wanted. Empty means ready to finalize. |
| `state` | string | yes |  |
| `total_bytes` | integer (int64) | yes |  |

### User

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `age` | integer |  | Present only when a birth date is set. Absent is not zero. |
| `can_upload` | boolean | yes |  |
| `display_name` | string | yes |  |
| `has_pin` | boolean | yes | Whether switching into this profile asks for a PIN. |
| `id` | integer (int64) | yes |  |
| `is_admin` | boolean | yes |  |
| `managed` | boolean | yes | A passwordless profile switched into from its owner's device. |
| `restricted` | boolean | yes | Subject to age gating. |
| `username` | string | yes |  |

