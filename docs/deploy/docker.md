# Docker

**Only if you already run a container host.** Bard's primary install is a
static binary under systemd — see [the README](../../README.md#install) — which
is one file, needs no daemon, and avoids every caveat on this page.

```
docker compose up -d
```

## Mount media at the path it has on the host

A source stores the **absolute path** it was scanned at. Mount `/srv/books`
at `/media/books` inside the container and every indexed file records a path
that nothing outside the container can resolve — the admin UI shows a path that
does not exist, and an export restored onto bare metal points at nothing.

```yaml
volumes:
  - /srv/books:/srv/books:ro
```

Same path both sides. Read-only unless that source accepts uploads, which is
the one case Bard writes media.

## The data directory must be writable by uid 2273

The image runs as `2273:2273` rather than root. A named volume is seeded from
the image, so `bard-data` arrives owned correctly and needs nothing from you.
A **bind mount** does not:

```
sudo chown -R 2273:2273 /path/to/bard-data
```

Skip that and the server exits with `unable to open database file`.

## trusted_proxies, and why Docker makes this worse

```yaml
trusted_proxies: []        # published straight to the host
```

A published port is NAT'd, so **by the time a request reaches Bard the real
client address is already gone** — every request appears to come from the
bridge gateway, typically `172.17.0.1`. There is no setting that recovers it,
because the information was discarded before Bard was involved.

So:

- **Published straight to the host, nothing in front:** `[]`. Every client
  shares one rate-limit bucket. Acceptable on a LAN, not for anything exposed.
- **A tunnel or proxy in the same compose project:** trust the bridge network,
  `["172.16.0.0/12"]`, and make sure that proxy sends `X-Forwarded-For`. Now
  real client addresses come through.

Load `/diagnostics` after any change. It reports the address rate limiting
actually keys on, which on Docker is the number people are most often wrong
about.

## Config

`packaging/docker/bard.yml` is mounted read-only at `/etc/bard/bard.yml`. Edit
it on the host and restart:

```
docker compose restart
```

## Backups

The nightly snapshot lands inside the volume, which does not help if the volume
is what you lose. Pull a portable archive out instead:

```
docker compose exec bard /bard export --out /var/lib/bard/export.tar.gz
docker compose cp bard:/var/lib/bard/export.tar.gz ./
```

That carries the database and the cover cache, and deliberately not the media.
See [backup and restore](../backup.md).

## What you give up

- The absolute-path constraint above, which bare metal does not have.
- `tls: auto` needs ports 80 and 443 published, and the challenge listener
  reachable from the internet.
- One more moving part between a crash and the logs.

The systemd install has none of these. It is one static binary, a unit file,
and a config.
