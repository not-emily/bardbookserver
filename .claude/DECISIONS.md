# Decisions Log - bardbookserver

Decisions made during implementation, including those that changed or went
beyond what was planned.

## 2026-11-17: The wizard reports TLS rather than writing the config

**Decision:** The setup wizard's last step shows `bind_addr`, `tls.mode` and a
`trusted_proxies` warning, and explains the three deployment recipes. It does
not write `bard.yml`.

**Rationale:** The plan lists "choose TLS mode" as a wizard deliverable, and a
web form that rewrites the config file is the obvious reading. But YAML was
chosen in the first place *because it supports comments*, and round-tripping
through yaml.v3 destroys them — so the wizard would silently eat a deployment's
own notes. The file may also be root-owned, read-only, or absent entirely with
defaults in use, and the change needs a restart to take effect regardless.

Telling the truth is more useful than a form that half works: every documented
recipe except the direct port-forward terminates TLS in front of Bard, so the
answer is usually "nothing to set here, and mind `trusted_proxies`".

**Alternatives Considered:** Writing the file and prompting for a restart,
which destroys comments and fails on a read-only config. A separate settings
table shadowing the config, which gives two places a value can come from and
no way to tell which won.

**Impact:** `trusted_proxies` gets its warning in front of whoever is
installing, which is the fourth place that setting has turned out to be
load-bearing. Phase 12's deploy recipes are where the detail belongs.

---

## 2026-11-17: Setup state is derived, and the scan is not a step

**Decision:** `setupStep` returns the admin step when no users exist, the
source step when no sources exist, and nothing otherwise. The first scan is the
wizard's last page but not a gate: adding a source completes setup.

**Rationale:** Deriving it means a half-finished wizard resumes where it
actually stopped, with no flag for a bad restore to reset — the same reasoning
that already binds the admin gate to counting users.

Gating on the scan would be the mistake. An install that chooses to scan later,
or whose first scan fails on a bad path, would be held in the wizard with no
way out, and a "skip" button would just be the flag again under another name. A
source is the last thing setup genuinely needs.

**Alternatives Considered:** A `setup_complete` flag, which is the thing the
plan already warns about for the admin gate. Gating on a finished scan, which
traps an install that does not want one yet.

**Impact:** The middleware order is load-bearing and commented as such: with no
users there is nobody to sign in as, so that outranks the sign-in redirect;
every later step needs an account, so resuming happens after authentication.
Resuming only redirects a GET, since steering a form submission would drop what
it carried. The source form doubles as the plain add-source page once setup is
complete, which v1 phase 11.3 gets for free.

---

## 2026-11-16: A browser is a device, and cookie auth is the only thing CSRF guards

**Decision:** The admin UI signs in by form and receives its device token in an
HttpOnly `bard_session` cookie. `requireAuth` reads the cookie as a second
source after the Authorization header, and applies one rule: **a request that
authenticated by cookie must echo a CSRF token on any non-GET; a request that
authenticated by Bearer never needs one.** The token is a per-device random in
`devices.csrf_token`.

**Rationale:** Plex's model is a client-generated `X-Plex-Client-Identifier`
plus a per-device token, with the IP and browser string recorded for display
only — not identity. Bard is already stronger, since the token is
server-generated and the device row is keyed on its hash. What does not
transfer is the transport: Plex Web is a single-page app, so every request is
XHR and can set a header. This UI is Go templates, and **a page navigation
cannot set an Authorization header** — so the token has to be in a cookie.

Making the browser a device rather than a separate session keeps one
revocation surface. A browser appears in the authorized-devices list, and
revoking it signs the browser out on the next request, because the token lookup
already hits the database every time.

The CSRF rule is phrased on the *source of authentication* rather than the
route, which is what keeps the apps untouched: they send a header, so nothing
changes for them, and no new route can forget the check.

**Alternatives Considered:** A token in `localStorage` with a JS client, which
has no CSRF problem at all but means page loads are unauthenticated — so it
needs a SPA, contradicting "no Node, no build step". A separate `sessions`
table, which is a second auth mechanism and a second revocation surface, and
leaves the devices screen either incomplete or merging two tables.

**Impact:** `Secure` is set only where the client actually used TLS, which
behind a tunnel is known from `X-Forwarded-Proto` and only from a *trusted*
proxy — `trusted_proxies` is load-bearing for a third time. `SameSite=Lax`,
since `Strict` would break an inbound link to the UI and Lax already blocks
cross-site POST. An empty stored token never matches, so a device issued before
this existed cannot be driven by a cookie. Verified by neutering the check and
watching the write tests fail.

---

## 2026-11-16: A chunk is an offset on a boundary, not an arbitrary range

**Decision:** `PUT /api/v1/uploads/{id}/chunks/{offset}` requires the offset to
be a multiple of the session's `chunk_bytes`, and every chunk to be exactly
that length except the last. One row per delivered ordinal in `upload_chunks`.

**Rationale:** The plan asks for chunks at explicit offsets, which this is, but
accepting *any* offset would mean storing and merging byte ranges — and then
"what is still missing" is interval arithmetic, and an overlapping resend is a
correctness question rather than a no-op. On a boundary, an offset is an
ordinal: missing chunks are a set difference, a resend is an INSERT OR IGNORE
over a primary key, and a wrong length is caught instead of silently
truncating.

Assembly is a single file opened with `WriteAt`, so the bytes land in place
with no reassembly pass over a gigabyte at the end.

**Alternatives Considered:** Arbitrary ranges, as above. Numbering chunks in
the URL instead of using an offset, which is the same thing with less
information in the request and a small departure from the plan's wording for no
gain.

**Impact:** `chunk_bytes` is fixed on the session at creation, so changing the
configured size cannot corrupt a transfer already in flight. A test asserts
that offsets of 1, 512, `total_bytes` and -1 are all refused.

---

## 2026-11-16: Bytes assemble inside the source, in a directory the scan skips

**Decision:** A partial upload is written to `<source>/.bard-ingest/<id>.part`
rather than under the data directory, and moved into the library layout on
finalize.

**Rationale:** The move has to be a rename rather than a copy. The data
directory and a media source are routinely on different disks here —
audiobooks are large and the plan says so — and a cross-device rename fails,
leaving a 700MB copy as the fallback. Assembling inside the source makes the
final step atomic and free.

The directory is hidden because `scan.skipName` already skips anything starting
with a dot, so a transfer in progress is invisible to the walker with no new
rule to get wrong. Writing a probe file there at session creation is also how a
read-only source is detected, which the plan wants surfaced before bytes are
sent rather than at the end of a long transfer: a mode bit is not enough,
because a read-only mount reports permissions that look fine.

**Alternatives Considered:** Assembling under the data directory, which is
tidier — nothing incomplete is ever inside the media tree — but pays a
cross-device copy of the whole file and risks a half-copied file in the tree
anyway. Assembling in a visible directory and teaching the walker to skip it,
which is a second place to keep correct.

**Impact:** A rejected or abandoned upload is deleted, so nothing incomplete
survives in the source. Filing the file into `Author/Series/## - Title` means
Bard renames a file — which is not what Decision 3 forbids: it protects files
that already exist, and this one is not part of the library until it lands.
A title read out of an uploaded file is untrusted, so path separators and
control characters are stripped and `TestADestinationCannotEscapeTheSource`
holds that.

---

## 2026-11-16: The gate rides on the query, so no read path can omit it

**Decision:** `db.Gate` — the account id, whether it is restricted, and its age
— is stored on the `db.Query` itself by `BookScope.Apply`. The columns, the
sort and the filters all read it off the query rather than taking it as a
parameter.

**Rationale:** Every read path already composes through `scopedBooks`, which is
the single call to `BookScope.Apply`. Putting the gate there means a listing,
a search, a facet count and a by-id fetch are all gated by construction, with
no new argument at any of the twenty-odd call sites and no way to assemble a
book query that forgot to. The alternative — threading a gate parameter
through `Books`, `Book`, `ApplyBookSort`, `ApplyBookFilters` and `FacetValues`
— is the same safety resting on twenty call sites being right.

The age is computed once in Go from the authenticated user and interpolated,
rather than derived from `birth_date` in SQL. SQL could do it, but that would
be a second Go/SQL twin to keep aligned, and this phase already has one in
`EffectiveMinAge`. One twin is a documented hazard; two is a pattern.

**Alternatives Considered:** A gate parameter on every read function, as
above. Computing the age in SQL from `birth_date`, which adds the twin.
Resolving the gate inside `db` from the user id, which puts a query inside
every other query.

**Impact:** The zero `Gate` gates nothing, so a query built without the scope
fails open — which is why `TestNoReadPathLeaksALockedBook` was verified by
neutering the scope and watching it fail across list, search and detail.
`Clone` copies the gate, since `Books` and `CountBooks` both clone.

---

## 2026-11-16: An unrated book is locked, not hidden

**Decision:** A book whose effective `min_age` is NULL is visible to a
restricted account and marked `locked` with no `required_age`. Only a source
with `hide_when_locked` removes a book outright.

**Rationale:** Caught by writing the leak test and asserting the wrong thing:
I had expected an unrated book to be absent. But `ReasonUnrated` reads "Nobody
has rated this one yet — ask a parent", which is a message for a card someone
can see. Hiding the book would make the reason unreachable and leave a child
with no way to ask.

It also keeps the two mechanisms distinct, which Decision 7 wants: *unrated*
is a gap in the library's data and shows as a locked card; *hidden* is a
deliberate per-source choice and shows as nothing at all.

`required_age` is omitted rather than zero, because there is no age — a client
shows "ask a parent" instead of a number.

**Alternatives Considered:** Hiding unrated books from restricted accounts,
which fails safe but silently shrinks the library and makes the reason code
dead. Treating a missing age as 18, which invents a rating nobody set.

**Impact:** `required_age` is surfaced only on a locked book, so an
unrestricted reader never sees another book's `min_age`.

---

## 2026-11-16: The genre list is flat, and genres are created on demand

**Decision:** `taxonomy.Canonical` is 44 flat genres with no hierarchy, and a
`genres` row is inserted the first time a book needs it rather than seeded by a
migration or a startup pass. `genres.parent_id` from Phase 3 stays unused.

**Rationale:** A hierarchy would make the facet ask which level it is showing,
and "Epic Fantasy under Fantasy" is a distinction provider data cannot support
— the same string arrives as `FICTION / Fantasy / Epic`, `Fantasy fiction` and
`fantasy` depending on where it came from, and none of those place it in a
tree.

Creating on demand removes the only way the table and the list could disagree.
A seeding migration would need a new migration every time the list grows, which
the plan says it will; a startup pass would need a hook in every entry point
and could still be skipped. Resolving a slug inside the transaction that files
the book means the table cannot drift, and the facet lists only genres
something is actually filed under — which is what a browsing UI wants anyway.

**Alternatives Considered:** Two levels with Fiction and Nonfiction on top,
which doubles the mapping work and makes every facet query choose a level.
Seeding from a migration, which is the drift above.

**Impact:** `TestEveryAliasPointsAtACanonicalGenre` is the guard the
on-demand insert needs: a typo in the alias table would otherwise fail at scan
time rather than in the suite, which is the same reasoning as the policy reason
coverage test. An admin UI listing all genres reads the Go list, not the table.

---

## 2026-11-16: A BISAC heading is split by the taxonomy, not by each provider

**Decision:** Providers pass subject strings through whole, and
`taxonomy.Classify` splits a heading on `/` and classifies each part. A heading
where no part maps is kept whole in `raw_subjects`.

**Rationale:** Found by test. Google Books was splitting its own categories in
8.2, which left Open Library's `FICTION / Fantasy / Epic` matching nothing at
all, because it arrives as one string from there and from any client. Splitting
in one place fixes every provider at once and is the only place that knows what
a part is worth.

Keeping the unmapped heading whole rather than per part matters for the next
person: `raw_subjects` holding `FICTION / Fantasy / Epic` says what to add to
the table, where three rows reading "Fiction", "Fantasy" and "Epic" do not.

**Alternatives Considered:** Splitting in each provider, which is what 8.2 did
and what missed. Classifying only the last part, which drops the audience
marker the age suggestion needs out of `JUVENILE FICTION / Fantasy & Magic`.

**Impact:** An explicit noise list drops "Fiction", "General", "In library" and
the like rather than keeping them, since they say nothing and would attach to
every row in `raw_subjects`. `raw_subjects.provider` is left NULL: `Resolve`
takes one provider's subject list wholesale and does not record whose, and
threading provenance through every field is not worth a column nothing reads.

---

## 2026-11-16: Fix Match names an edition, and applying one pins and locks

**Decision:** The endpoint is `/api/v1/books/{id}/match` as planned, but it
resolves to exactly one edition — implied when a book has one, named with
`edition_id` when it has several, and a 400 otherwise. Applying a match records
a pin in `provider_matches` *and* sets the lock bits for the fields the chosen
record filled, then writes both to the sidecar.

**Rationale:** A provider record describes one manifestation. One choice cannot
correct both the EPUB and the audiobook of a title: the ISBN belongs to one and
the ASIN to the other, and an Audnexus record is meaningless against an EPUB.
Refusing an ambiguous book is better than silently picking an edition or
writing the same ISBN onto both.

Pinning and locking look redundant and are not. The pin keeps a rescan
consistent *while providers are enabled*; the locks keep the correction when a
provider is later disabled, and the sidecar keeps it when the database is
deleted and rebuilt from disk, which the plan's success metrics require. Only
the fields the record actually filled are locked, so an empty description does
not freeze the field against a better provider later.

**Alternatives Considered:** Pinning without locking, which loses the
correction the moment a provider is turned off. Locking every field, which
freezes a book against every future parser improvement. Applying to every
edition of the book, which writes one edition's identifiers onto another.

**Impact:** `DELETE /api/v1/books/{id}/match` unpins without discarding the
values, because asking for the match to be reconsidered is not asking for the
metadata to be thrown away; unlocking a field is a separate act that v1 phase
11 surfaces. An automatic match is deliberately *not* pinned, so a better score
stays free to win.

---

## 2026-11-16: A chosen candidate is re-found, not remembered

**Decision:** Applying a match re-runs the search the candidate came from and
finds it again by id, rather than holding the candidate between the search and
the choice. The query the match was found by is stored on the pin as JSON.

**Rationale:** The response cache makes the re-run free, so there is nothing to
gain from server-side state between two requests — no session, no expiry, no
candidate store to keep consistent. `Candidate` carries an unexported payload
that only its own provider can read, so it was never serialisable anyway.

Storing the query is what makes a pin durable. A rescan searches on whatever
the file says *today*; if a person corrected a badly titled file, searching on
that title again would not return the record they chose, and the pinned id
would be unfindable. The stored query reproduces the search that found it.

**Alternatives Considered:** Fetching the provider record by id alone, which
Open Library cannot satisfy — its work endpoint carries neither the authors'
names nor the cover id, both of which the search already returned. Caching
candidates server-side, which is state with a lifetime to get wrong.

**Impact:** A pin whose record a provider has withdrawn produces a warning on
every scan rather than silently reverting, which is the right way round. The
`Want` flags are excluded from the stored query, since they describe the file
as it was at the time.

---

## 2026-11-16: A shared quota is not counted, it is obeyed

**Decision:** No request budget is tracked. When a provider answers 429 or 403
with a body naming a *daily* quota, an in-memory breaker stops asking that
provider until the next UTC midnight. A per-minute rate limit is still retried,
and the cache is still served while the breaker is open.

**Rationale:** Google Books states its limit as a thousand queries a day rather
than a rate, which the interval limiter cannot express. But keyless, that quota
belongs to a shared anonymous project — verified live, where the quota was
already exhausted by other callers before Bard sent anything. A local counter
would therefore have been confidently wrong in both directions: refusing while
quota remained, and asking after it was gone.

The service's own refusal is the only reliable signal, so that is what Bard
reads. Only the body distinguishes the two refusals, so the body is read on 429
and 403 rather than inferring from the status.

Not retrying is the point. A 5000-book backfill meeting a daily quota halfway
would otherwise send 3 attempts for every remaining book — about seven thousand
requests to a service that has already said no.

**Alternatives Considered:** A persisted `provider_budget` table counting
requests per day, which was the original plan until the live 429 showed the
count could not be ours to keep. Treating a daily quota as an ordinary rate
limit, which is what the retry loop did before and is the hammering above.

**Impact:** No migration. The breaker is per provider and in memory, so a
restart re-probes once and is re-refused once, which is acceptable at one
wasted request a day. `TestADailyQuotaRefusalStopsAskingForTheDay` and
`TestAPlainRateLimitIsStillRetried` are the pair that pin the distinction.

---

## 2026-11-16: Audnexus is ASIN-only, so the MP4 parser learned to read one

**Decision:** Audnexus is registered first, serves audiobooks only, and
searches nothing: it answers `/books/{asin}` or it does not answer. `ParseM4B`
now reads the iTunes free-form `----` atom to extract the Audible ASIN.

**Rationale:** Audnexus exposes no title search — confirmed by probing it,
where every search-shaped route is a 404. So it can only ever be keyed on an
ASIN, and before this nothing in Bard produced one from an audio file: the plan
assumed the identifier would be there. Without the parser change the provider
could never have fired, and the narrator facet would have stayed empty, which
is the gap Audnexus exists to close.

It leads the registry because it is the only source of narration and the only
one that knows an audiobook's own edition. Since it declines every ebook, Open
Library still leads for everything else.

go-mp4 models neither the free-form atom nor its mean/name/data children, so
the children are walked by hand — the same approach `chpl` already needed.

**Alternatives Considered:** Deriving an ASIN from an ISBN through another
provider, which is a guess dressed as an identifier. Leaving Audnexus keyed on
nothing and skipping it, which is most of this phase's value for audiobooks.

**Impact:** **An audiobook from a DRM-free store carries no ASIN at all** —
both sample M4Bs are from Libro.fm and have none — so Audnexus cannot enrich
them and their narrators stay empty. Documented rather than worked around.
The free-form parser is pinned by a fixture matching the real atom layout but
is **unverified against a genuine Audible file**, since the sample library has
none. Audnexus also answers `REGION_UNAVAILABLE` with a 200 and an error
object, which is read as "no match" rather than as a fault.

---

## 2026-11-16: The long description outranks the comment

**Decision:** `ParseM4B` prefers the `ldes` or `desc` atom over the `©cmt`
comment, reversing what Phase 4 did. `mp4TextAtom` reads those atoms by hand.

**Rationale:** A Phase 4 bug found while wiring providers: `applyAudioTags` set
the description from the comment *after* the atom had been consulted, so the
comment always won. On the real library that makes Red Rising's description
"Pierce Brown Purchased from Libro.fm." — a store's stamp standing as the
book's blurb.

The atom read was also dead code regardless: it went through go-mp4's typed
extraction, which cannot descend into an atom it does not model, and `ldes` is
not one it models. So the long description had never been read from any file.

**Alternatives Considered:** Filtering junk-looking comments, which is a
heuristic guessing at a store's wording. Dropping the comment entirely, which
loses the only description an MP3 set ever has.

**Impact:** The comment is still the fallback, so a file with nothing else
keeps it. A provider description outranks both, so an enriched library sees
this only where no provider matched. Rescanning is what applies the fix; there
is no migration.

---

## 2026-11-16: A provider fills only what the file cannot measure

**Decision:** The plan's precedence chain — sidecar → provider → embedded →
path — is not applied uniformly. A provider outranks the file on bibliographic
fields (title, author, description, publisher, subjects, ISBN) and is never
consulted for what the file itself measures: duration, chapter marks, page
count and cover art. `provider.Query` carries `WantCover`, `WantPageCount` and
`WantChapters`, set from what the local metadata already has, and a provider
offers those fields only when asked.

**Rationale:** Taken literally, the chain lets an estimate overwrite a
measurement. Open Library's `number_of_pages_median` is the median across every
edition of a work, so enabling a provider would have replaced a PDF's exact
page count with a guess. The same for chapters, where the plan already says the
opposite — "prefer Audnexus over local `chpl` data only when local chapters are
absent" — which a uniform chain cannot express.

Making it a request rather than a post-merge filter also saves the work: a
library of EPUBs with embedded art never downloads 5000 covers it already has,
and the flag is off for exactly the books where it would be waste.

**Alternatives Considered:** Reordering the whole chain to put embedded above
provider, which gives the file's sloppy or absent title priority over a good
one and loses the point of enrichment. Filtering the provider's result after
the merge, which spends the request and the download first.

**Impact:** `metadata.Resolve` is unchanged and still has one rule. The scanner
resolves local metadata first and passes it to `provider.QueryFor`, so the
chain reads as locks → provider → everything local.
`TestOpenLibraryLeavesWhatTheFileAlreadyMeasured` pins it.

---

## 2026-11-16: The lookup is keyed on everything known locally, not the tags

**Decision:** The scanner resolves the sidecar, the file's tags and the path
convention into one local metadata record, asks the provider with that, and
then resolves again with the provider's answer inserted below the locks.

**Rationale:** Found by test: keying the query on the embedded tags alone meant
an untagged file was never looked up, because the query had no title. That is
the common case rather than an edge — an M4B with no tags, a PDF with no title,
or any file whose title Bard knows only from `Author/Series/## - Title/`. The
path convention exists precisely to name those files, so excluding it from the
query excluded the books that need a provider most.

Resolving twice costs nothing: `Resolve` only fills unknown fields, so merging
an already-merged record is equivalent to the long chain, and the second call
reads as the two things that actually rank — the locks, then the provider,
then everything local.

**Alternatives Considered:** Keying on the tags and falling back to the path
only when they are empty, which is the same thing with a branch. Looking up
after resolution and re-resolving, which is where this started.

**Impact:** `provider.QueryFor` takes local metadata rather than parser output.
`TestAProviderFillsWhatTheFileCouldNotSay` runs against a fixture whose EPUB
does not parse at all, which is the condition that caught this.

---

## 2026-11-16: Fetch takes the candidate, and the cache sits in the HTTP layer

**Decision:** The plan's `Fetch(ctx, id string)` became
`Fetch(ctx, Query, Candidate)`, with the candidate carrying an unexported
payload holding what the search already returned. Caching is keyed on the
request URL and status rather than on a provider identifier, and lives in the
client every provider goes out through.

**Rationale:** Open Library's search response carries the title, authors, year,
publisher, page count and cover id; only the description needs the work record.
A bare id would make `Fetch` re-request what the search just returned. Fix
Match passes the candidate a person picked, which it already holds, so nothing
is lost. The payload is unexported and the providers share the package, so it
never leaves.

Caching the HTTP exchange rather than the parsed result means both search and
fetch are cached without either provider remembering to, a 404 is cached as the
answer it is, and a later parser improvement re-reads the stored body instead
of re-asking.

**Alternatives Considered:** Caching normalized `Metadata`, which needs a
schema version and loses the raw body a parser fix would want. Re-requesting
inside `Fetch` and relying on the cache to make it free, which works and is
one more request on the first pass of every book.

**Impact:** `provider_cache` is `(provider, url) → (status, body)` with no
expiry. Artwork bypasses it, since the cover cache already
content-addresses what was kept.

---

## 2026-11-16: A contradicted author refuses the match; a surname confirms it

**Decision:** Scoring is identifier-first and then title-carried: an ISBN or
ASIN match scores 1, an exact normalized title 0.6, a subtitle-stripped match
0.5, a prefix 0.35. An agreeing author adds 0.4, an absent one adds nothing,
and a contradicting one halves the score, which puts even an exact title below
the 0.6 threshold. Agreement accepts one name being contained in the other.

**Rationale:** Normalization is `grouping`'s, so the rule that decides two
editions are one book is the rule that decides a provider found the right one.
A title alone has to be enough, because many files name no author at all; a
contradicted author has to be fatal, because a confidently wrong match takes a
person to notice and undo, while a missed one only leaves the book as the file
described it.

Containment rather than equality is not a loosening for its own sake: Bard's
own recommended layout yields an author folder that is frequently a surname,
and `Tolkien` must agree with `J. R. R. Tolkien` or a library laid out the way
the docs ask would match nothing.

**Alternatives Considered:** Fuzzy string distance, which turns a wrong answer
into a confident wrong answer — the same reason the Phase 7 handoff refuses it.
Requiring an identifier, which excludes every Gutenberg EPUB, none of which
carries an ISBN.

**Impact:** `Rank` keeps candidates below the threshold with their scores,
because Fix Match exists to show a person what the automatic match refused.
`Best` is the only caller that applies the threshold.

---

## 2026-11-16: Progress is keyed on (user, edition, device)

**Decision:** A progress row is per device. `(user, edition)` names the set of
positions to aggregate, not a row. `devices.id` is nulled rather than cascaded
when a device is revoked.

**Rationale:** The plan's progress contract says "keyed on (user, edition),
never (user, book)" and its `Position` struct carries no device, but the same
phase requires that "two offline devices reporting different positions both
survive" and that the API "return every known position". One row cannot
diverge from itself, so the per-device key is what the stated behaviour needs;
the plan's intent — never collapse the ebook and the audio position — is
untouched, since the edition is still part of the key.

Nulling the device on a revoke keeps the reading. Cascading would mean
revoking a lost phone silently rolls its owner's progress back to whatever
another device last reported, which is the data loss this phase exists to
prevent. The unique index is partial (`WHERE device_id IS NOT NULL`) because
SQLite treats NULLs as distinct, so a plain one would let a revoked device
accumulate rows.

**Alternatives Considered:** One row per (user, edition) plus a separate
per-device table, which is the same storage with a canonical row that resolves
divergence silently — the thing the plan forbids. Keeping a single row and
taking the furthest position, which loses the ability to say "your iPad is
further ahead".

**Impact:** `client_seq` is compared only within one device, since the counters
are each client's own and not comparable across them —
`TestTwoDevicesDivergingBothSurvive` asserts a lower sequence from a second
device is not read as stale. `space.Diverged` reports whether the spread
exceeds 1%, so a client is not interrupted over half a point.

---

## 2026-11-16: Reading state is derived, and its SQL interpolates the user id

**Decision:** Only `finished` and `abandoned` are stored, as `user_books.marked`;
unread and reading derive from the furthest progress across a book's editions.
`model.DeriveReadingState` and `db.ReadingStateSQL` are twins held together by
`TestReadingStateMatchesSQL`. The SQL writes the user id into the expression
rather than binding it.

**Rationale:** Deriving is what keeps "continue reading" correct with nobody
maintaining it, and it has to exist in SQL because the facet filters and counts
on it — a Go-side annotation would break pagination the same way a post-filter
does. A hand-set mark outranks progress because abandoning a book you are 60%
through must survive the progress that would otherwise call it "reading".

The interpolation is deliberate and narrow: the expression needs the id three
times, and `db.Query` concatenates arguments positionally, so numbered
parameters in one clause would misalign every other clause in the query. The
value is an `int64` taken from the authenticated session, never a string from a
request.

**Alternatives Considered:** Storing the state and updating it on every
progress write, which is a second source of truth to drift. Binding the id
three times, which works only while nothing else in the query takes arguments —
a trap for the next person to add a facet.

**Impact:** `FinishedPercent` is 0.95, because an audiobook ends in credits and
an ebook in back matter. `finished_at` is stamped the first time a book crosses
it and is cleared only when the mark is removed *and* progress no longer
supports it, so un-finishing a book you did read keeps its history entry.

---

## 2026-11-16: The shelf and reading state are facets, not endpoints

**Decision:** The saved shelf is `GET /api/v1/books?saved=true`, and reading
state and personal rating are facets alongside it. `ApplyBookSort`,
`ApplyBookFilters` and `FacetValues` all take the reader's id.

**Rationale:** A `/shelf` endpoint would need its own scope, filters, sorts and
pagination, and would be a second read path to keep leak-free. As facets they
inherit all of it, and `?saved=true&format=m4b&sort=saved` composes for free.
This is the payoff of Decision 8's single query builder reaching user state.

**Alternatives Considered:** Dedicated endpoints per list, which is what most
of these APIs do and what makes their filtering inconsistent.

**Impact:** The facet registry's two halves now take a user id; the facets that
do not care take `_`. `bookSorts` became a function of the user because
ordering a shelf by when it was shelved needs one. History stays its own
endpoint, since it is ordered by a date that is not a book column and returns a
different shape.

---

## 2026-11-16: Annotations are keyed on a client-supplied uuid

**Decision:** The client generates each annotation's uuid; the server upserts
on `(user_id, uuid)` and assigns a per-user sequence number on every write,
including the soft delete.

**Rationale:** An annotation is made offline, so it needs an identity before it
ever reaches the server, and a retried upload after a dropped connection has to
update rather than make a second highlight. Two accounts may hold the same uuid
without colliding, since neither generated it for the other.

The sequence is per user so the lookup rides `annotations_cursor_idx`, and
writers serialize on an immediate transaction, so no two can claim one number.
A delete advances it like any other change, which is what carries the tombstone
to a device that synced before the delete — a hard delete would leave nothing
to carry and the stale device would upload the highlight again.

**Alternatives Considered:** Server-assigned ids with a client-side mapping
table, which puts the reconciliation in every client. A global sequence, which
needs a full-table MAX on every write.

**Impact:** The cursor advances only as far as what was actually handed over,
so a truncated page resumes rather than skipping —
`TestATruncatedChangesPageIsResumable`. Re-saving a deleted uuid revives it,
deliberately: the client is the authority on whether its own annotation exists.

---

## 2026-11-16: The handoff keeps a chapter number when nothing else survives

**Decision:** Chapter titles are normalised by stripping leading decoration
("Chapter", "Part", a leading number and punctuation), but only when something
remains. A title that is only its number keeps the number. A title matching
more than one target chapter falls back to percentage.

**Rationale:** Found while testing: `normaliseTitle("1984")` returned empty,
because the stripper could not tell a number that decorates a title from a
number that *is* one — the same ambiguity the path parser hit in Phase 4.
Falling back to the whole title when stripping leaves nothing fixes that, and
has the side benefit that two lists which both number their chapters can still
align on the number.

Ambiguity falling back is the important half. Landing confidently in the wrong
chapter is worse than admitting the lists do not line up, which is why a title
found twice is treated as no match at all.

**Alternatives Considered:** Fuzzy string distance, which turns a wrong answer
into a confident wrong answer. Aligning purely by index whenever counts differ,
which misfiles every chapter after an audiobook's prologue track.

**Impact:** Verified against the real library: Red Rising's M4B and its 47-part
MP3 set carry chapter starts agreeing to within a second, and a handoff from
chapter 20 at 50% lands at 19722.6s in the other edition, inside the same
chapter. `media.MapPosition` is pure, so the alignment is tested without a
database.

---

## 2026-11-16: The sync manifest names candidates, with a reason each

**Decision:** The manifest lists editions with a reason — `reading`, `saved`,
or `next_in_series` — strongest first, each edition appearing once. A book
being read contributes only the edition actually in progress; a shelved book
contributes all of them.

**Rationale:** Per Decision 15 the client decides what to cache, so the server's
job is to name candidates with their sizes and hashes and stop there. The reason
is what lets a client prioritise without guessing.

Listing only the edition in progress matters because reading the ebook says
nothing about wanting 600MB of narration. A shelved book is the opposite: no
format has been chosen yet, so the sizes in the manifest are what the choice
gets made with.

**Alternatives Considered:** Every edition of everything, which puts a
gigabyte of candidates in front of a phone. A server-side priority score,
which is the server guessing at storage it cannot see.

**Impact:** "Next in series" applies the scope to the candidate as well as the
result, or a hidden book would be chosen as next and then filtered out,
silently stopping the series there. No expiry, no caps, no eviction, as
Decision 15 requires.

---

## 2026-11-16: The sync manifest moves to Phase 7

**Decision:** Phase 6 ships every other deliverable and leaves
`GET /api/v1/sync` to Phase 7.2. The reading-state and personal-rating facets
are left out of the facet set for the same reason.

**Rationale:** The manifest is defined entirely by state Phase 7 creates —
active reads, next unread in a series the user is reading, and the saved shelf.
None of those tables exist yet, so the endpoint could only have returned an
empty list. Phase 7's own file already says it "feeds the reading-state facet
from Phase 6", so the dependency runs the other way round in two places.

**Alternatives Considered:** Shipping the endpoint with its final response shape
and no content, which locks the contract early but passes its validation line on
shape rather than substance. Pulling a progress table and a saved shelf forward
into Phase 6, which spends Phase 7's design decisions — divergence surfacing,
derived state — without their tests.

**Impact:** Phase 6's validation list loses two lines, noted in the phase file.
Adding a facet is one entry in `db.bookFacets`, so Phase 7 adds reading state
without touching a handler.

---

## 2026-11-16: The search index was never populated, and now updates inside the scan

**Decision:** `db.ReindexBook` and `db.RemoveBookFromIndex` became package
functions taking a `db.Conn`, and the scanner calls them inside the same
transaction as the metadata that changed. `grouping.ReapEditionlessBooks`
returns the ids it deleted, and `Merge` and `Split` return an `Outcome` naming
the books to reindex.

**Rationale:** Phase 3 built `books_fts` and its maintenance function; nothing
outside tests ever called it, so `/api/v1/search` found nothing on a real
library while the Phase 3 tests passed by reindexing by hand. Found by running
the server against the sample set.

Updating the index in the transaction that caused the change means a scan
killed halfway cannot leave a book indexed under its old title. Removal matters
independently: SQLite reuses rowids, so a stale row would hand its text to
whichever book is created next.

**Alternatives Considered:** A post-scan pass reindexing every book, which
writes 5000 rows on a clean rescan and breaks the "a clean rescan writes
nothing" property. An SQLite trigger, which cannot see authors and narrators
because they arrive through joins.

**Impact:** Only touched books are rewritten, so a clean rescan still writes
nothing. `TestScanPopulatesTheSearchIndex` and
`TestReapedBookLeavesTheSearchIndex` are the regression tests. A reindex
failure after a manual merge is logged and leaves a stale row rather than
undoing the merge, because the merge has already committed.

---

## 2026-11-16: The file ETag is id, size and mtime, not the content hash

**Decision:** `media.FileETag` uses `files.content_hash` when it is set and
falls back to a strong tag built from the file id, its size and its mtime.

**Rationale:** The Phase 6 notes ask for a strong ETag from the content hash,
but `files.content_hash` has never been written: hashing every file means
reading every byte, which is exactly what the scanner avoids to keep a cold
scan of 5000 titles affordable. Size plus mtime is sound here because the
scanner restats every file on every pass, so changed bytes change the tag.
Preferring the hash when present means Phase 10's uploads, or any later hashing
pass, improve the tag for free.

**Alternatives Considered:** Hashing lazily on first full download, which reads
a gigabyte to answer a range request. Hashing during the scan, which is the
cost Phase 4 declined.

**Impact:** `files.modified_at` is stored as RFC3339Nano by the scanner while
the defaulted columns use `datetime('now')`; `db.parseStamp` accepts both.
Parsing it with the wrong layout silently yielded the zero time and cost the
tag its mtime component, so a file edited in place at the same size kept its
ETag. Pinned by `TestFileETagCarriesTheModifiedTime`.

---

## 2026-11-16: Facets filter with EXISTS, and counts inherit the scope

**Decision:** Every facet contributes an `EXISTS` subquery rather than a join,
values within one facet are OR'd and separate facets AND'd, and facet counts
run over the same scoped and filtered book set as the listing they accompany.
An unrecognised query parameter is a 400 rather than an ignored filter.

**Rationale:** A book with two editions matching one filter would appear twice
through a join, which inflates `total`, breaks pagination and double-counts the
facet — the same class of bug as post-filtering, and the reason Decision 4 puts
visibility in the query. Counting over the whole library instead of the scoped
set would let a number report a book the caller cannot see, which is a leak even
though the record is absent.

Rejecting an unknown parameter matters because a filter that silently does not
apply returns more than the caller asked for; `?formats=epub` should fail rather
than quietly list everything.

**Alternatives Considered:** Joining and de-duplicating with `DISTINCT`, which
breaks `LIMIT` arithmetic. Counting each facet with its own filter removed, the
usual multi-select behaviour, which contradicts the validation line asking
counts to match what the same filters return.

**Impact:** `db.bookFacets` is the one place a dimension is defined, with a
`where` and a `list` per facet. `TestFacetCountsMatchTheRecordsTheSameFiltersReturn`
walks every facet value and asserts the count equals the listing total.

---

## 2026-11-16: One length facet spans both kinds at 150 seconds a page

**Decision:** `db.LengthSeconds` is an audiobook's duration, or a page count
times 150 seconds when there is no audio edition. One `length` facet and one
`length` sort read it.

**Rationale:** The plan asks for a "length bucket" without saying how a 400-page
ebook and a 16-hour audiobook share one. Converting gives a single facet whose
buckets mean the same thing either way, and the sample library's ebook and
audiobook pairs work out to 115–170 seconds a page, so 150 is the middle rather
than a guess. Page counts only ever come from PDFs — the EPUB parser produces
none — so this is a fallback rather than the usual answer.

**Alternatives Considered:** Separate `duration` and `pages` facets, which is
unambiguous but makes "under five hours" two different questions. Bucketing only
audiobooks, which leaves every ebook out of a facet the plan asks to span the
library.

**Impact:** A book with neither a duration nor a page count falls outside every
bucket and is excluded from a length-filtered listing, which is correct: an
unknown length cannot satisfy "under five hours". The three EPUBs in the sample
set are in that position today.

---

## 2026-11-16: BookScope ships without a BookPolicy

**Decision:** `internal/policy/book.go` holds `BookScope` only. The
`View`/`Read`/`Save` split arrives in Phase 9 with the age ladder it exists to
express.

**Rationale:** Phase 6 has no per-book decision to make — anything the scope
returns is readable by anyone who can see it. Three methods that all `allow()`
would be scaffolding whose fields Phase 9 then has to change, and the
leak-proofing the plan wants lives in the scope regardless: book detail, file
content and cover all resolve through a scoped query, so Phase 9's
direct-by-id leak tests are covered by changing one function.

**Alternatives Considered:** Writing the three always-allow methods now so the
handlers already call through them. Rejected because the handlers would be
calling a question with one possible answer, and the scope already gives Phase 9
a single place to add the ladder.

**Impact:** Phase 9 adds `BookPolicy` rather than extending it, and adds its age
conditions inside `BookScope.Apply`. The scope's teeth today are
`sources.enabled`, which also drops editionless books.

---

## 2026-11-16: Cover variants stay pre-generated at scan time

**Decision:** Covers keep the Phase 4.3 behaviour of writing every configured
variant when the cover is first stored. The read path picks the smallest cached
width that covers the request and falls back to the full-size copy.

**Rationale:** The Phase 6 notes ask for variants generated on first request
rather than pre-generated for 5000 books, but 4.3 already pre-generates them and
the cost is two small JPEGs per distinct cover, deduplicated by content hash.
Reversing it would add a write path to the request handler for no measurable
saving.

The fallback is what makes this safe: a cover smaller than a configured width
has no file at that width, and adding a width later leaves existing covers
without one. `BestVariant` consults what is actually on disk rather than
assuming `Widths`.

**Alternatives Considered:** Generating on demand and caching, which puts image
decoding in the request path and needs its own concurrency story.

**Impact:** `Cache.Variants` is the authority on what exists. A 400px cover in
the sample set has only the 180 variant, so `?width=480` correctly serves the
400px original.

---

## 2026-11-13: A differing ISBN does not prevent a group

**Decision:** A matching ISBN or ASIN forces a group. A differing one does
nothing. This contradicts a Phase 5 validation line that asked for the
opposite.

**Rationale:** An ISBN identifies a manifestation, not a work. An ebook and its
audiobook have different ISBNs, two narrations have different ISBNs, and two
printings of one novel have different ISBNs — all of which are the same book
with several editions under Decision 2. Implementing the veto would have broken
the two validation lines directly above it in the same list, which ask for
exactly those groupings.

Precision comes from title plus author instead. An edition with no known author
offers no title key at all, so two different books called "Collected Poems"
cannot merge.

**Alternatives Considered:** Vetoing only within one kind, which still breaks
two narrations grouping. Vetoing only weak matches, which keeps the letter of
the requirement while making the behaviour harder to predict than simply not
having it.

**Impact:** Two distinct books sharing a title are kept apart by their authors,
covered by a test. If a real library ever produces a bad merge that an ISBN
would have caught, the manual split is permanent and one click.

---

## 2026-11-13: Transactions begin immediate

**Decision:** The SQLite DSN sets `_txlock=immediate`, so every transaction
takes the write lock up front.

**Rationale:** Phase 5 made the scanner read before it writes — it looks for a
book to join, then inserts. A deferred transaction that reads first cannot
upgrade to a writer once another connection has written, and WAL reports that
as `SQLITE_BUSY` immediately rather than waiting out `busy_timeout`, because
waiting cannot help. The scan job failed with "database is locked" roughly one
run in ten under `-race`, and would have failed in production as soon as Phase
7 added progress writes alongside a scan.

**Alternatives Considered:** Retrying on busy, which is more code and leaves
the window open elsewhere. Moving the reads outside the transaction, which
loses the consistency that stops two sources creating the same book at once.

**Impact:** Writers serialize, which at this scale is what we want anyway.
Caught only because the failure was intermittent under the race detector and
worth chasing rather than re-running.

---

## 2026-11-13: The sidecar records decisions, and only locked fields outrank the file

**Decision:** `bard.json` holds the full resolved metadata for every edition in
its directory, plus a `locked` list naming the fields a person corrected by
hand. On a scan it is consulted twice: the locked subset at the highest
precedence, and the whole record at the lowest. The resolution order is
locked sidecar → embedded → path convention → remembered sidecar.

**Rationale:** A sidecar that simply outranked the embedded parsers would
freeze the library. Once written, a fix to the EPUB or MP4 parser could never
reach a book that had been scanned even once, because the stale value would keep
winning. Consulting it twice gives all three properties at the same time: a
hand correction survives anything, a parser improvement reaches every unlocked
field, and a value nothing can re-derive — a provider's ASIN, in Phase 8 — is
still never forgotten.

Writing the derivable fields too, rather than corrections alone, is what makes
the file worth opening: it is the thing a person reads and edits, and it is what
rebuilds an index on a machine with no network.

Chapters and cover art are deliberately excluded. Both are re-derived on every
scan from the file itself, and neither belongs in something meant to be
hand-edited.

**Alternatives Considered:** Storing only locked fields, which keeps precedence
trivially correct but leaves the file nearly empty and useless for a rebuild.
Storing everything at the highest precedence, which is the freezing problem
above.

**Impact:** `metadata.Resolve` is called with four sources and the order lives
at that one call site. Unlocking a field in the admin UI must also drop it from
`locked` in the sidecar, or the old value keeps winning. Renaming any entry in
`model.fieldNames` silently unlocks that field in every sidecar already on disk.

---

## 2026-11-13: One keyed sidecar per directory, not one per edition

**Decision:** A single `bard.json` per directory, with an `editions` map keyed
by each edition's file base name, or `"."` for an edition that is the directory
itself. The file is written once per directory at the end of a scan, and only
when its bytes would actually change.

**Rationale:** The plan assumed one sidecar per *edition directory*, which holds
only under the `Author/Series/## - Title/` convention. The walker deliberately
supports flat layouts too — a shelf of M4Bs, an EPUB and a PDF side by side —
and a single unkeyed file cannot describe those. Keying it costs one level of
nesting and covers every layout the scanner accepts.

Comparing before writing is what keeps a rescan from touching the media tree at
all; otherwise every scan would restamp every `bard.json`, which is both noisy
and a lie about what changed.

**Alternatives Considered:** `<basename>.bard.json` beside each file, which
scatters one small file per book through the library and reads worse than one
file describing a directory.

**Impact:** Entries for editions that have vanished are pruned on the next
scan of their directory. A directory that loses *all* its books keeps its stale
sidecar, since nothing visits it to clean up.

---

## 2026-11-13: A leading number is a series position only inside a series folder

**Decision:** The path parser reads `## - Title` as an ordinal only when the
candidate sits in a series directory, meaning there are at least two
directories above the title.

**Rationale:** Found while testing: `Marquez/100 Years of Solitude.epub` parsed
as position 100 of an unnamed series with the title "Years of Solitude". Making
the ordinal conditional on a series folder keeps the documented convention
working while leaving numerically titled books alone. Capping a bare ordinal at
three digits already protected `1984.epub` and `2001 A Space Odyssey.epub`,
but not a title whose number is followed by more words.

**Impact:** `Author/Series/01 - Title` parses fully; `Author/01 - Title` keeps
the number in its title. Since the path is the lowest-precedence source, a wrong
guess is overridden by anything the file itself says.

---

## 2026-11-12: MP3 duration comes from the VBR header, not from summing frames

**Decision:** Read the encoder's Xing/Info or VBRI frame count where there is
one, and otherwise estimate from the first frame's bitrate. Never walk every
frame.

**Rationale:** Summing frame headers is exact but reads the whole file, and a
cold scan of 5000 titles cannot afford that — the header read is 8KB per file.
The header's position has to be computed from the side-info size rather than
searched for: an ID3 text field containing the word "Info" matches a naive scan,
which the Doctorow sample demonstrates ("**Info**rmation Doesn't Want to Be
Free").

**Alternatives Considered:** `ffprobe` per file, which the plan reserves for
chapter extraction and will not make a hard dependency. Parsing the LAME
gapless delay and padding, which would close the last 40ms but adds a second
binary layout to get wrong.

**Impact:** Exact where a Xing count exists and the encoder wrote no gapless
info; otherwise ~0.04s long per file, because the frame count includes the
header frame and the LAME delay and padding are not subtracted. Measured
against the same book in both formats: the MP3 set reports 58342.5s and the M4B
58340.7s, a 1.7s drift over 16.2 hours, well inside the ±1–2 minute tolerance
Decision 12 accepts for cross-edition handoff.

---

## 2026-11-12: Narrator comes from the composer tag, not the album artist

**Decision:** For both M4B and MP3, the artist tag is the author and the
composer tag is the narrator. Album artist is only a fallback author.

**Rationale:** This is the iTunes audiobook convention and what other readers
expect. Album artist is the ambiguous field — tools disagree over whether it
names the author or the narrator, so trusting it would file some narrators as
authors. None of the sample audiobooks set composer at all, so narrators stay
empty until the Phase 8 providers fill them, which is the right outcome: an
empty field is recoverable, a wrong one looks correct and never gets fixed.

**Alternatives Considered:** Scraping the comment field, which in one sample
reads "Read By Wil Wheaton". Rejected as fitting the parser to one publisher's
habit rather than to a standard.

**Impact:** Narrator is usually unknown after a scan of audio. Phase 8's
Audnexus provider is what populates it.

---

## 2026-11-12: Covers are content-addressed, and only JPEG variants are kept

**Decision:** The cover cache keys files on the SHA-256 of the source bytes and
stores a full-size copy plus a variant per configured width, all re-encoded to
JPEG. The original bytes are not kept, and no variant is ever upscaled.

**Rationale:** Content addressing makes the dedup fall out for free, which the
sample library proves worthwhile: Red Rising ships as both an M4B and 47 MP3s
carrying identical art, and both editions point at one cached cover. Keeping
only JPEG means paths are predictable without recording each cover's source
format, and the original is safe to drop because disk is authoritative — a
rescan re-extracts it.

**Alternatives Considered:** Keying on the edition id, which would store the
same art twice and need invalidating when it changes. Keeping the original
alongside the variants, which costs a format column for no reader benefit.

**Impact:** `editions.cover_hash` in migration 0006 is the only index; the file
layout is otherwise self-describing. Covers orphaned by a metadata change are
not collected yet, since a shared cover cannot be deleted without refcounting —
a few KB each, left for Phase 12 maintenance.

---

## 2026-11-12: MP3s group on the set, M4Bs never group

**Decision:** A directory's MP3s become one edition when a strict majority of
their names carry a track prefix, or when they share a six-character prefix;
otherwise each is its own edition. Multiple M4Bs in one directory are always
separate editions. Subdirectories merge into one edition only when every one of
them is named like a disc (`Disc 1`, `CD2`, `Part 3`) and holds nothing but
MP3s.

**Rationale:** A chapter-per-file audiobook and a drop folder of single-file
audiobooks are identical file by file, so the signal has to come from the set.
The two formats need opposite defaults: a flat `Audiobooks/*.m4b` shelf is a
common layout and merging it would be catastrophic, while a flat folder of MP3s
is usually one book.

The disc guard is deliberately narrow because the rival reading of the same
shape — a folder of per-book subdirectories — is commoner, and wrongly merging
a shelf is far worse than splitting a rip, which a Phase 5 merge fixes once.

A strict majority rather than unanimity on track prefixes means one stray
`Bonus Interview.mp3` cannot shatter a thirty-part audiobook into thirty
editions, which would need thirty manual merges to undo.

**Alternatives Considered:** Requiring every name to be numbered (the stray-file
shatter). Trusting the existing grouping in the index over what disk says
(contradicts disk being authoritative). ID3 track numbers, which are a better
signal but belong to 4.3.

**Impact:** Two accepted misfilings, both fixable by Phase 5's split/merge: a
drop folder whose names happen to share six characters merges, and bare numeric
titles are separated from track numbers only by capping a bare track number at
three digits, so `1984.mp3` is a title and `001.mp3` is a part. 4.3 should
re-order parts from ID3 once it can read them.

---

## 2026-11-12: Shutdown requeues a job for free; a crash charges it an attempt

**Decision:** A job interrupted by shutdown goes back to pending with its
attempt count decremented. A job found in `running` at startup goes back to
pending with the attempt it already spent left standing.

**Rationale:** The two cases look identical in the table but are not. A SIGTERM
during a scan is not the job's fault and must cost nothing, or a few restarts
would exhaust `max_attempts` on healthy work. A job that killed the process is
the opposite: letting it retry forever is a boot loop, so the spent attempt is
what eventually parks it.

Bookkeeping therefore runs on a context detached from the cancelled one, with a
short timeout — otherwise the write recording a finished job is itself cancelled
and the job runs again on the next boot.

**Alternatives Considered:** A heartbeat column to tell a live worker from a
dead one. Unnecessary while the pool is in-process, since at startup no running
row can have a worker.

**Impact:** `Release` and `Reclaim` are separate operations with deliberately
different arithmetic. Pinned by `TestShutdownRequeuesWithoutChargingAnAttempt`
and `TestReclaimRequeuesRunningJobs`.

---

## 2026-11-12: `bard source add` lands in Phase 4, ahead of the admin UI

**Decision:** Phase 4.2 adds a minimal `bard source add|list` CLI, though the
plan assigns source management to the Phase 11 admin UI.

**Rationale:** Sources live in the database, not the config file, so without
this verb `bard scan` has nothing to scan and Phase 4 cannot be exercised by
hand at all — the alternative was telling the operator to write an `INSERT` by
hand against `sources`.

**Alternatives Considered:** Declaring sources in `bard.yml`. Rejected: it
contradicts the plan's open assumption and would become a second source of
truth the admin UI then has to reconcile.

**Impact:** `add` warns when `default_min_age` is unset, since that fails closed
for every restricted account. Phase 11 builds its UI on the same queries in
`internal/scan/sources.go`, which is also where a proper store should land.

---

## 2026-11-12: Effective min age inherits from editions, not "the book's source"

**Decision:** A book's effective minimum age is its own `min_age` if set,
otherwise the most restrictive `default_min_age` across the sources its
editions live in. `NULL` when any of those sources is undecided, or when the
book has no editions, and `NULL` denies.

**Rationale:** The v1 phase 3 schema was planned around a book inheriting from
"its source", but a book has no single source — `source_id` lives on editions,
so a book can have its EPUB in one source and its M4B in another. Taking the
most restrictive fails closed.

The SQL twin needs `COUNT(*) = COUNT(s.default_min_age)` rather than a bare
`COALESCE(b.min_age, MAX(s.default_min_age))`: SQLite's `MAX` ignores NULLs, so
the shorter form returns a permissive age for a book whose editions include an
unrated source — failing open in the one case that must fail closed.

**Alternatives Considered:** Least restrictive (fails open). Per-edition gating
(a child could read the ebook but not hear the audiobook, which is confusing).
Materialising the value at scan time (would need recomputation whenever a
source default changes in the admin UI).

**Impact:** `model.EffectiveMinAge` and `db.EffectiveMinAgeSQL` must agree;
`TestEffectiveMinAgeMatchesSQL` runs both over the same fixtures. Phase 9
filters in SQL and authorizes in Go, so divergence would let one allow what the
other denies.

---

## 2026-11-12: No policy registry in the Go port

**Decision:** Policies are constructed by typed functions
(`NewDevicePolicy(actor, device)`). The explicit registry from
`rails/policies/advanced` is omitted.

**Rationale:** The Rails registry exists to avoid `.constantize` resolving an
attacker-influenced string to a class (CWE-470). Go has no dynamic class
lookup here, so there is no hazard to guard and a registry would add
indirection without removing one. Action inference from a controller action
name likewise has no Go equivalent, so callers name the action.

**Alternatives Considered:** Building a registry anyway for symmetry with the
pattern. Rejected as indirection with no safety benefit.

**Impact:** Documented in the `internal/policy` package comment. The rest of the
pattern is kept verbatim: structured `Result` over bool, `allow`/`deny` helpers
only, unexported boolean role helpers, single-point message resolution.

---

## 2026-11-12: Reason coverage is a test, not a convention

**Decision:** `allReasons` enumerates every `policy.Reason`, and a test asserts
each resolves to a distinct non-default message.

**Rationale:** The Rails pattern notes that a `deny(:foo)` with no matching
`when :foo` silently degrades to "Not authorized", defeating the point of
carrying reasons. In Go that can be a build failure instead of a review
comment. Verified by adding an uncovered reason and watching the test fail.

**Impact:** Adding a `Reason` without a message breaks CI.

---

## 2026-11-12: Managed profiles derive from owner_id; PIN gates the switch back

**Decision:** A device row carries `owner_id` (who logged in) and
`active_user_id` (the selected profile). The switchable set is the owner plus
managed users owned by it. Switching into a profile that has a PIN requires it.

**Rationale:** A per-device list of assumable profiles was planned; deriving
from `owner_id` means one less table and no way for the list to drift from
reality. Keeping the token stable while `active_user_id` changes
is how a profile picker actually works on a shared device.

Because the switchable set is shared by every profile on the device, a PIN on
the owner's profile is the only thing stopping a child from switching into it —
and therefore from bypassing every age restriction in phase 9.

**Alternatives Considered:** Issuing a new token per profile switch (then
switching back needs the original token). An explicit per-device allowlist
(more state, same outcome).

**Impact:** Phase 9's age gating is only as strong as the owner's PIN. The admin
UI should push for one.

---

## 2026-11-12: Pure-Go SQLite accepted at a write-throughput cost

**Decision:** `modernc.org/sqlite` over the cgo `mattn/go-sqlite3`.

**Rationale:** Roughly 2–3× slower on writes, but gives a genuinely static
binary, `GOOS=linux GOARCH=arm64 go build` with no C toolchain, and a trivial
release matrix. Writes happen during scans and progress updates, where tens of
seconds of extra wall-clock is unobservable.

**Alternatives Considered:** cgo for speed. Both are `database/sql` drivers, so
this is a one-line reversal if a benchmark ever justifies it.

**Impact:** `CGO_ENABLED=0` everywhere. Revisit only with a real benchmark.

---

## 2026-11-12: Default port 2273 kept despite an IANA assignment

**Decision:** Default to 2273, configurable.

**Rationale:** "BARD" on a phone keypad, in the registered range, clear of the
ephemeral range, and no collision with Plex, Jellyfin, Audiobookshelf, or the
*arr suite. IANA lists it as `mysql-im` (MySQL Instance Manager, 2004), a tool
removed from MySQL in 2010, so real collision risk is nil.

**Alternatives Considered:** Hunting for a wholly unassigned port. Not worth it
once the port is configurable, which is also what Plex does with 32400.

**Impact:** None beyond documenting it.

---
