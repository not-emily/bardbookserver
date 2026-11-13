package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/scan"
)

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	only := fs.String("source", "", "scan only this source, by name or id")
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

	sources, err := resolveSources(ctx, database, *only)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return errors.New("no enabled sources; add one with `bard source add`")
	}

	queue := jobs.NewQueue(database.DB)
	pool := jobs.NewPool(queue, cfg.Workers, logger)
	scan.NewScanner(database.DB, logger).Register(pool)

	for _, src := range sources {
		switch _, err := scan.EnqueueSource(ctx, queue, src.ID); {
		case errors.Is(err, jobs.ErrDuplicate):
			logger.Info("scan already queued", "source", src.Name)
		case err != nil:
			return err
		}
	}

	if err := pool.Drain(ctx); err != nil {
		return err
	}

	counts, err := queue.Counts(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	if failed := counts[jobs.StateFailed]; failed > 0 {
		return fmt.Errorf("%d job(s) failed; see `bard scan` output above", failed)
	}
	return nil
}

// resolveSources takes a name, an id, or nothing at all for every enabled one.
func resolveSources(ctx context.Context, database *db.DB, only string) ([]*model.Source, error) {
	if only == "" {
		return scan.EnabledSources(ctx, database.DB)
	}

	src, err := scan.SourceByName(ctx, database.DB, only)
	if errors.Is(err, scan.ErrSourceNotFound) {
		id, convErr := strconv.ParseInt(only, 10, 64)
		if convErr != nil {
			return nil, fmt.Errorf("no source named %q", only)
		}
		src, err = scan.SourceByID(ctx, database.DB, id)
	}
	if err != nil {
		return nil, err
	}
	return []*model.Source{src}, nil
}

// cmdSource is the minimum needed to point `bard scan` at a directory. Phase
// 11 replaces it with the admin UI.
func cmdSource(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bard source add|list")
	}

	switch args[0] {
	case "add":
		return cmdSourceAdd(args[1:])
	case "list":
		return cmdSourceList(args[1:])
	default:
		return fmt.Errorf("unknown source command %q", args[0])
	}
}

func cmdSourceAdd(args []string) error {
	fs := flag.NewFlagSet("source add", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to bard.yml")
	name := fs.String("name", "", "display name for the source (required)")
	path := fs.String("path", "", "directory to scan (required)")
	minAge := fs.Int("min-age", -1, "default minimum age for books in this source")
	sidecars := fs.Bool("sidecars", true, "write bard.json beside the media")
	hide := fs.Bool("hide-when-locked", false, "hide age-locked books from restricted users entirely")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *path == "" {
		return errors.New("source add requires --name and --path")
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

	in := scan.NewSource{
		Name:           *name,
		Path:           *path,
		WriteSidecars:  *sidecars,
		HideWhenLocked: *hide,
	}
	// A source with no default fails closed for restricted accounts, so the
	// flag stays unset rather than defaulting to a number.
	if *minAge >= 0 {
		in.DefaultMinAge = minAge
	}

	src, err := scan.CreateSource(ctx, database.DB, in)
	if err != nil {
		return err
	}
	fmt.Printf("added source %d %q at %s\n", src.ID, src.Name, src.Path)
	if src.DefaultMinAge == nil {
		fmt.Println("warning: no default_min_age set, so restricted accounts cannot read anything here")
	}
	return nil
}

func cmdSourceList(args []string) error {
	fs := flag.NewFlagSet("source list", flag.ContinueOnError)
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

	sources, err := scan.AllSources(ctx, database.DB)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		fmt.Println("no sources configured")
		return nil
	}

	for _, s := range sources {
		age := "unset"
		if s.DefaultMinAge != nil {
			age = strconv.Itoa(*s.DefaultMinAge)
		}
		scanned := "never"
		if s.LastScanFinishedAt != nil {
			scanned = s.LastScanFinishedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Printf("%d\t%s\tenabled=%t\tmin_age=%s\tscanned=%s\t%s\n",
			s.ID, s.Name, s.Enabled, age, scanned, s.Path)
	}
	return nil
}
