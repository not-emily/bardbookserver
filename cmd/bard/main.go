package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/not-emily/bardbookserver/internal/api"
	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bard:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "scan":
		return notImplemented("scan", 4)
	case "user":
		return cmdUser(args[1:])
	case "export":
		return notImplemented("export", 12)
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bard - self-hosted ebook and audiobook server

Usage:
  bard serve [--config PATH]   Run the server
  bard scan                    Scan configured sources
  bard user add|passwd|pin|list
                               Manage accounts
  bard export                  Export database and caches
  bard version                 Print the version

`)
}

func notImplemented(name string, phase int) error {
	return fmt.Errorf("%s is not implemented yet (v1 phase %d)", name, phase)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml (default: ./bard.yml, /etc/bard/bard.yml)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer database.Close()

	instanceID, err := database.Meta(ctx, "instance_id")
	if err != nil {
		return fmt.Errorf("read instance id: %w", err)
	}
	logger.Info("bard starting",
		"version", version,
		"instance", instanceID,
		"data_dir", cfg.DataDir)

	return api.NewServer(cfg, database, logger, version).Run(ctx)
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
