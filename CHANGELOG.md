# Changelog

Notable changes, newest first. Versions follow [semantic versioning][semver];
until 1.0 the API may still change between minor versions.

[semver]: https://semver.org/

## Unreleased

First release candidate. Everything below is the initial feature set rather
than a list of changes against anything.

### The server

- Scans one or more **sources** (directory roots) into a single library,
  detecting EPUB, PDF, M4B and folder-of-MP3 audiobooks
- Extracts embedded metadata, cover art and audiobook chapter timings — EPUB
  OPF, MP4 atoms, ID3, and the Nero `chpl` chapter atom
- Groups editions into books on normalised title and author, assisted by ISBN
  and ASIN, with manual merge and split that a rescan cannot undo
- Writes `bard.json` sidecars beside the media, per source, so corrections
  outlive the database. Never renames or moves your files
- Series from metadata, admin-curated server collections, private user
  collections
- Faceted filtering across the library: genre, format, author, narrator,
  series, language, length, reading state
- A normalised genre taxonomy mapped from provider subject strings, keeping
  unmapped subjects as raw tags you can map yourself
- Full-text metadata search over SQLite FTS5
- HTTP range requests on all media. Nothing is ever transcoded

### Accounts and access

- Per-server local accounts with Argon2id passwords. No cloud identity
- Revocable per-device opaque tokens with a visible authorized-devices list
- Managed users — no password, optional PIN — for profile switching on a shared
  family device
- Age gating with per-source defaults, per-book overrides and per-user
  exceptions, failing closed
- Private per-user state: progress, reading state, ratings, saved shelf,
  history and annotations, not visible to anyone else including admins
- Cross-device progress sync that surfaces divergence rather than silently
  taking the last write
- Chapter-level and proportional cross-edition position handoff, so you can
  read and then listen

### Metadata enrichment

- Open Library, Google Books and Audnexus, **all off by default** — a lookup
  sends your titles to a third party, so it is your decision
- Per-provider rate limiting, circuit breaking and response caching
- Fix Match: ranked candidates including the ones the automatic matcher
  refused, a free-text search for something else entirely, and field locking
  so a correction sticks. Unlocking clears the sidecar too
- Suggested age ratings are only suggestions; nothing applies without an admin
  confirming it

### Operating it

- Embedded web admin UI: setup wizard, sources, accounts, devices, uploads,
  Fix Match, the rating queue and genre mapping. No Node, no vendored
  JavaScript, no build step beyond `go build`
- Resumable chunked upload from a phone or tablet, admin-granted, with
  per-chunk hashing
- `/diagnostics`, which reports the address rate limiting keys on for the
  request that loaded it and says whether `trusted_proxies` is right
- Nightly SQLite snapshots via `VACUUM INTO`, scheduled off the snapshot
  directory rather than stored state, so a failed night stays due
- `bard export` and `bard import` move an install between machines, carrying
  the database and cover cache and deliberately not the media
- Static amd64 and arm64 binaries, a systemd unit running as a dedicated
  non-root account, an install script, and a Docker image
- Optional built-in ACME (`tls: auto`) for the direct port-forward case, with
  `tls.acme_directory` for rehearsing against Let's Encrypt staging
- Deployment recipes for Cloudflare Tunnel, Tailscale and a reverse proxy, each
  stating its `trusted_proxies` value

### Known limitations

- **No first-party reading app.** iOS and Android clients are a separate
  project; this is the server
- Full-text search covers metadata, not the text inside books
- QuickTime text chapter tracks in M4B are not read, only the Nero `chpl`
  atom. `ffprobe` is the documented fallback and nothing calls it yet
- An author known by initials in one place and full names in another
  ("L. M. Montgomery" against "Lucy Maud Montgomery") scores too low to match
  automatically; Fix Match handles it by hand
- MP3 durations run fractionally long where an encoder wrote no gapless
  information — about 1.7s over sixteen hours, inside the handoff tolerance
- No mDNS or LAN discovery; clients are pointed at an address
