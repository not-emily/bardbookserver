// Command gen writes docs/api.md from docs/api/openapi.yaml. Run it through
// scripts/docs; a test fails if its output is stale.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/not-emily/bardbookserver/internal/docs"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "docs:", err)
		os.Exit(1)
	}
}

func run() error {
	spec, err := docs.Load()
	if err != nil {
		return err
	}

	out := docs.MarkdownPath()
	if err := os.WriteFile(out, []byte(spec.Markdown()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}

	clean, err := filepath.Abs(out)
	if err != nil {
		clean = out
	}
	fmt.Printf("wrote %s (%d operations)\n", clean, len(spec.Operations()))
	return nil
}
