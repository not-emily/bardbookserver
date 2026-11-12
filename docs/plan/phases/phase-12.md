# Phase 12: Packaging & Release

> **Depends on:** Phase 11
> **Enables:** Public install; the mobile apps as separate projects
>
> See: [Full Plan](../plan.md)

## Goal

Make Bard installable from a public GitHub repo, and safe to expose to the internet.

## Key Deliverables

- goreleaser config: static binaries for linux amd64 and arm64
- GitHub Actions: build, test, release on tag
- systemd unit and an install script
- Docker image and a `docker-compose.yml`
- Optional built-in ACME (`tls: auto`) for the port-forward case
- Three deployment recipes: Cloudflare Tunnel (recommended), reverse proxy, Tailscale
- Nightly SQLite snapshot and `bard export`
- README, CHANGELOG, MIT license
- Security notes documenting the internet-exposure posture

## Files to Create

- `.goreleaser.yml`
- `.github/workflows/ci.yml`, `release.yml`
- `packaging/systemd/bard.service`, `packaging/install.sh`
- `Dockerfile`, `docker-compose.yml`
- `internal/api/tls.go` — autocert wiring
- `internal/db/backup.go`, `cmd/bard/export.go`
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

README must cover: what Bard is, supported formats, install, point-at-a-directory, and the explicit statement that apps come later. Set expectations honestly — the server is useful via the admin UI and the API, and there is no first-party reading client yet.

## Validation

- [ ] A tagged release produces static amd64 and arm64 binaries
- [ ] The arm64 binary runs on arm64 hardware or under emulation
- [ ] `ldd` reports a static binary, or the equivalent check passes
- [ ] The install script produces a running service on a clean machine
- [ ] The Docker image starts, persists state across restarts, and serves the UI
- [ ] Following the Cloudflare Tunnel recipe verbatim yields a working remote connection
- [ ] After following it, two devices on different networks are rate-limited independently
- [ ] `tls: auto` obtains a certificate against the Let's Encrypt staging environment
- [ ] A nightly snapshot restores into a working server
- [ ] `bard export` output restores on a different machine with manual corrections intact
- [ ] CI runs tests on every push and blocks a release on failure
