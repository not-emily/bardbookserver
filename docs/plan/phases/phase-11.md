# Phase 11: Web Admin UI

> **Depends on:** Phase 8, Phase 9, Phase 10
> **Enables:** Phase 12 (a shippable install experience)
>
> See: [Full Plan](../plan.md)

## Goal

Make Bard installable and operable without SSH: first-run setup, sources, users, devices, match fixing, and the age-rating queue.

## Key Deliverables

- First-run setup wizard: create admin, add the first source, choose TLS mode, run the first scan
- Source management: path, enable, sidecar toggle, `default_min_age`, `hide_when_locked`, scan now
- User management: create, managed users and PINs, `birth_date` and `restricted`, `can_upload`
- Authorized devices list with revoke
- Fix Match: uncertain groupings and mismatched metadata, searchable and correctable
- Age-rating queue: unrated books with suggestions, bulk confirm
- Genre mapping review for unmapped `raw_subjects`
- Job and scan status with progress
- Upload form — needs a secure context for `crypto.subtle`, and says so rather
  than failing cryptically

## Sub-phases

Nine deliverables is more than one session, so this phase runs in four.

### 11.1: The shell and its authentication
Cookie sessions with CSRF, `embed.FS`, layout, sign-in, the admin gate, and the
setup gate. Done.

### 11.2: The setup wizard
The first source and the first scan. Done.

**TLS is reported, not written.** A form that rewrites `bard.yml` destroys the
comments YAML was chosen for, and the file may be read-only or absent. The
wizard shows the current mode, explains the recipes, and warns about
`trusted_proxies`. See the decision log.

### 11.3: Sources, users, devices
Done. A source's path is editable and the move carries the indexed files, since
re-adding would discard every edition and the reading progress hanging off it.
Deleting a source is deliberately not offered; disabling is the reversible
version.

### 11.4a: The rating queue and genre mapping
Done. Both consume Phase 8's output, and they share the `genre_aliases`
foundation.

### 11.4b: Fix Match and the upload form
Done, which completes Phase 11.

**htmx is not vendored, and will not be.** The plan assumed it, but every page
here turned out to be a plain form, and the one thing that genuinely needs
scripting — chunking a file with `File.slice` and hashing it with
`crypto.subtle` — is something htmx cannot do. Avoiding the dependency keeps
`go build` the whole story and leaves no third-party JS to track. Job and scan
progress use a `<meta refresh>`, which costs nothing and needs no library.

## Files to Create

- `internal/web/templates/*.html`
- `internal/web/static/` — vendored htmx, minimal CSS
- `internal/web/embed.go` — `embed.FS`, template parsing, static handler
- `internal/api/web_handlers.go` — the pages

**Handlers live in `internal/api`, not `internal/web`.** The UI needs the same
store, policies and logger the Server already holds, and the cookie path has to
stay beside the Bearer path it must agree with. `internal/web` owns the
templates and assets, which is the separable half.

## Dependencies

**Internal:** Phases 8, 9, 10 for the features it exposes; Phase 2 for admin authorization.

**External:** none. htmx turned out to earn nothing — see 11.4b.

## Implementation Notes

Go templates plus vendored htmx, embedded with `embed.FS`. **No Node and no build step** — a contributor runs `go build` and gets a working UI. The whole point of this phase is that "install it and point it at a directory" works for someone who will not edit YAML.

The setup wizard runs only while no admin exists, and that gate must be airtight: an exposed setup wizard is an unauthenticated admin-creation endpoint. Bind the check to "zero users in the database," not a config flag a bad restore could reset.

The rating queue is what makes 5000 books tractable: list unrated books with their suggested ages from Phase 8, grouped by suggestion, and bulk-confirm. **Confirming is an explicit act** — the queue never applies a suggestion on its own, per Decision 6.

Surface the inheritance from Decision 5 honestly: show which books take their age from the source versus their own override, so changing a source default has visible consequences rather than silently re-restricting a thousand books.

Every page is admin-only except the setup wizard. This is not a reading UI; reading lives in the apps.

Keep it plain. This UI is used at install time and then a few times a year.

## Validation

- [x] A fresh install reaches the setup wizard, creates an admin, adds a source, and completes a scan
      *(11.2: walked against the real sample library — 6 books, 7 editions, 58 files)*
- [x] The wizard is unreachable once a user exists
- [x] Editing a source's `default_min_age` visibly changes which books a restricted user can read
      *(11.3: the edit form counts the books inheriting it before you save)*
- [x] A device can be revoked from the UI and stops working immediately
- [x] A managed user can be created with a PIN
- [x] Fix Match finds a book, lists candidates, and applying one updates metadata
      *(11.4b: verified live — five real Open Library candidates for Anne of
      Green Gables, applying one set the title, description and author)*
- [x] A hand-edited field shows as locked and survives a refresh triggered from the UI
      *(11.4b: and unlocking clears the sidecar too, or the old value would
      keep outranking the file forever)*
- [x] The rating queue bulk-confirms suggestions and leaves unconfirmed books unrated
- [x] Unmapped subjects are listed and can be mapped to a canonical genre
- [~] Scan progress is visible while a scan runs
      *(11.2: a meta refresh while a job is queued, with the counts climbing.
      htmx polling replaces it in 11.4; per-book progress is not persisted)*
- [x] A non-admin session cannot reach any admin page
- [x] The UI works with no outbound network access
