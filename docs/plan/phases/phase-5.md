# Phase 5: Grouping

> **Depends on:** Phase 4
> **Enables:** Phase 6 (what clients browse is the grouped book)
>
> See: [Full Plan](../plan.md)

## Goal

Collapse editions of the same title into one book automatically, and make manual corrections permanent.

## Key Deliverables

- Title and author normalization (case, punctuation, articles, subtitles, series suffixes)
- Match key generation, with ISBN/ASIN as a strong signal when present
- Auto-grouping during scan reconciliation
- Merge and split endpoints
- Grouping decisions persisted to sidecar so a rescan cannot undo them
- Confidence score retained per grouping, so the admin UI can surface uncertain matches

## Files to Create

- `internal/grouping/normalize.go`
- `internal/grouping/matcher.go`
- `internal/grouping/decisions.go` — persistence and replay of manual merges/splits
- `internal/db/migrations/0006_grouping.sql` — decision records, confidence
- `internal/api/grouping_handlers.go`
- `internal/grouping/matcher_test.go`

## Dependencies

**Internal:** Phase 4 scanner and the metadata precedence function.

**External:** none new.

## Implementation Notes

**Manual decisions outrank the matcher, permanently.** This is the whole point of the phase. A decision record stores a stable identity (ISBN/ASIN where available, otherwise content hash) rather than a row id, because row ids do not survive a database rebuild and Decision 3 promises the index is rebuildable from disk.

Normalization has to be conservative. Collapsing two genuinely different books is far worse than failing to group two editions of one: an over-eager merge hides a book behind the wrong cover and needs a manual split, while a missed merge just shows two cards. Tune for precision, and let the admin UI's uncertain-match queue catch the rest.

Known hazards worth test fixtures: subtitle divergence between ebook and audiobook editions of the same title; "The" and "A" leading articles; author name order and initials (`J.K. Rowling` / `J. K. Rowling` / `Rowling, J.K.`); translated titles; series name embedded in the title (`Harry Potter and the…` vs a `Harry Potter #1` series field).

Abridged and unabridged editions group under one book, distinguished by the edition's abridged flag, per Decision 2. Two narrations likewise group — that is why edition is the general axis rather than format.

## Validation

- [ ] An EPUB and an M4B of the same title group into one book with two editions
- [ ] Two narrations of one title group into one book with two audiobook editions
- [ ] Matching ISBNs force a group even when titles differ cosmetically
- [ ] Differing ISBNs prevent a group even when titles match exactly
- [ ] A manual split survives a full rescan
- [ ] A manual merge survives deleting the database and rescanning from disk
- [ ] Author name variants normalize to one match key
- [ ] Two distinct books with similar titles are not merged
- [ ] Grouping confidence is recorded and uncertain matches are queryable
