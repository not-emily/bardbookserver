package embedded

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const containerXML = `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

// writeEPUB builds a fixture in a temp dir. Real books never enter the repo,
// so every committed fixture is generated here.
func writeEPUB(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.epub")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseEPUB3(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>The Fellowship of the Ring</dc:title>
    <dc:title>Being the First Part of The Lord of the Rings</dc:title>
    <dc:creator id="a1">J.R.R. Tolkien</dc:creator>
    <meta property="role" refines="#a1" scheme="marc:relators">aut</meta>
    <dc:contributor id="n1">Rob Inglis</dc:contributor>
    <meta property="role" refines="#n1" scheme="marc:relators">nrt</meta>
    <dc:contributor id="x1">Some Typesetter</dc:contributor>
    <dc:language>en-GB</dc:language>
    <dc:publisher>Allen &amp; Unwin</dc:publisher>
    <dc:date>1954-07-29T00:00:00Z</dc:date>
    <dc:description>A hobbit leaves home.</dc:description>
    <dc:subject>Fantasy fiction</dc:subject>
    <dc:subject>Quests</dc:subject>
    <dc:identifier>urn:isbn:978-0-618-57498-6</dc:identifier>
    <meta property="belongs-to-collection" id="c1">The Lord of the Rings</meta>
    <meta property="group-position" refines="#c1">1</meta>
  </metadata>
  <manifest>
    <item id="nav" href="toc.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="cov" href="images/cover.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine/>
</package>`

	nav := `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <body>
    <nav epub:type="landmarks"><ol><li><a href="start.xhtml">Start</a></li></ol></nav>
    <nav epub:type="toc">
      <ol>
        <li><a href="ch1.xhtml">A Long-expected Party</a></li>
        <li><a href="ch2.xhtml">The Shadow of the&#160;Past</a>
          <ol><li><a href="ch2.xhtml#s1">Part the Second</a></li></ol>
        </li>
      </ol>
    </nav>
  </body>
</html>`

	path := writeEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      opf,
		"OEBPS/toc.xhtml":        nav,
		"OEBPS/images/cover.jpg": "JPEGBYTES",
	})

	md, err := ParseEPUB(path)
	if err != nil {
		t.Fatal(err)
	}

	if md.Title != "The Fellowship of the Ring" {
		t.Errorf("title %q", md.Title)
	}
	if md.Subtitle != "Being the First Part of The Lord of the Rings" {
		t.Errorf("subtitle %q", md.Subtitle)
	}
	if want := []string{"J.R.R. Tolkien"}; !equal(md.Authors, want) {
		t.Errorf("authors %v, want %v", md.Authors, want)
	}
	// The unroled dc:contributor is dropped; only the nrt one counts.
	if want := []string{"Rob Inglis"}; !equal(md.Narrators, want) {
		t.Errorf("narrators %v, want %v", md.Narrators, want)
	}
	if md.Language != "en" {
		t.Errorf("language %q, want the primary subtag of en-GB", md.Language)
	}
	if md.Publisher != "Allen & Unwin" {
		t.Errorf("publisher %q", md.Publisher)
	}
	if md.PublishedOn != "1954-07-29" {
		t.Errorf("published_on %q, want the timestamp reduced to a date", md.PublishedOn)
	}
	if md.ISBN != "9780618574986" {
		t.Errorf("isbn %q, want the urn and hyphens stripped", md.ISBN)
	}
	if md.Series != "The Lord of the Rings" {
		t.Errorf("series %q", md.Series)
	}
	if md.SeriesPosition == nil || *md.SeriesPosition != 1 {
		t.Errorf("series position %v, want 1", md.SeriesPosition)
	}
	if len(md.Subjects) != 2 {
		t.Errorf("subjects %v, want 2", md.Subjects)
	}
	if md.Cover == nil || string(md.Cover.Data) != "JPEGBYTES" || md.Cover.MIME != "image/jpeg" {
		t.Errorf("cover %+v, want the cover-image manifest item", md.Cover)
	}

	// The landmarks nav is skipped, the toc nav flattened in document order.
	wantChapters := []string{"A Long-expected Party", "The Shadow of the Past", "Part the Second"}
	if len(md.Chapters) != len(wantChapters) {
		t.Fatalf("got %d chapters %+v, want %d", len(md.Chapters), md.Chapters, len(wantChapters))
	}
	for i, want := range wantChapters {
		if md.Chapters[i].Title != want {
			t.Errorf("chapter %d title %q, want %q", i, md.Chapters[i].Title, want)
		}
	}
	if md.Chapters[0].Href != "ch1.xhtml" {
		t.Errorf("chapter href %q", md.Chapters[0].Href)
	}
}

func TestParseEPUB2(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/"
         xmlns:opf="http://www.idpf.org/2007/opf" version="2.0">
  <metadata>
    <dc:title>Dune</dc:title>
    <dc:creator opf:role="aut" opf:file-as="Herbert, Frank">Frank Herbert</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier opf:scheme="ISBN">0-441-17271-7</dc:identifier>
    <meta name="cover" content="cover-img"/>
    <meta name="calibre:series" content="Dune Chronicles"/>
    <meta name="calibre:series_index" content="1.5"/>
  </metadata>
  <manifest>
    <item id="cover-img" href="cover.jpeg" media-type="image/jpeg"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="ncx"/>
</package>`

	ncx := `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="np1" playOrder="1">
      <navLabel><text>Book One: Dune</text></navLabel>
      <content src="part1.html"/>
      <navPoint id="np1a" playOrder="2">
        <navLabel><text>Chapter 1</text></navLabel>
        <content src="part1.html#c1"/>
      </navPoint>
    </navPoint>
    <navPoint id="np2" playOrder="3">
      <navLabel><text>Appendix</text></navLabel>
      <content src="appendix.html"/>
    </navPoint>
  </navMap>
</ncx>`

	path := writeEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      opf,
		"OEBPS/toc.ncx":          ncx,
		"OEBPS/cover.jpeg":       "COVER2",
	})

	md, err := ParseEPUB(path)
	if err != nil {
		t.Fatal(err)
	}

	if md.Title != "Dune" {
		t.Errorf("title %q", md.Title)
	}
	if want := []string{"Frank Herbert"}; !equal(md.Authors, want) {
		t.Errorf("authors %v, want %v from the opf:role attribute", md.Authors, want)
	}
	if md.ISBN != "0441172717" {
		t.Errorf("isbn %q, want the scheme attribute honoured", md.ISBN)
	}
	if md.Series != "Dune Chronicles" {
		t.Errorf("series %q", md.Series)
	}
	if md.SeriesPosition == nil || *md.SeriesPosition != 1.5 {
		t.Errorf("series position %v, want 1.5", md.SeriesPosition)
	}
	if md.Cover == nil || string(md.Cover.Data) != "COVER2" {
		t.Errorf("cover %+v, want the meta name=cover indirection followed", md.Cover)
	}

	wantChapters := []string{"Book One: Dune", "Chapter 1", "Appendix"}
	if len(md.Chapters) != len(wantChapters) {
		t.Fatalf("got %d chapters %+v, want %d", len(md.Chapters), md.Chapters, len(wantChapters))
	}
	for i, want := range wantChapters {
		if md.Chapters[i].Title != want {
			t.Errorf("chapter %d title %q, want %q", i, md.Chapters[i].Title, want)
		}
	}
}

func TestEPUBRejectsIdentifiersThatAreNotISBNs(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>Anne of Green Gables</dc:title>
    <dc:identifier id="id">http://www.gutenberg.org/45</dc:identifier>
  </metadata>
  <manifest/><spine/>
</package>`

	md, err := ParseEPUB(writeEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      opf,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if md.ISBN != "" {
		t.Errorf("isbn %q, want a Gutenberg URL rejected", md.ISBN)
	}
}

func TestEPUBWithoutAContainerFails(t *testing.T) {
	path := writeEPUB(t, map[string]string{"OEBPS/content.opf": "<package/>"})
	if _, err := ParseEPUB(path); err == nil {
		t.Error("an epub with no container.xml parsed successfully")
	}
}

func TestEPUBMissingCoverEntryIsNotAnError(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata><dc:title>Ghost Cover</dc:title></metadata>
  <manifest>
    <item id="cov" href="missing.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine/>
</package>`

	md, err := ParseEPUB(writeEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      opf,
	}))
	if err != nil {
		t.Fatalf("a manifest pointing at a missing file failed the parse: %v", err)
	}
	if md.Cover != nil {
		t.Error("cover reported for a missing entry")
	}
	if md.Title != "Ghost Cover" {
		t.Errorf("title %q, want parsing to continue past the bad cover", md.Title)
	}
}

func TestEPUBPercentEncodedHrefResolves(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata><dc:title>Spaced</dc:title></metadata>
  <manifest>
    <item id="cov" href="my%20cover.jpg" media-type="image/jpeg" properties="cover-image"/>
  </manifest>
  <spine/>
</package>`

	md, err := ParseEPUB(writeEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      opf,
		"OEBPS/my cover.jpg":     "SPACED",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if md.Cover == nil || string(md.Cover.Data) != "SPACED" {
		t.Errorf("cover %+v, want the percent-encoded href resolved", md.Cover)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeISBN(t *testing.T) {
	cases := map[string]string{
		"978-0-618-57498-6":           "9780618574986",
		"0-441-17271-7":               "0441172717",
		"043942089X":                  "043942089X",
		"http://www.gutenberg.org/45": "",
		"":                            "",
		"12345":                       "",
		"X441172717":                  "",
	}
	for in, want := range cases {
		if got := normalizeISBN(in); got != want {
			t.Errorf("normalizeISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeLanguageAndDate(t *testing.T) {
	langs := map[string]string{
		"en-GB": "en", "EN": "en", "eng": "eng", "": "", "english": "",
		"XXX": "", "und": "", "zxx": "",
	}
	for in, want := range langs {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
	dates := map[string]string{
		"1954-07-29T00:00:00Z": "1954-07-29",
		"1937":                 "1937",
		"not a date":           "",
	}
	for in, want := range dates {
		if got := normalizeDate(in); got != want {
			t.Errorf("normalizeDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRealEPUBsParse(t *testing.T) {
	// Gutenberg files carry both nav flavours and no ISBN, which is why they
	// are worth running against when they happen to be on the machine.
	cases := []struct {
		file, title, author string
		minChapters         int
	}{
		{"pg45-images-3.epub", "Anne of Green Gables", "L. M. Montgomery", 40},
		{"pg1184-images-3.epub", "The Count of Monte Cristo", "Alexandre Dumas", 100},
	}
	for _, c := range cases {
		md, err := ParseEPUB(sample(t, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if md.Title != c.title {
			t.Errorf("%s title %q, want %q", c.file, md.Title, c.title)
		}
		if len(md.Authors) == 0 || md.Authors[0] != c.author {
			t.Errorf("%s authors %v, want %q first", c.file, md.Authors, c.author)
		}
		if len(md.Chapters) < c.minChapters {
			t.Errorf("%s got %d chapters, want at least %d", c.file, len(md.Chapters), c.minChapters)
		}
		if md.Cover == nil || !strings.HasPrefix(md.Cover.MIME, "image/") {
			t.Errorf("%s cover %+v", c.file, md.Cover)
		}
		if md.ISBN != "" {
			t.Errorf("%s isbn %q, want none for a Gutenberg file", c.file, md.ISBN)
		}
	}
}
