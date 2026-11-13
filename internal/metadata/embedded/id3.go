package embedded

import (
	"os"
	"strings"

	"github.com/dhowden/tag"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

// ParseMP3Set reads a folder of parts as one edition: tags come from the first
// file that has them, and each file becomes a chapter at its running offset.
// The per-file durations go back to the file rows, which is what lets a client
// seek to a position without opening every part.
func ParseMP3Set(paths []string) (metadata.Metadata, []*float64) {
	md := metadata.Metadata{}
	durations := make([]*float64, len(paths))

	for i, path := range paths {
		durations[i] = mp3Duration(path)

		if md.Title == "" {
			if tags, err := readTags(path); err == nil {
				applyAudioTags(&md, tags)
				// A part's own title names the part, not the book; the album
				// is the book, so prefer it and keep the rest.
				if album := strings.TrimSpace(tags.Album()); album != "" {
					md.Title = album
				}
			}
		}
	}

	md.Chapters = mp3Chapters(paths, durations)
	md.DurationSeconds = totalDuration(durations)
	return md, durations
}

// mp3Chapters makes one chapter per file. A chapter-per-file audiobook has no
// finer structure to read, and Phase 7's read-to-listen handoff needs the
// offsets regardless.
func mp3Chapters(paths []string, durations []*float64) []metadata.Chapter {
	chapters := make([]metadata.Chapter, 0, len(paths))
	var elapsed float64
	known := true

	for i, path := range paths {
		title := partTitle(path)

		chapter := metadata.Chapter{Title: title}
		if known {
			start := elapsed
			chapter.StartSeconds = &start
		}
		chapters = append(chapters, chapter)

		// Once one part's length is unknown every later offset is guesswork,
		// so stop claiming them rather than reporting wrong ones.
		if durations[i] == nil {
			known = false
			continue
		}
		elapsed += *durations[i]
	}
	return chapters
}

func partTitle(path string) string {
	if tags, err := readTags(path); err == nil {
		if t := strings.TrimSpace(tags.Title()); t != "" {
			return t
		}
	}
	return stem(path)
}

func totalDuration(durations []*float64) *float64 {
	var total float64
	for _, d := range durations {
		if d == nil {
			return nil
		}
		total += *d
	}
	if total <= 0 {
		return nil
	}
	return &total
}

func readTags(path string) (tag.Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return tag.ReadFrom(f)
}

func stem(path string) string {
	base := path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return base
}
