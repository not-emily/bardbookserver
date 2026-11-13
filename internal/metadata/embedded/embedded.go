// Package embedded reads the metadata a book file carries about itself.
package embedded

import (
	"fmt"

	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

// Result adds the per-file facts an edition-level Metadata cannot hold.
// FileDurations is parallel to the paths passed in.
type Result struct {
	metadata.Metadata
	FileDurations []*float64
}

// Parse reads an edition's files. The error is for the caller to log as a
// warning: a book Bard cannot parse still belongs in the library with whatever
// its path reveals, so a parse failure must never fail a scan.
func Parse(format model.Format, paths []string) (Result, error) {
	if len(paths) == 0 {
		return Result{}, fmt.Errorf("no files to parse")
	}

	switch format {
	case model.FormatEPUB:
		md, err := ParseEPUB(paths[0])
		return Result{Metadata: md}, err

	case model.FormatPDF:
		md, err := ParsePDF(paths[0])
		return Result{Metadata: md}, err

	case model.FormatM4B:
		md, err := ParseM4B(paths[0])
		return Result{Metadata: md}, err

	case model.FormatMP3:
		md, durations := ParseMP3Set(paths)
		return Result{Metadata: md, FileDurations: durations}, nil

	default:
		return Result{}, fmt.Errorf("no parser for format %q", format)
	}
}
