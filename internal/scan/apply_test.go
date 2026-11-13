package scan

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

func coverJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 600, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 600; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: 40, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeFixtureEPUB puts a real, parseable EPUB on disk at path.
func writeFixtureEPUB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>The Fellowship of the Ring</dc:title>
    <dc:creator id="a1">J.R.R. Tolkien</dc:creator>
    <meta property="role" refines="#a1">aut</meta>
    <dc:contributor id="n1">Rob Inglis</dc:contributor>
    <meta property="role" refines="#n1">nrt</meta>
    <dc:language>en</dc:language>
    <dc:publisher>Allen and Unwin</dc:publisher>
    <dc:identifier>urn:isbn:9780618574986</dc:identifier>
    <dc:description>A hobbit leaves home.</dc:description>
    <meta property="belongs-to-collection" id="c1">The Lord of the Rings</meta>
    <meta property="group-position" refines="#c1">1</meta>
  </metadata>
  <manifest>
    <item id="nav" href="toc.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="cov" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine/>
</package>`

	nav := `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <body><nav epub:type="toc"><ol>
    <li><a href="ch1.xhtml">A Long-expected Party</a></li>
    <li><a href="ch2.xhtml">The Shadow of the Past</a></li>
    <li><a href="ch3.xhtml">Three is Company</a></li>
  </ol></nav></body>
</html>`

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	entries := []struct {
		name string
		body []byte
	}{
		{"META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)},
		{"OEBPS/content.opf", []byte(opf)},
		{"OEBPS/toc.xhtml", []byte(nav)},
		{"OEBPS/cover.jpg", coverJPEG(t)},
	}
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func queryStrings(t *testing.T, d *db.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func scanOneEPUB(t *testing.T) (*Scanner, *db.DB, int64, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "Tolkien", "fotr.epub")
	writeFixtureEPUB(t, path)

	s, d, sourceID := testScanner(t, root)
	return s, d, sourceID, path
}

func TestScanAppliesEmbeddedMetadata(t *testing.T) {
	s, d, sourceID, _ := scanOneEPUB(t)
	res := mustScan(t, s, sourceID)

	if res.MetadataApplied != 1 {
		t.Errorf("metadata applied to %d editions, want 1", res.MetadataApplied)
	}
	if res.CoversStored != 1 {
		t.Errorf("stored %d covers, want 1", res.CoversStored)
	}

	ctx := context.Background()
	var (
		title, sortT, key, description string
		seriesID                       *int64
		position                       *float64
	)
	if err := d.QueryRowContext(ctx,
		`SELECT title, sort_title, match_key, description, series_id, series_position FROM books`).
		Scan(&title, &sortT, &key, &description, &seriesID, &position); err != nil {
		t.Fatal(err)
	}

	// The embedded title beats the filename the provisional pass used.
	if title != "The Fellowship of the Ring" {
		t.Errorf("title %q, want the embedded one rather than the filename", title)
	}
	if sortT != "Fellowship of the Ring" {
		t.Errorf("sort_title %q, want the article dropped", sortT)
	}
	// The grouping key carries the author, or two different books sharing a
	// title would collapse into one.
	if key != "fellowship of the ring|jrr tolkien" {
		t.Errorf("match_key %q", key)
	}
	if description != "A hobbit leaves home." {
		t.Errorf("description %q", description)
	}
	if seriesID == nil {
		t.Error("series_id not set")
	} else if names := queryStrings(t, d, `SELECT name FROM series`); len(names) != 1 ||
		names[0] != "The Lord of the Rings" {
		t.Errorf("series %v", names)
	}
	if position == nil || *position != 1 {
		t.Errorf("series_position %v, want 1", position)
	}

	var (
		language, publisher, isbn string
		coverHash                 *string
	)
	if err := d.QueryRowContext(ctx,
		`SELECT language, publisher, isbn, cover_hash FROM editions`).
		Scan(&language, &publisher, &isbn, &coverHash); err != nil {
		t.Fatal(err)
	}
	if language != "en" || publisher != "Allen and Unwin" || isbn != "9780618574986" {
		t.Errorf("edition fields: language=%q publisher=%q isbn=%q", language, publisher, isbn)
	}
	if coverHash == nil || len(*coverHash) != 64 {
		t.Fatalf("cover_hash %v, want a sha256 digest", coverHash)
	}
	if _, err := os.Stat(s.covers.Path(*coverHash, 180)); err != nil {
		t.Errorf("the 180px variant was not cached: %v", err)
	}

	// Authorship attaches to the book, narration to the edition.
	authors := queryStrings(t, d, `
		SELECT c.name FROM contributions ct JOIN contributors c ON c.id = ct.contributor_id
		 WHERE ct.role = 'author' AND ct.book_id IS NOT NULL ORDER BY ct.position`)
	if len(authors) != 1 || authors[0] != "J.R.R. Tolkien" {
		t.Errorf("authors %v", authors)
	}
	narrators := queryStrings(t, d, `
		SELECT c.name FROM contributions ct JOIN contributors c ON c.id = ct.contributor_id
		 WHERE ct.role = 'narrator' AND ct.edition_id IS NOT NULL`)
	if len(narrators) != 1 || narrators[0] != "Rob Inglis" {
		t.Errorf("narrators %v", narrators)
	}
	if sortNames := queryStrings(t, d,
		`SELECT sort_name FROM contributors WHERE name = 'J.R.R. Tolkien'`); len(sortNames) != 1 ||
		sortNames[0] != "Tolkien, J.R.R." {
		t.Errorf("sort_name %v, want the surname first", sortNames)
	}

	chapters := queryStrings(t, d, `SELECT title FROM chapters ORDER BY ordinal`)
	want := []string{"A Long-expected Party", "The Shadow of the Past", "Three is Company"}
	if strings.Join(chapters, "|") != strings.Join(want, "|") {
		t.Errorf("chapters %v, want %v", chapters, want)
	}
}

// Parsing is the expensive half of a scan, so an unchanged tree must not do it.
func TestRescanDoesNotReparse(t *testing.T) {
	s, _, sourceID, _ := scanOneEPUB(t)
	mustScan(t, s, sourceID)

	res := mustScan(t, s, sourceID)
	if res.MetadataApplied != 0 {
		t.Errorf("reparsed %d editions on a clean rescan", res.MetadataApplied)
	}
	if res.CoversStored != 0 {
		t.Errorf("re-stored %d covers on a clean rescan", res.CoversStored)
	}
}

func TestScanLeavesLockedFieldsAlone(t *testing.T) {
	s, d, sourceID, path := scanOneEPUB(t)
	mustScan(t, s, sourceID)

	ctx := context.Background()
	locked := model.FieldTitle.With(model.FieldAuthors)
	if _, err := d.ExecContext(ctx,
		`UPDATE books SET title = 'Hand Corrected', locked_fields = ?`, locked); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`UPDATE editions SET locked_fields = ?`, model.FieldCover); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE editions SET cover_hash = 'kept'`); err != nil {
		t.Fatal(err)
	}

	// Touch the file so the scan reparses and tries to write metadata again.
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	res := mustScan(t, s, sourceID)
	if res.MetadataApplied != 1 {
		t.Fatalf("metadata applied to %d editions, want the touched one reparsed", res.MetadataApplied)
	}

	var (
		title     string
		coverHash string
		publisher string
	)
	if err := d.QueryRowContext(ctx, `SELECT title FROM books`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Hand Corrected" {
		t.Errorf("title %q, want the locked value kept", title)
	}
	if err := d.QueryRowContext(ctx,
		`SELECT cover_hash, publisher FROM editions`).Scan(&coverHash, &publisher); err != nil {
		t.Fatal(err)
	}
	if coverHash != "kept" {
		t.Errorf("cover_hash %q, want the locked value kept", coverHash)
	}
	// An unlocked field on the same row is still refreshed.
	if publisher != "Allen and Unwin" {
		t.Errorf("publisher %q, want the unlocked field still written", publisher)
	}
}

func TestEditionsSharingACoverStoreItOnce(t *testing.T) {
	root := t.TempDir()
	writeFixtureEPUB(t, filepath.Join(root, "a", "one.epub"))
	writeFixtureEPUB(t, filepath.Join(root, "b", "two.epub"))

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	hashes := queryStrings(t, d, `SELECT DISTINCT cover_hash FROM editions WHERE cover_hash IS NOT NULL`)
	if len(hashes) != 1 {
		t.Errorf("got %d distinct cover hashes %v, want the identical art shared", len(hashes), hashes)
	}
	if n := countRows(t, d, "editions"); n != 2 {
		t.Errorf("%d editions, want 2", n)
	}
}

func TestScanFallsBackToThePathWhenAFileCarriesNothing(t *testing.T) {
	root := tree(t, "Tolkien/The Silmarillion.epub") // an empty file, unparseable
	s, d, sourceID := testScanner(t, root)

	res := mustScan(t, s, sourceID)
	if len(res.Warnings) == 0 {
		t.Error("an unparseable file produced no warning")
	}

	titles := queryStrings(t, d, `SELECT title FROM books`)
	if len(titles) != 1 || titles[0] != "The Silmarillion" {
		t.Errorf("titles %v, want the filename used as a fallback", titles)
	}
}

func TestScanStoresPerFileDurations(t *testing.T) {
	paths := sampleSet(t, "Information Doesn_t Want to Be Free")

	root := t.TempDir()
	dir := filepath.Join(root, "Information Doesn_t Want to Be Free")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Link rather than copy: these files are large and never enter the repo.
	for _, src := range paths {
		if err := os.Symlink(src, filepath.Join(dir, filepath.Base(src))); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "editions"); n != 1 {
		t.Fatalf("%d editions, want the folder treated as one", n)
	}

	ctx := context.Background()
	var title string
	if err := d.QueryRowContext(ctx, `SELECT title FROM books`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Information Doesn't Want to Be Free" {
		t.Errorf("title %q, want the album tag to beat the folder name", title)
	}

	var (
		parts   int
		total   float64
		minPart float64
	)
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(duration_seconds), 0), COALESCE(MIN(duration_seconds), 0)
		   FROM files WHERE duration_seconds IS NOT NULL`).Scan(&parts, &total, &minPart); err != nil {
		t.Fatal(err)
	}
	if parts != len(paths) {
		t.Errorf("%d files have durations, want all %d", parts, len(paths))
	}
	if minPart <= 0 {
		t.Errorf("shortest part is %v", minPart)
	}

	var editionDuration float64
	if err := d.QueryRowContext(ctx, `SELECT duration_seconds FROM editions`).Scan(&editionDuration); err != nil {
		t.Fatal(err)
	}
	if diff := editionDuration - total; diff > 0.01 || diff < -0.01 {
		t.Errorf("edition duration %v, want the sum of its parts %v", editionDuration, total)
	}

	// Chapter offsets must rise across parts.
	starts := queryStrings(t, d, `SELECT CAST(start_seconds AS TEXT) FROM chapters ORDER BY ordinal`)
	if len(starts) != len(paths) {
		t.Errorf("%d chapters, want one per part", len(starts))
	}
}
