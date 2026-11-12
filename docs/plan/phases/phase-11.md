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

## Files to Create

- `internal/web/templates/*.html`
- `internal/web/static/` — vendored htmx, minimal CSS
- `internal/web/handlers.go`, `internal/web/setup.go`
- `internal/web/embed.go` — `embed.FS`

## Dependencies

**Internal:** Phases 8, 9, 10 for the features it exposes; Phase 2 for admin authorization.

**External:** vendored htmx (one file, committed — no CDN, no Node, no build step).

## Implementation Notes

Go templates plus vendored htmx, embedded with `embed.FS`. **No Node and no build step** — a contributor runs `go build` and gets a working UI. The whole point of this phase is that "install it and point it at a directory" works for someone who will not edit YAML.

The setup wizard runs only while no admin exists, and that gate must be airtight: an exposed setup wizard is an unauthenticated admin-creation endpoint. Bind the check to "zero users in the database," not a config flag a bad restore could reset.

The rating queue is what makes 5000 books tractable: list unrated books with their suggested ages from Phase 8, grouped by suggestion, and bulk-confirm. **Confirming is an explicit act** — the queue never applies a suggestion on its own, per Decision 6.

Surface the inheritance from Decision 5 honestly: show which books take their age from the source versus their own override, so changing a source default has visible consequences rather than silently re-restricting a thousand books.

Every page is admin-only except the setup wizard. This is not a reading UI; reading lives in the apps.

Keep it plain. This UI is used at install time and then a few times a year.

## Validation

- [ ] A fresh install reaches the setup wizard, creates an admin, adds a source, and completes a scan
- [ ] The wizard is unreachable once a user exists
- [ ] Editing a source's `default_min_age` visibly changes which books a restricted user can read
- [ ] A device can be revoked from the UI and stops working immediately
- [ ] A managed user can be created with a PIN
- [ ] Fix Match finds a book, lists candidates, and applying one updates metadata
- [ ] A hand-edited field shows as locked and survives a refresh triggered from the UI
- [ ] The rating queue bulk-confirms suggestions and leaves unconfirmed books unrated
- [ ] Unmapped subjects are listed and can be mapped to a canonical genre
- [ ] Scan progress is visible while a scan runs
- [ ] A non-admin session cannot reach any admin page
- [ ] The UI works with no outbound network access
