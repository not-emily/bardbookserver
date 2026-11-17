# Bard

A self-hosted server for your ebooks and audiobooks. Plex, but for books.

Point it at directories of book files. It indexes them, works out what they
are, groups the EPUB and the audiobook of the same title into one book, and
serves the lot over an authenticated HTTP API — to your family, from anywhere,
without handing your library to anybody.

One static Go binary. No daemon, no runtime, no database to install.

## Read this before you start

**There is no Bard reading app yet.** iOS and Android clients are the next
project; this repository is the server half. Today you get a web admin UI for
setting everything up and an HTTP API to build against. If you are looking for
something to read a book on tonight, this is not yet it.

What does work, completely: scanning, metadata, grouping, accounts, age gating,
remote access, uploads, and the API that a client will use.

## What it handles

| Format | Notes |
| --- | --- |
| EPUB | Metadata and cover from the OPF |
| PDF | Metadata where the file carries any |
| M4B | Chapters from the Nero `chpl` atom |
| Folder of MP3s | One audiobook, chapters from the files and ID3 tags |

**Never transcoded.** M4B is AAC in an MP4 container and plays natively on
everything that matters. `ffprobe` is an optional fallback for chapter
extraction and never a requirement.

**Your files are never renamed or moved.** The only place Bard writes media is
when you upload something new.

## Install

Needs a Linux host with systemd, amd64 or arm64.

```
curl -LO https://github.com/not-emily/bardbookserver/releases/latest/download/bard-linux-amd64.tar.gz
tar xzf bard-linux-amd64.tar.gz
cd bard-linux-amd64
sudo ./install
```

The archive carries the binary, the installer and the systemd unit, so there is
nothing to clone. Use `bard-linux-arm64.tar.gz` on a Pi or an ARM server.

Or from source, if Go 1.27 is already there:

```
git clone https://github.com/not-emily/bardbookserver
cd bardbookserver
sudo ./scripts/install
```

Or push it to a server from a machine that has the repo. The host needs no Go,
no git and no access to GitHub, because everything is built here:

```
scripts/deploy bard-server --install    # detects the arch, builds, installs
scripts/deploy bard-server --restart    # later, to upgrade the binary
```

Either way you get a `bard` system account, `/etc/bard/bard.yml`,
`/var/lib/bard`, and a running service. Re-running upgrades the binary and
leaves your config and data alone.

Then open <http://127.0.0.1:2273/> and create the first admin account. The setup
wizard takes you through adding a source and running the first scan.

**On a headless server** there is no browser at that address, so either forward
the port — `ssh -L 2273:127.0.0.1:2273 your-server` — and use the wizard from
your own machine, or skip it and create the account over SSH:

```
sudo -u bard bard user add --admin --username you
sudo -u bard bard source add --name Books --path /srv/books --min-age 0
sudo -u bard bard scan
```

Run the CLI as the `bard` user rather than root. As root the database and its
write-ahead log end up root-owned, and the service cannot write to them.

There is also a [Docker image](docs/deploy/docker.md), with caveats.

## Reaching it from outside the house

Bard serves plain HTTP and expects something in front of it. Pick one:

| Recipe | Good for | Trade |
| --- | --- | --- |
| **[Cloudflare Tunnel](docs/deploy/cloudflare-tunnel.md)** | Most people | Cloudflare terminates TLS and can see traffic |
| [Tailscale](docs/deploy/tailscale.md) | Maximum privacy | Every reader must join your tailnet |
| [Reverse proxy](docs/deploy/reverse-proxy.md) | A host with a public IP | Your login form faces the internet |

**Every recipe states its `trusted_proxies` value, and that line is the one
people get wrong.** Set it too narrow and every client behind your tunnel
shares a single rate-limit bucket, so one person fumbling a password locks out
the household. Set it too wide and a caller can forge `X-Forwarded-For` and
walk straight past login rate limiting. Both failures are silent.

Which is why Bard has a `/diagnostics` page: load it the way a real client
reaches you, and it tells you which address rate limiting actually keys on and
whether that is right.

## Configuration

`/etc/bard/bard.yml`. Everything has a default; a missing file still serves.

```yaml
data_dir: /var/lib/bard
bind_addr: 127.0.0.1
port: 2273

trusted_proxies: ["127.0.0.1/32"]   # see your deploy recipe

workers: 2             # background scan concurrency
log_level: info

tls:
  mode: "off"          # off | files | auto

metadata:
  providers:           # all off by default: a lookup sends your titles away
    openlibrary: true
  timeout_seconds: 10

backup:
  enabled: true
  interval_hours: 24
  keep: 7
```

**Online metadata lookup is off until you turn it on.** Enabling a provider
means your book titles leave your house, so it is your decision rather than a
default. Everything works without it, from what your files already carry.

## Commands

```
bard serve                     Run the server
bard scan [--source NAME]      Scan now, in the foreground
bard source add|list           Point it at a directory
bard user add|passwd|pin|list  Manage accounts
bard export [--out PATH]       A portable archive of this install
bard import [--force] FILE     Restore one
bard snapshot                  Database snapshot now
bard version
```

## Backups

Nightly snapshots are on by default. They carry what a rescan cannot rebuild:
accounts, everyone's reading progress and ratings, and every manual correction
you have made.

`bard export` moves an install to another machine. It does **not** include your
media — that is your library, and it stays where it is.

See [backup and restore](docs/backup.md).

## How it thinks about your library

- **One library, many sources.** A *source* is a directory root. "Library"
  means the whole collection, which is what people browse — nobody browses a
  source.
- **Book → Edition → File.** A **book** is what you browse. An **edition** is a
  manifestation of it: the EPUB, a particular narration, an abridgement. A
  **file** is bytes. Series sit above books; authors attach to a book, narrators
  to one edition.
- **Disk is the truth.** The database is an index you can delete and rebuild.
  Corrections you make by hand are written to a `bard.json` sidecar beside the
  media, so they survive even that.
- **Nobody sees anybody else's shelf.** Progress, ratings, history and
  annotations are private per account, including from an admin.

## Age gating

Each source carries a default minimum age; individual books can override it,
and individual users can be granted exceptions. A restricted account with no
age information on a book is refused rather than allowed — it fails closed.

Suggested ratings from metadata are only ever *suggestions*. Nothing is applied
until an admin confirms it in the rating queue.

A source can also hide age-locked books entirely rather than showing them
locked, for when "you may not read this" is itself the wrong message.

## Running it yourself

```
./scripts/build          # a binary for this machine
./scripts/release        # static amd64 + arm64 into dist/
./scripts/dev start      # local server on an isolated data dir
go test ./...
```

No code generation, no Node, no build step. `go build` is the whole story.

## Security

Bard is built to be exposed to the internet, because reading on your phone over
cellular was a day-one requirement. Argon2id passwords, revocable per-device
tokens, rate-limited logins, and CSRF protection on the cookie-authenticated
browser session.

Please read [SECURITY.md](SECURITY.md) before exposing it, and report anything
you find there rather than in a public issue.

## Licence

MIT. See [LICENSE](LICENSE).
