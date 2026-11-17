package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/not-emily/bardbookserver/internal/backup"
	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
)

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	out := fs.String("out", "", "archive to write (default: bard-export-<date>.tar.gz)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	path := *out
	if path == "" {
		path = fmt.Sprintf("bard-export-%s.tar.gz", time.Now().UTC().Format("2006-01-02"))
	}

	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer database.Close()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer file.Close()

	manifest, err := backup.Export(ctx, database, cfg.CoverCacheDir(), version, file)
	if err != nil {
		os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	fmt.Printf("wrote %s (%d books, %d accounts, %d covers)\n",
		path, manifest.Books, manifest.Accounts, manifest.Covers)
	fmt.Println("media is not included; point the restored install at the same directories")
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	force := fs.Bool("force", false, "overwrite an install that is already there")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: bard import [--force] ARCHIVE")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	file, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer file.Close()

	manifest, err := backup.Restore(file, cfg.DataDir, cfg.CoverCacheDir(), *force)
	switch {
	case errors.Is(err, backup.ErrNotEmpty):
		return fmt.Errorf("%s already holds a database; stop bard and pass --force to replace it", cfg.DataDir)
	case err != nil:
		return err
	}

	fmt.Printf("restored %d books, %d accounts and %d covers into %s\n",
		manifest.Books, manifest.Accounts, manifest.Covers, cfg.DataDir)
	fmt.Println("run `bard scan` once the sources point at this machine's media")
	return nil
}

func cmdSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer database.Close()

	name := db.SnapshotName(time.Now())
	if err := database.Snapshot(ctx, filepath.Join(cfg.SnapshotDir(), name)); err != nil {
		return err
	}
	fmt.Println(filepath.Join(cfg.SnapshotDir(), name))

	if cfg.Backup.Enabled {
		if _, err := db.PruneSnapshots(cfg.SnapshotDir(), cfg.Backup.Keep); err != nil {
			return err
		}
	}
	return nil
}
