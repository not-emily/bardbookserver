# Backup and restore

Your media is your media — Bard never moves or renames it, and a backup of your
books is whatever you already do for the rest of your files.

What Bard holds that **cannot be re-derived from the media** is the other half:

- every account, its password hash and PIN
- what everybody has read, how far, their ratings, shelves, history and
  annotations
- every manual correction — merges, splits, locked fields, chosen matches, age
  ratings you confirmed
- the genre mappings you taught it

A rescan rebuilds the index. It cannot rebuild any of that. So that is what the
snapshots and the export carry.

## Nightly snapshots

On by default, in `<data_dir>/snapshots`, keeping a week:

```yaml
backup:
  enabled: true
  interval_hours: 24
  keep: 7
```

A snapshot is a complete, consistent SQLite database written with
`VACUUM INTO`, **never a file copy** — a live database's committed state is
spread across its write-ahead log, so copying the file gets you a torn read
that may not open at all.

There is no schedule stored anywhere. Bard reads the newest filename in the
snapshot directory and takes another when that one is old enough, which means a
night that failed is simply still due, and a restart cannot skip or double up.

To restore one, stop the server and put it in place:

```
sudo systemctl stop bard
sudo -u bard cp /var/lib/bard/snapshots/bard-2026-11-17-030000.db /var/lib/bard/bard.db
sudo systemctl start bard
```

Delete any `bard.db-wal` and `bard.db-shm` alongside it first; they belong to
the database you are replacing.

Snapshots are mode 0600, because a snapshot is a complete copy of every
password hash and device token in your install. Treat them accordingly — a
snapshot on a shared drive is those hashes on a shared drive.

**A snapshot beside the database does not protect you from losing the disk.**
It protects you from a bad migration, a mistaken bulk edit, or a corruption.
For the disk, copy the export somewhere else.

## Moving to another machine

```
bard export --out bard-export.tar.gz
```

That is a snapshot plus the cover cache, and **deliberately not your media**.
The media is the library; it stays where it is and the new machine points at it.

On the new machine, with Bard installed but never started:

```
bard import bard-export.tar.gz
```

It refuses a data directory that already holds a database, because what it
would overwrite is everyone's reading. `--force` overrides that, and stop the
server first.

Then point the sources at wherever the media lives on *this* machine — edit the
path in the admin UI rather than re-adding the source, because re-adding
discards every edition and the reading progress hanging off it — and scan:

```
bard scan
```

Covers come back from the archive, so the first browse is not blank while a
rescan refetches them.

## Taking one by hand

```
bard snapshot
```

Prints the path it wrote and prunes to `backup.keep`. Safe while the server is
running; that is what `VACUUM INTO` is for.

## What is not covered

- **Media files.** Never in an export, by design.
- **Your config.** `/etc/bard/bard.yml` is yours; it is small and you wrote it.
- **The provider response cache.** Rebuilt on demand, and not worth carrying.
