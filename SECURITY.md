# Security

Bard is designed to be exposed to the internet. Remote access over cellular
was a day-one requirement, which is why authentication, rate limiting and
trusted-proxy handling were built in phases 1 and 2 rather than added later.

This document is what you should know before you expose it, and how to tell me
if you find something wrong.

## Reporting a vulnerability

Use GitHub's **[private vulnerability reporting][pvr]** on this repository
(Security → Report a vulnerability). Please do not open a public issue for
anything exploitable.

Include what you did, what happened, and what you expected. A proof of concept
is welcome. I will acknowledge within a week — this is a personal project, not
a company with a rota, so please be patient and I will be honest about
timelines.

Nothing here carries a bounty. Credit in the changelog if you want it.

[pvr]: https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability

## The one setting that matters most

`trusted_proxies` is the list of hops Bard will believe an `X-Forwarded-For`
header from. **It fails silently in both directions:**

- **Too narrow** (or empty, behind a proxy): every client resolves to the
  proxy's address and shares one rate-limit bucket. One person fumbling a
  password locks out the household, and an attacker is throttled alongside
  your family instead of on their own.
- **Too wide**: a caller sets `X-Forwarded-For` to whatever it likes and walks
  past login rate limiting entirely.

It should list **only the hops in front of Bard** — never a range of clients.
Behind `cloudflared` or a local reverse proxy that is `["127.0.0.1/32"]`. With
nothing in front of Bard it is `[]`.

Load `/diagnostics` the way a real client reaches you. It reports the address
rate limiting actually keys on and says whether it is right. Every deploy
recipe in `docs/deploy/` states its value.

## What protects an exposed install

**Passwords** are hashed with Argon2id. There is no password recovery, because
there is no email — an admin resets one with `bard user passwd`.

**Devices hold opaque tokens**, not JWTs, stored as hashes. Every token is
listed in the admin UI with where and when it was last used, and revoking one
takes effect on the next request. There is nothing to "expire" client-side and
no signature to verify offline, which is deliberate: revocation that actually
revokes is worth more than statelessness here.

**Logins are rate limited** per resolved client address, as are PIN attempts
for managed profiles.

**Browser sessions are cookies, and CSRF is enforced on them.** The token is
per-device and compared in constant time; a non-GET request authenticated by
cookie without it is refused. A device issued before CSRF existed has an empty
stored token, which never matches, so it cannot be driven by a cookie at all.
API clients using `Authorization: Bearer` are not subject to CSRF, because a
header cannot be set by a cross-site form.

**Responses are explicit DTOs, never serialized models.** Models carry password
hashes, token hashes, age-gating internals and filesystem paths; a path in a
response would leak your server's layout. A test enforces this.

**Hidden content yields absence.** Where a source hides age-locked books, a
request for one returns 404, not a reason code. Reason codes only ever describe
things you can already see.

**Age gating fails closed.** A restricted account with nothing to compare is
refused rather than allowed, and the Go and SQL implementations are held
together by a test so they cannot drift.

## What does not protect you

**Bard does not encrypt your media.** Files on disk are files on disk.

**An admin can read everything in the library** — but not anyone else's
progress, ratings or annotations. Those are private per account by design,
including from admins.

**`tls: mode: off` is plain HTTP.** That is correct behind a tunnel or proxy
that terminates TLS, and wrong if nothing does. If Bard is exposed directly,
use `tls: auto`.

**Snapshots and exports are full copies of your database**, password hashes and
device tokens included. Snapshots are written mode 0600; an export is yours to
look after. One on a shared drive is those hashes on a shared drive.

**Uploads are admin-granted and sniffed, not trusted.** Only accounts with
`can_upload` may upload, and content is checked against its claimed type. It is
still a write into your library by someone you chose to trust.

## Supported versions

The latest release. This is a personal project; there are no backported fixes
to older tags.

## Hardening the service

The packaged systemd unit already runs as a dedicated non-root account with
`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome=read-only`, an empty
capability set and a read-only filesystem apart from its own data directory.
The default port is deliberately unprivileged so none of that needs relaxing.

If a source accepts uploads, that directory must be added to `ReadWritePaths`
in the unit. Scanning works without it; only uploading fails.
