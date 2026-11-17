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
- Upload form

## Sub-phases

Nine deliverables is more than one session, so this phase runs in four.

### 11.1: The shell and its authentication
Cookie sessions with CSRF, `embed.FS`, layout, sign-in, the admin gate, and the
setup gate. Done.

### 11.2: The setup wizard
The first source, TLS mode, and the first scan.

### 11.3: Sources, users, devices

### 11.4: Fix Match, the rating queue, genre mapping, job status, upload

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

**External:** vendored htmx (one file, committed — no CDN, no Node, no build step).

**htmx is not vendored yet.** 11.1 needs none of it: sign-in, sign-out and the
dashboard are plain forms. Pulling a third-party file into the repo wants an
explicit decision about version and provenance, so it waits until 11.4, where
live job status is the first thing that actually needs it.

## Implementation Notes

Go templates plus vendored htmx, embedded with `embed.FS`. **No Node and no build step** — a contributor runs `go build` and gets a working UI. The whole point of this phase is that "install it and point it at a directory" works for someone who will not edit YAML.

The setup wizard runs only while no admin exists, and that gate must be airtight: an exposed setup wizard is an unauthenticated admin-creation endpoint. Bind the check to "zero users in the database," not a config flag a bad restore could reset.

The rating queue is what makes 5000 books tractable: list unrated books with their suggested ages from Phase 8, grouped by suggestion, and bulk-confirm. **Confirming is an explicit act** — the queue never applies a suggestion on its own, per Decision 6.

Surface the inheritance from Decision 5 honestly: show which books take their age from the source versus their own override, so changing a source default has visible consequences rather than silently re-restricting a thousand books.

Every page is admin-only except the setup wizard. This is not a reading UI; reading lives in the apps.

Keep it plain. This UI is used at install time and then a few times a year.

## Validation

- [~] A fresh install reaches the setup wizard, creates an admin, adds a source, and completes a scan
      *(11.1: admin created and signed in; the source and the scan are 11.2)*
- [x] The wizard is unreachable once a user exists
- [ ] Editing a source's `default_min_age` visibly changes which books a restricted user can read
- [ ] A device can be revoked from the UI and stops working immediately
- [ ] A managed user can be created with a PIN
- [ ] Fix Match finds a book, lists candidates, and applying one updates metadata
- [ ] A hand-edited field shows as locked and survives a refresh triggered from the UI
- [ ] The rating queue bulk-confirms suggestions and leaves unconfirmed books unrated
- [ ] Unmapped subjects are listed and can be mapped to a canonical genre
- [ ] Scan progress is visible while a scan runs
- [x] A non-admin session cannot reach any admin page
- [x] The UI works with no outbound network access
