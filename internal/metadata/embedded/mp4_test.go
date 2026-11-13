package embedded

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// box assembles an MP4 box: a big-endian length, the four-character type, then
// the payload.
func box(boxType string, payload []byte) []byte {
	out := make([]byte, 4, 8+len(payload))
	binary.BigEndian.PutUint32(out, uint32(8+len(payload)))
	out = append(out, boxType...)
	return append(out, payload...)
}

// mvhdPayload is a version 0 movie header: the flags, two timestamps, the
// timescale, the duration, then the trailing fields the box requires.
func mvhdPayload(timescale, duration uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 0}) // version 0 + flags
	b.Write(make([]byte, 8))    // creation, modification
	binary.Write(&b, binary.BigEndian, timescale)
	binary.Write(&b, binary.BigEndian, duration)
	// rate, volume, two reserved fields, the 36-byte matrix, 24 predefined
	// bytes and the next track id.
	b.Write(make([]byte, 4+2+2+8+36+24+4))
	return b.Bytes()
}

type fixtureChapter struct {
	start float64
	title string
}

func chplPayload(version byte, chapters []fixtureChapter) []byte {
	var b bytes.Buffer
	b.WriteByte(version)
	b.Write([]byte{0, 0, 0}) // flags

	if version == 0 {
		binary.Write(&b, binary.BigEndian, uint32(len(chapters)))
	} else {
		b.Write([]byte{0, 0, 0, 0}) // reserved
		b.WriteByte(byte(len(chapters)))
	}

	for _, c := range chapters {
		binary.Write(&b, binary.BigEndian, uint64(c.start*chplTicksPerSecond))
		b.WriteByte(byte(len(c.title)))
		b.WriteString(c.title)
	}
	return b.Bytes()
}

func writeM4B(t *testing.T, moovChildren ...[]byte) string {
	t.Helper()

	var payload []byte
	for _, child := range moovChildren {
		payload = append(payload, child...)
	}

	var file []byte
	file = append(file, box("ftyp", []byte("M4A isomM4A mp42"))...)
	file = append(file, box("moov", payload)...)

	path := filepath.Join(t.TempDir(), "fixture.m4b")
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseM4BDurationAndChapters(t *testing.T) {
	chapters := []fixtureChapter{
		{0, "An Unexpected Party"},
		{4.5, "Roast Mutton"},
		{9, "A Short Rest"},
	}
	path := writeM4B(t,
		box("mvhd", mvhdPayload(22050, 330750)), // 15s
		box("udta", box("chpl", chplPayload(1, chapters))),
	)

	md, err := ParseM4B(path)
	if err != nil {
		t.Fatal(err)
	}

	if md.DurationSeconds == nil || *md.DurationSeconds != 15 {
		t.Errorf("duration %v, want 15s from the timescale and duration", md.DurationSeconds)
	}
	if len(md.Chapters) != 3 {
		t.Fatalf("got %d chapters %+v, want 3", len(md.Chapters), md.Chapters)
	}
	for i, want := range chapters {
		got := md.Chapters[i]
		if got.Title != want.title {
			t.Errorf("chapter %d title %q, want %q", i, got.Title, want.title)
		}
		if got.StartSeconds == nil || *got.StartSeconds != want.start {
			t.Errorf("chapter %d start %v, want %v", i, got.StartSeconds, want.start)
		}
	}
}

// Version 0 writes a four-byte chapter count where version 1 writes one byte
// after a reserved word, and real files use both.
func TestParseCHPLVersionZero(t *testing.T) {
	chapters := []fixtureChapter{{0, "One"}, {12.25, "Two"}}
	got := parseCHPL(chplPayload(0, chapters))

	if len(got) != 2 {
		t.Fatalf("got %d chapters, want 2", len(got))
	}
	if got[1].Title != "Two" || got[1].StartSeconds == nil || *got[1].StartSeconds != 12.25 {
		t.Errorf("second chapter %+v", got[1])
	}
}

func TestParseCHPLStopsAtATruncatedEntry(t *testing.T) {
	full := chplPayload(1, []fixtureChapter{{0, "One"}, {5, "Two"}})

	// Cut inside the second entry's title.
	got := parseCHPL(full[:len(full)-2])
	if len(got) != 1 {
		t.Errorf("got %d chapters from a truncated atom, want the one complete entry", len(got))
	}

	if got := parseCHPL([]byte{1, 0, 0}); len(got) != 0 {
		t.Errorf("got %d chapters from a stub atom, want none", len(got))
	}
	if got := parseCHPL(nil); len(got) != 0 {
		t.Errorf("got %d chapters from an empty atom, want none", len(got))
	}
}

func TestParseM4BWithoutChaptersOrTags(t *testing.T) {
	path := writeM4B(t, box("mvhd", mvhdPayload(1000, 60000)))

	md, err := ParseM4B(path)
	if err != nil {
		t.Fatalf("a bare m4b failed to parse: %v", err)
	}
	if md.DurationSeconds == nil || *md.DurationSeconds != 60 {
		t.Errorf("duration %v, want 60s", md.DurationSeconds)
	}
	if len(md.Chapters) != 0 {
		t.Errorf("got %d chapters, want none", len(md.Chapters))
	}
}

func TestSplitPeople(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Pierce Brown", []string{"Pierce Brown"}},
		{"Alexandre Dumas; Auguste Maquet", []string{"Alexandre Dumas", "Auguste Maquet"}},
		{"Dumas/Maquet", []string{"Dumas", "Maquet"}},
		{"Herbert, Frank", []string{"Herbert", "Frank"}},
		{"  ", nil},
	}
	for _, c := range cases {
		got := splitPeople(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitPeople(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitPeople(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestRealM4BsParse(t *testing.T) {
	cases := []struct {
		file, title, author string
		minChapters         int
		minHours            float64
	}{
		{"Red Rising.m4b", "Red Rising", "Pierce Brown", 40, 15},
		{"The Fellowship of the Ring.m4b", "The Fellowship of the Ring", "J.R.R. Tolkien", 30, 20},
	}
	for _, c := range cases {
		md, err := ParseM4B(sample(t, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if md.Title != c.title {
			t.Errorf("%s title %q, want %q", c.file, md.Title, c.title)
		}
		if len(md.Authors) == 0 || md.Authors[0] != c.author {
			t.Errorf("%s authors %v, want %q", c.file, md.Authors, c.author)
		}
		if len(md.Chapters) < c.minChapters {
			t.Errorf("%s got %d chapters, want at least %d", c.file, len(md.Chapters), c.minChapters)
		}
		if md.DurationSeconds == nil || *md.DurationSeconds/3600 < c.minHours {
			t.Errorf("%s duration %v, want at least %v hours", c.file, md.DurationSeconds, c.minHours)
		}
		if md.Cover == nil || len(md.Cover.Data) == 0 {
			t.Errorf("%s has no cover", c.file)
		}

		// Chapter offsets must rise, or a player seeking by chapter goes backwards.
		for i := 1; i < len(md.Chapters); i++ {
			prev, cur := md.Chapters[i-1].StartSeconds, md.Chapters[i].StartSeconds
			if prev == nil || cur == nil {
				t.Fatalf("%s chapter %d has no offset", c.file, i)
			}
			if *cur <= *prev {
				t.Errorf("%s chapter %d starts at %v, not after %v", c.file, i, *cur, *prev)
			}
		}
	}
}
