# Phase 12: Packaging & Release

> **Depends on:** Phase 11
> **Enables:** Public install; the mobile apps as separate projects
>
> See: [Full Plan](../plan.md)

## Goal

Make Bard installable from a public GitHub repo, and safe to expose to the internet.

## Key Deliverables

- Static binaries for linux amd64 and arm64, built by a shell script
- GitHub Actions: build, test, release on tag
- systemd unit and an install script
- Optional built-in ACME (`tls: auto`) for the port-forward case
- Three deployment recipes: Cloudflare Tunnel (recommended), reverse proxy, Tailscale
- A diagnostics page that proves `trusted_proxies` is right
- Nightly SQLite snapshot and `bard export`
- README, CHANGELOG, MIT license
- Security notes documenting the internet-exposure posture

**goreleaser is not used.** It is CI-only and so never reaches the binary, but
what Bard needs from it is two `go build` lines and a `sha256sum`. A hand-rolled
script is verifiable locally, which the YAML never is, and matches the rest of
the project's refusals: no router, no Node, a hand-written migration runner.

**Docker is secondary, not the primary path.** A static binary under systemd is
the install Bard is built for; the image exists for someone already running a
container host. Its recipe must state that every source path inside the
container has to be mounted at the same absolute path it has on the host, or
a scan records paths the host cannot serve.

**Cloudflare credentials stay out of Bard entirely.** `cloudflared` connects
outbound and forwards to localhost, so Bard never speaks to Cloudflare and must
never hold a token that is a capability over the user's DNS zone. No setup verb
wraps it. What *is* Bard's problem is whether `trusted_proxies` is right, which
is why the diagnostics page replaces the tunnel wrapper.

## Sub-phases

### 12.1: Snapshots, export and import
Done. `bard export`/`import`/`snapshot`, plus the nightly snapshot.

The snapshot schedule reads its due time from the snapshot directory listing
rather than a stored timestamp, so a failed night stays due and is retried
without anything to reset. It polls on a ticker because a job cannot queue its
own successor: the dedupe index covers `running`, so it would collide with
itself.

### 12.2: Build, install and CI
Done. `scripts/build`, `scripts/release`, `scripts/install`, the systemd unit,
the Docker image, and the two workflows.

`scripts/release` fails the build if `file` reports a dynamically linked
artefact, because nothing else in the build would say so and a dynamic binary
defeats the point of shipping one file.

**The container needed the data directory to exist in the image**, owned by the
runtime uid. Docker seeds a fresh named volume from the image including its
ownership, so without that the volume arrives root-owned and the server cannot
open its own database.

**Docker's NAT is itself a `trusted_proxies` trap.** Every request through a
published port arrives from the bridge gateway, so the real client address is
gone before Bard sees it. Verified: a request to the container logged
`client=172.17.0.1`.

### 12.3: TLS and diagnostics
Done. `tls: auto` via autocert, and the diagnostics page.

**An ACME challenge can only be answered where the CA looks for it** —
TLS-ALPN-01 on 443 or HTTP-01 on 80 — and Bard's default port is neither. So
`tls.challenge_addr` is a separate listener, and losing it is logged rather
than left to a renewal that silently fails months later. `tls.acme_directory`
exists so a deployment can be rehearsed against staging without spending a
production rate limit that takes a week to recover.

autocert's own non-challenge fallback redirects to port 443, which is the
wrong place whenever Bard serves TLS anywhere else, so the redirect is Bard's.

**The diagnostics page is a page, not a CLI verb**, because the question it
answers is "what did the deployment do to this request" — and only a request
that actually came through the tunnel can answer it. A verb on the host would
report on a path no real client takes.

### 12.4: Documentation
Done. README, the three deploy recipes plus Docker, backup and restore,
SECURITY, CHANGELOG, LICENSE.

The README leads with the limitation rather than burying it: there is no
first-party reading app yet, and someone looking for one tonight should find
that out in the first paragraph rather than after installing.

**Release archives install themselves.** Writing the install docs exposed that
a downloaded tarball held only a binary, so following the README meant cloning
the repo for a unit file. Each archive now carries the binary, the installer
and the unit, verified by installing from one in a container with no repo
present.

## Files to Create

- `scripts/build`, `scripts/release`, `scripts/install`
- `.github/workflows/ci.yml`, `release.yml`
- `packaging/systemd/bard.service`
- `Dockerfile`, `docker-compose.yml`
- `internal/api/tls.go` — autocert wiring
- `internal/api/web_diagnostics.go`
- `internal/db/backup.go`, `internal/backup/`, `cmd/bard/export.go`
- `docs/deploy/cloudflare-tunnel.md`, `reverse-proxy.md`, `tailscale.md`
- `README.md`, `CHANGELOG.md`, `SECURITY.md`

## Dependencies

**Internal:** all prior phases.

**External:**
- `golang.org/x/crypto/acme/autocert` — only for `tls: auto`
- goreleaser, GitHub Actions (CI only)

## Implementation Notes

`CGO_ENABLED=0` everywhere. The pure-Go choices in Decision 1 exist so the release matrix is trivial — verify the arm64 artifact actually runs rather than assuming it cross-compiled correctly.

The systemd unit runs as a dedicated non-root user, which is why the default port sits outside the privileged range. Default state directory under `/var/lib/bard`, config at `/etc/bard/bard.yml`.

**The Cloudflare Tunnel recipe is the primary path** and needs its asymmetry spelled out: `cloudflared` connects outbound and forwards to Bard over plain HTTP on localhost, so `tls: off` is correct and Let's Encrypt is neither possible nor needed there — ACME challenges have no inbound path to answer on. Document both caveats from Decision 13: Cloudflare's self-serve terms discourage proxying large volumes of non-HTML content, and Cloudflare terminates TLS and can see the traffic.

**Every recipe must state its `trusted_proxies` value.** Behind a tunnel, requests arrive from `127.0.0.1`, and a wrong or missing allowlist silently breaks per-device rate limiting — the exact failure Phase 1 was sequenced early to prevent. This is the single most important line in each deploy doc.

`tls: auto` serves only the direct port-forward case. Say plainly in the docs that exposing a port makes the server internet-attackable and that Phase 2's rate limiting is what makes that survivable.

Snapshots use SQLite's backup API or `VACUUM INTO`, never a filesystem copy of a live WAL database. `bard export` produces a portable archive — database plus cover cache — so the install moves to another machine, which is Decision 3's promise.

The cover cache shards by hash prefix (`covers/42/<hash>/480.jpg`), so both
halves of the archive have to walk it: a flat read exports nothing, and
flattening the names on restore collides every book's `full.jpg`.

README must cover: what Bard is, supported formats, install, point-at-a-directory, and the explicit statement that apps come later. Set expectations honestly — the server is useful via the admin UI and the API, and there is no first-party reading client yet.

## Validation

- [~] A tagged release produces static amd64 and arm64 binaries
      *(12.2: `scripts/release` verified locally and the workflow calls that
      same script, but no tag has been pushed through Actions yet)*
- [x] The arm64 binary runs on arm64 hardware or under emulation
      *(12.2: under qemu-aarch64 the arm64 build applied all 16 migrations,
      hashed with Argon2id, scanned the real library to the same 7 editions /
      58 files / 7 covers as amd64, and matched an FTS5 search — which is the
      pure-Go SQLite claim in Decision 1 actually tested)*
- [x] `ldd` reports a static binary, or the equivalent check passes
      *(12.2: "not a dynamic executable" for both, and `release` now refuses
      to ship one that is)*
- [~] The install script produces a running service on a clean machine
      *(12.2: run in a clean Debian container — creates the `bard` system
      account, 0640 root:bard config, 0750 data dir, and the account with no
      supplementary groups ran the unit's exact ExecStart and served
      `/healthz`. systemd itself was stubbed, so the unit is verified only by
      `systemd-analyze verify`)*
- [x] The Docker image starts, persists state across restarts, and serves the UI
      *(12.2: 23MB scratch image; an admin created through the UI survived a
      container restart)*
- [ ] Following the Cloudflare Tunnel recipe verbatim yields a working remote connection
      *(12.4: written, and `cloudflared` is not installed on this machine.
      Needs a Cloudflare account and a domain, so it is the user's to walk)*
- [~] After following it, two devices on different networks are rate-limited independently
      *(12.3: /diagnostics reports the address rate limiting keys on, and
      eight tests cover the verdicts including both ways `trusted_proxies` can
      be wrong. Two real networks still need the tunnel standing up)*
- [~] `tls: auto` obtains a certificate against the Let's Encrypt staging environment
      *(12.3: the wiring is verified — autocert serves the challenge path,
      enforces its host policy, and a handshake with SNI reaches the ACME
      client at the configured directory. A real staging certificate needs a
      public domain with inbound port 80, which this machine does not have;
      `tls.acme_directory` is there so it can be done on one that does)*
- [x] A nightly snapshot restores into a working server
      *(12.1: the boot snapshot of a restored install, copied to `bard.db`,
      served the library and accepted the original password)*
- [x] `bard export` output restores on a different machine with manual corrections intact
      *(12.1: 6 books, 1 account, 14 covers; a hand-set age survived and the
      covers served from the restored cache)*
- [~] CI runs tests on every push and blocks a release on failure
      *(12.2: both workflows written; `release.yml` runs the suite before it
      builds. Neither has run, since there is no remote yet)*
