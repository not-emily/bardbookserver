package embedded

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePDF assembles a minimal valid PDF around the given objects, computing
// the cross-reference offsets that pdfcpu insists on.
func writePDF(t *testing.T, objects []string, trailer string) string {
	t.Helper()

	var (
		body    strings.Builder
		offsets = make([]int, len(objects))
	)
	body.WriteString("%PDF-1.4\n")
	for i, obj := range objects {
		offsets[i] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}

	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&body, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d %s >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, trailer, xref)

	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func twoPagePDF(t *testing.T) string {
	t.Helper()
	return writePDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
		"<< /Title (Dune) /Author (Frank Herbert) /Subject (A desert planet.) " +
			"/CreationDate (D:19650801120000Z) >>",
	}, "/Root 1 0 R /Info 5 0 R")
}

func TestParsePDF(t *testing.T) {
	md, err := ParsePDF(twoPagePDF(t))
	if err != nil {
		t.Fatal(err)
	}

	if md.Title != "Dune" {
		t.Errorf("title %q, want Dune", md.Title)
	}
	if want := []string{"Frank Herbert"}; !equal(md.Authors, want) {
		t.Errorf("authors %v, want %v", md.Authors, want)
	}
	if md.PageCount == nil || *md.PageCount != 2 {
		t.Errorf("page count %v, want 2", md.PageCount)
	}
	if md.PublishedOn != "1965-08-01" {
		t.Errorf("published_on %q, want the PDF D: date converted", md.PublishedOn)
	}
	if md.Description != "A desert planet." {
		t.Errorf("description %q, want the Subject field", md.Description)
	}
	// Pure Go cannot rasterise a page, so a PDF with no embedded image has no
	// cover rather than a blank one.
	if md.Cover != nil {
		t.Errorf("cover %+v, want none for a vector-only page", md.Cover)
	}
}

func TestParsePDFWithoutInfoDictionary(t *testing.T) {
	path := writePDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
	}, "/Root 1 0 R")

	md, err := ParsePDF(path)
	if err != nil {
		t.Fatalf("a PDF with no Info dictionary failed to parse: %v", err)
	}
	if md.Title != "" || len(md.Authors) != 0 {
		t.Errorf("got title %q authors %v, want both unknown", md.Title, md.Authors)
	}
	if md.PageCount == nil || *md.PageCount != 1 {
		t.Errorf("page count %v, want 1", md.PageCount)
	}
}

func TestParsePDFRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.pdf")
	if err := os.WriteFile(path, []byte("this is not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePDF(path); err == nil {
		t.Error("garbage parsed as a PDF")
	}
}

func TestPDFDate(t *testing.T) {
	cases := map[string]string{
		"D:19650801120000Z": "1965-08-01",
		"D:1965":            "1965",
		"19650801":          "1965-08-01",
		"":                  "",
		"garbage":           "",
	}
	for in, want := range cases {
		if got := pdfDate(in); got != want {
			t.Errorf("pdfDate(%q) = %q, want %q", in, got, want)
		}
	}
}
