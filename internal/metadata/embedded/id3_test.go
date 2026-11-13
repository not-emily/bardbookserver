package embedded

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// mp3FrameHeader builds a 128 kbps 44.1 kHz stereo MPEG 1 Layer III header,
// which is 1152 samples per frame.
func mp3FrameHeader() []byte {
	return []byte{
		0xFF, 0xFB, // sync, MPEG1, Layer III, no CRC
		0x90, // bitrate index 9 (128k), sample rate index 0 (44100), no padding
		0x00, // stereo
	}
}

// writeMP3 builds a file with an optional ID3v2 tag of the given size, a frame
// header, and optionally a Xing header carrying a frame count. Padding makes
// the file long enough for the CBR estimate to have something to measure.
func writeMP3(t *testing.T, tagBytes int, xingFrames int, audioPadding int) string {
	t.Helper()
	var b bytes.Buffer

	if tagBytes > 0 {
		b.WriteString("ID3")
		b.Write([]byte{4, 0, 0}) // version, revision, flags
		size := uint32(tagBytes)
		b.Write([]byte{
			byte(size >> 21 & 0x7F), byte(size >> 14 & 0x7F),
			byte(size >> 7 & 0x7F), byte(size & 0x7F),
		})
		// A tag body containing a sync word and the text "Info", both of which
		// a naive scan would mistake for the start of the audio.
		body := make([]byte, tagBytes)
		copy(body, []byte("TIT2Information\xff\xfb\x90\x00"))
		b.Write(body)
	}

	frame := mp3FrameHeader()
	b.Write(frame)
	if xingFrames > 0 {
		b.Write(make([]byte, 32)) // stereo MPEG 1 side info
		b.WriteString("Xing")
		binary.Write(&b, binary.BigEndian, uint32(1)) // frames flag
		binary.Write(&b, binary.BigEndian, uint32(xingFrames))
	}
	b.Write(make([]byte, audioPadding))

	path := filepath.Join(t.TempDir(), "fixture.mp3")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMP3DurationFromXingHeader(t *testing.T) {
	// 601 frames at 1152 samples and 44100 Hz.
	const frames = 601
	want := float64(frames) * 1152 / 44100

	got := mp3Duration(writeMP3(t, 2048, frames, 1000))
	if got == nil {
		t.Fatal("no duration")
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Errorf("duration %v, want %v", *got, want)
	}
}

// The Xing header's position is fixed by the side info size. Finding it by
// searching would match the "Info" text inside the ID3 tag instead.
func TestMP3DurationIgnoresInfoTextInTheTag(t *testing.T) {
	const frames = 100
	want := float64(frames) * 1152 / 44100

	got := mp3Duration(writeMP3(t, 4096, frames, 500))
	if got == nil {
		t.Fatal("no duration")
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Errorf("duration %v, want %v from the real Xing header", *got, want)
	}
}

func TestMP3DurationFallsBackToBitrate(t *testing.T) {
	// No Xing header, so the estimate is audio bytes over the frame bitrate.
	const padding = 128000
	path := writeMP3(t, 0, 0, padding)

	got := mp3Duration(path)
	if got == nil {
		t.Fatal("no duration")
	}
	want := float64(padding+len(mp3FrameHeader())) * 8 / 128000
	if math.Abs(*got-want) > 1e-6 {
		t.Errorf("duration %v, want the CBR estimate %v", *got, want)
	}
}

func TestMP3DurationOfSomethingThatIsNotAnMP3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.mp3")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x00}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mp3Duration(path); got != nil {
		t.Errorf("duration %v for a file with no frame header, want nil", got)
	}
}

func TestID3v2SizeSkipsTheTag(t *testing.T) {
	path := writeMP3(t, 2048, 100, 0)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if got, want := id3v2Size(f), int64(10+2048); got != want {
		t.Errorf("id3v2Size = %d, want %d", got, want)
	}
}

func TestMP3SetChaptersAccumulateOffsets(t *testing.T) {
	paths := []string{
		writeMP3(t, 0, 0, 128000), // 8s at 128 kbps
		writeMP3(t, 0, 0, 128000),
		writeMP3(t, 0, 0, 64000), // 4s
	}

	md, durations := ParseMP3Set(paths)
	if len(durations) != 3 {
		t.Fatalf("got %d durations, want 3", len(durations))
	}
	if len(md.Chapters) != 3 {
		t.Fatalf("got %d chapters, want one per file", len(md.Chapters))
	}

	var elapsed float64
	for i, c := range md.Chapters {
		if c.StartSeconds == nil {
			t.Fatalf("chapter %d has no offset", i)
		}
		if math.Abs(*c.StartSeconds-elapsed) > 1e-6 {
			t.Errorf("chapter %d starts at %v, want %v", i, *c.StartSeconds, elapsed)
		}
		if durations[i] == nil {
			t.Fatalf("file %d has no duration", i)
		}
		elapsed += *durations[i]
	}

	if md.DurationSeconds == nil || math.Abs(*md.DurationSeconds-elapsed) > 1e-6 {
		t.Errorf("total %v, want the sum %v", md.DurationSeconds, elapsed)
	}
}

// Once one part's length is unknown, every later offset would be a guess, so
// the parser stops claiming them rather than reporting wrong ones.
func TestMP3SetStopsOffsetsAtAnUnreadablePart(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.mp3")
	if err := os.WriteFile(bad, bytes.Repeat([]byte{0}, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := []string{writeMP3(t, 0, 0, 128000), bad, writeMP3(t, 0, 0, 128000)}

	md, durations := ParseMP3Set(paths)
	if durations[1] != nil {
		t.Errorf("unreadable part reported duration %v", *durations[1])
	}
	if md.Chapters[0].StartSeconds == nil {
		t.Error("first chapter lost its offset")
	}
	if md.Chapters[1].StartSeconds == nil {
		t.Error("the unreadable part should still start where the first one ended")
	}
	if md.Chapters[2].StartSeconds != nil {
		t.Errorf("chapter after the gap claims offset %v", *md.Chapters[2].StartSeconds)
	}
	if md.DurationSeconds != nil {
		t.Errorf("total %v, want unknown when a part is unreadable", *md.DurationSeconds)
	}
}

func sampleSet(t *testing.T, dir string) []string {
	t.Helper()
	d := filepath.Join(sampleDir, dir)
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Skipf("sample set %s not present", dir)
	}

	var paths []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".mp3" {
			paths = append(paths, filepath.Join(d, e.Name()))
		}
	}
	if len(paths) == 0 {
		t.Skipf("sample set %s holds no mp3s", dir)
	}
	sort.Strings(paths)
	return paths
}

func TestRealMP3SetsParse(t *testing.T) {
	cases := []struct {
		dir, title, author string
		files              int
		minHours           float64
	}{
		{"Information Doesn_t Want to Be Free", "Information Doesn't Want to Be Free", "Cory Doctorow", 6, 5},
		{"Red Rising", "Red Rising", "Pierce Brown", 47, 16},
	}
	for _, c := range cases {
		paths := sampleSet(t, c.dir)
		if len(paths) != c.files {
			t.Errorf("%s has %d files, want %d", c.dir, len(paths), c.files)
		}

		md, durations := ParseMP3Set(paths)
		if md.Title != c.title {
			t.Errorf("%s title %q, want %q from the album tag", c.dir, md.Title, c.title)
		}
		if len(md.Authors) == 0 || md.Authors[0] != c.author {
			t.Errorf("%s authors %v, want %q", c.dir, md.Authors, c.author)
		}
		if md.DurationSeconds == nil || *md.DurationSeconds/3600 < c.minHours {
			t.Errorf("%s duration %v, want at least %v hours", c.dir, md.DurationSeconds, c.minHours)
		}
		if len(md.Chapters) != len(paths) {
			t.Errorf("%s got %d chapters, want one per file", c.dir, len(md.Chapters))
		}
		for i, d := range durations {
			if d == nil || *d <= 0 {
				t.Errorf("%s part %d has no duration", c.dir, i)
			}
		}
	}
}

// The same book as a single M4B and as a folder of parts must agree on length,
// which is the strongest check available on two independent parsers.
func TestRealM4BAndMP3SetAgreeOnDuration(t *testing.T) {
	m4b, err := ParseM4B(sample(t, "Red Rising.m4b"))
	if err != nil {
		t.Fatal(err)
	}
	set, _ := ParseMP3Set(sampleSet(t, "Red Rising"))

	if m4b.DurationSeconds == nil || set.DurationSeconds == nil {
		t.Fatal("one of the two editions has no duration")
	}

	drift := math.Abs(*m4b.DurationSeconds - *set.DurationSeconds)
	// The MP3 side runs slightly long: a Xing frame count includes the header
	// frame and Bard does not subtract LAME gapless delay and padding.
	if drift > 10 {
		t.Errorf("m4b says %.1fs and the mp3 set says %.1fs, a drift of %.1fs",
			*m4b.DurationSeconds, *set.DurationSeconds, drift)
	}
	t.Logf("m4b %.1fs vs mp3 set %.1fs, drift %.2fs over %.1f hours",
		*m4b.DurationSeconds, *set.DurationSeconds, drift, *m4b.DurationSeconds/3600)
}
