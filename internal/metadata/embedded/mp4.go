package embedded

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"github.com/abema/go-mp4"
	"github.com/dhowden/tag"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

// chplTicksPerSecond is the Nero chapter atom's unit: 100-nanosecond ticks.
const chplTicksPerSecond = 10_000_000

func ParseM4B(path string) (metadata.Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return metadata.Metadata{}, fmt.Errorf("open m4b %s: %w", path, err)
	}
	defer f.Close()

	md := metadata.Metadata{}

	// A file whose tags will not parse can still yield chapters and duration,
	// so a tag failure is not fatal here.
	if tags, err := tag.ReadFrom(f); err == nil {
		applyAudioTags(&md, tags)
	}

	if d := mp4Duration(f); d != nil {
		md.DurationSeconds = d
	}
	if desc := mp4TextAtom(f, "desc", "ldes"); desc != "" && md.Description == "" {
		md.Description = desc
	}
	md.Chapters = mp4Chapters(f)

	return md, nil
}

// applyAudioTags follows the iTunes audiobook convention, which MP3 and M4B
// share: artist is the author and composer is the narrator. Album artist is
// only a fallback author, because tools disagree on whether it names the
// author or the narrator.
func applyAudioTags(md *metadata.Metadata, tags tag.Metadata) {
	md.Title = firstNonEmpty(tags.Title(), tags.Album())
	if author := firstNonEmpty(tags.Artist(), tags.AlbumArtist()); author != "" {
		md.Authors = splitPeople(author)
	}
	if narrator := tags.Composer(); narrator != "" {
		md.Narrators = splitPeople(narrator)
	}
	if genre := tags.Genre(); genre != "" {
		md.Subjects = []string{genre}
	}
	if year := tags.Year(); year > 0 {
		md.PublishedOn = fmt.Sprintf("%04d", year)
	}
	md.Description = tags.Comment()

	if pic := tags.Picture(); pic != nil && len(pic.Data) > 0 {
		md.Cover = &metadata.Cover{Data: pic.Data, MIME: pic.MIMEType}
	}
}

func mp4Duration(f *os.File) *float64 {
	boxes, err := mp4.ExtractBoxWithPayload(f, nil,
		mp4.BoxPath{mp4.BoxTypeMoov(), mp4.BoxTypeMvhd()})
	if err != nil || len(boxes) == 0 {
		return nil
	}
	mvhd, ok := boxes[0].Payload.(*mp4.Mvhd)
	if !ok || mvhd.Timescale == 0 {
		return nil
	}
	seconds := float64(mvhd.GetDuration()) / float64(mvhd.Timescale)
	if seconds <= 0 {
		return nil
	}
	return &seconds
}

// mp4Chapters reads the Nero chapter atom. QuickTime text chapter tracks are
// not handled: they need a second track's sample tables, and ffprobe is the
// documented fallback for files that carry only those.
func mp4Chapters(f *os.File) []metadata.Chapter {
	boxes, err := mp4.ExtractBox(f, nil,
		mp4.BoxPath{mp4.BoxTypeMoov(), mp4.BoxTypeUdta(), mp4.StrToBoxType("chpl")})
	if err != nil || len(boxes) == 0 {
		return nil
	}

	bi := boxes[0]
	if bi.Size <= bi.HeaderSize {
		return nil
	}
	payload := make([]byte, bi.Size-bi.HeaderSize)
	if _, err := f.ReadAt(payload, int64(bi.Offset+bi.HeaderSize)); err != nil {
		return nil
	}
	return parseCHPL(payload)
}

// parseCHPL decodes the Nero layout: a version and flags, then a reserved word
// and a byte count in version 1, or a four-byte count in version 0.
func parseCHPL(p []byte) []metadata.Chapter {
	if len(p) < 5 {
		return nil
	}
	version := p[0]
	off := 4

	var count int
	if version == 0 {
		if len(p) < off+4 {
			return nil
		}
		count = int(binary.BigEndian.Uint32(p[off:]))
		off += 4
	} else {
		if len(p) < off+5 {
			return nil
		}
		off += 4 // reserved
		count = int(p[off])
		off++
	}

	chapters := make([]metadata.Chapter, 0, count)
	for i := 0; i < count; i++ {
		if len(p) < off+9 {
			break
		}
		ticks := binary.BigEndian.Uint64(p[off:])
		titleLen := int(p[off+8])
		off += 9
		if len(p) < off+titleLen {
			break
		}

		start := float64(ticks) / chplTicksPerSecond
		chapters = append(chapters, metadata.Chapter{
			Title:        strings.TrimSpace(string(p[off : off+titleLen])),
			StartSeconds: &start,
		})
		off += titleLen
	}
	return chapters
}

// mp4TextAtom reads a free-text atom tag does not map, such as the long
// description audiobooks carry.
func mp4TextAtom(f *os.File, names ...string) string {
	for _, name := range names {
		boxes, err := mp4.ExtractBoxWithPayload(f, nil, mp4.BoxPath{
			mp4.BoxTypeMoov(), mp4.BoxTypeUdta(), mp4.BoxTypeMeta(),
			mp4.BoxTypeIlst(), mp4.StrToBoxType(name), mp4.BoxTypeData(),
		})
		if err != nil || len(boxes) == 0 {
			continue
		}
		if data, ok := boxes[0].Payload.(*mp4.Data); ok {
			if v := strings.TrimSpace(string(data.Data)); v != "" {
				return v
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// splitPeople handles the several names tools pack into one tag field.
func splitPeople(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == ';' || r == '/'
	})
	if len(fields) == 1 {
		fields = strings.Split(v, ", ")
	}

	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
