package api

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func audiobookBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i % 251)
	}
	return out
}

func (h *harness) getRange(path, rangeHeader string) *httptest.ResponseRecorder {
	h.t.Helper()
	r := h.authedRequest(path)
	r.Header.Set("Range", rangeHeader)
	return h.serve(r)
}

func TestFullRequestReturnsTheWholeFile(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	content := audiobookBytes(4096)
	fileID := h.addRealFile(lib.duneM4B, "dune.m4b", content)

	res := h.get(fmt.Sprintf("/api/v1/files/%d/content", fileID))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	if !bytes.Equal(res.Body.Bytes(), content) {
		t.Errorf("got %d bytes, want %d", res.Body.Len(), len(content))
	}
	if got := res.Header().Get("Content-Type"); got != "audio/mp4" {
		t.Errorf("content-type = %q, want audio/mp4", got)
	}
	if got := res.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("accept-ranges = %q, want bytes", got)
	}
	if res.Header().Get("ETag") == "" {
		t.Error("no ETag, so a client cannot revalidate a cached download")
	}
	if got := res.Header().Get("Content-Disposition"); got != `inline; filename="Dune.m4b"` {
		t.Errorf("content-disposition = %q", got)
	}
}

// The scanner stores file mtimes as RFC3339Nano. Parsing them as
// datetime('now') yielded the zero time, which silently cost the ETag its
// mtime component — so a file edited in place at the same size kept its tag.
func TestFileETagCarriesTheModifiedTime(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	content := audiobookBytes(512)
	fileID := h.addRealFile(lib.duneM4B, "dune.m4b", content)

	modified, err := time.Parse(time.RFC3339Nano, fileStamp)
	if err != nil {
		t.Fatal(err)
	}

	want := fmt.Sprintf(`"%d-%d-%d"`, fileID, len(content), modified.Unix())
	if got := h.get(fmt.Sprintf("/api/v1/files/%d/content", fileID)).Header().Get("ETag"); got != want {
		t.Errorf("etag = %s, want %s", got, want)
	}
}

func TestRangeRequestReturns206WithTheRightBytes(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	content := audiobookBytes(4096)
	fileID := h.addRealFile(lib.duneM4B, "dune.m4b", content)
	path := fmt.Sprintf("/api/v1/files/%d/content", fileID)

	tests := []struct {
		header    string
		wantStart int
		wantEnd   int
	}{
		{"bytes=0-99", 0, 99},
		{"bytes=100-199", 100, 199},
		{"bytes=4000-", 4000, 4095},
		{"bytes=-96", 4000, 4095},
	}

	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			res := h.getRange(path, tc.header)
			if res.Code != http.StatusPartialContent {
				t.Fatalf("status = %d, want 206", res.Code)
			}

			wantRange := fmt.Sprintf("bytes %d-%d/%d", tc.wantStart, tc.wantEnd, len(content))
			if got := res.Header().Get("Content-Range"); got != wantRange {
				t.Errorf("content-range = %q, want %q", got, wantRange)
			}

			want := content[tc.wantStart : tc.wantEnd+1]
			if !bytes.Equal(res.Body.Bytes(), want) {
				t.Errorf("body = %d bytes, want %d", res.Body.Len(), len(want))
			}
			if got := res.Header().Get("Content-Length"); got != fmt.Sprint(len(want)) {
				t.Errorf("content-length = %q, want %d", got, len(want))
			}
		})
	}
}

func TestMultiRangeRequestIsServed(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	fileID := h.addRealFile(lib.duneM4B, "dune.m4b", audiobookBytes(4096))

	res := h.getRange(fmt.Sprintf("/api/v1/files/%d/content", fileID), "bytes=0-9,100-109")
	if res.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", res.Code)
	}
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "multipart/byteranges") {
		t.Errorf("content-type = %q, want multipart/byteranges", got)
	}
}

func TestUnsatisfiableRangeIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	fileID := h.addRealFile(lib.duneM4B, "dune.m4b", audiobookBytes(100))

	res := h.getRange(fmt.Sprintf("/api/v1/files/%d/content", fileID), "bytes=500-600")
	if res.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("status = %d, want 416", res.Code)
	}
}

func TestIfNoneMatchOnAFileReturns304(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	fileID := h.addRealFile(lib.duneM4B, "dune.m4b", audiobookBytes(1024))
	path := fmt.Sprintf("/api/v1/files/%d/content", fileID)

	etag := h.get(path).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag to revalidate with")
	}

	r := h.authedRequest(path)
	r.Header.Set("If-None-Match", etag)
	res := h.serve(r)

	if res.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", res.Code)
	}
	if res.Body.Len() != 0 {
		t.Errorf("a 304 carried %d bytes", res.Body.Len())
	}
}

// A file id is the easiest thing for a client to guess, so the scope has to
// cover it as thoroughly as it covers a listing.
func TestFileOfAHiddenBookIsNotServed(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	res := h.get(fmt.Sprintf("/api/v1/files/%d/content", lib.retiredFile))
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

func TestMissingFileOnDiskIs404(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	res := h.get(fmt.Sprintf("/api/v1/files/%d/content", lib.duneEPUBFile))
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an indexed file that is gone", res.Code)
	}
}

func TestCoverServesTheSmallestVariantThatFits(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.storeCover(lib.duneEPUB, 600, 900)

	tests := []struct {
		query     string
		wantWidth int
	}{
		{"", 600},
		{"?width=180", 180},
		{"?width=100", 180},
		{"?width=200", 480},
		{"?width=480", 480},
		{"?width=1000", 600},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			res := h.get(fmt.Sprintf("/api/v1/books/%d/cover%s", lib.dune, tc.query))
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
			}
			if got := res.Header().Get("Content-Type"); got != "image/jpeg" {
				t.Errorf("content-type = %q, want image/jpeg", got)
			}

			img, err := jpeg.Decode(bytes.NewReader(res.Body.Bytes()))
			if err != nil {
				t.Fatalf("decode served cover: %v", err)
			}
			if got := img.Bounds().Dx(); got != tc.wantWidth {
				t.Errorf("served width %d, want %d", got, tc.wantWidth)
			}
		})
	}
}

func TestIfNoneMatchOnACoverReturns304(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.storeCover(lib.duneEPUB, 600, 900)

	path := fmt.Sprintf("/api/v1/books/%d/cover?width=180", lib.dune)
	etag := h.get(path).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a cover")
	}

	r := h.authedRequest(path)
	r.Header.Set("If-None-Match", etag)
	if res := h.serve(r); res.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", res.Code)
	}
}

// A thumbnail and a full-size cover share a hash, so the width has to be part
// of the tag or one 304s against the other.
func TestCoverETagVariesWithWidth(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.storeCover(lib.duneEPUB, 600, 900)

	thumb := h.get(fmt.Sprintf("/api/v1/books/%d/cover?width=180", lib.dune)).Header().Get("ETag")
	full := h.get(fmt.Sprintf("/api/v1/books/%d/cover", lib.dune)).Header().Get("ETag")

	if thumb == full {
		t.Errorf("both widths share the ETag %s", thumb)
	}

	r := h.authedRequest(fmt.Sprintf("/api/v1/books/%d/cover", lib.dune))
	r.Header.Set("If-None-Match", thumb)
	if res := h.serve(r); res.Code != http.StatusOK {
		t.Errorf("the full-size cover 304'd against a thumbnail tag: %d", res.Code)
	}
}

func TestCoverWithoutOneIs404(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	// No edition of this book records a cover at all.
	if res := h.get(fmt.Sprintf("/api/v1/books/%d/cover", lib.ancillary)); res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}

	// Dune records a hash the cache has no files for.
	if res := h.get(fmt.Sprintf("/api/v1/books/%d/cover", lib.dune)); res.Code != http.StatusNotFound {
		t.Errorf("uncached cover: status = %d, want 404", res.Code)
	}
}

func TestCoverOfAHiddenBookIsNotServed(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	retiredEdition := h.oneValue(
		`SELECT id FROM editions WHERE book_id = ?`, lib.retired)
	h.storeCover(retiredEdition, 600, 900)

	if res := h.get(fmt.Sprintf("/api/v1/books/%d/cover", lib.retired)); res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}
}

func TestBadCoverWidthIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	for _, query := range []string{"?width=0", "?width=-5", "?width=99999", "?width=wide"} {
		res := h.get(fmt.Sprintf("/api/v1/books/%d/cover%s", lib.dune, query))
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, res.Code)
		}
	}
}

func TestUnauthenticatedMediaIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	paths := []string{
		fmt.Sprintf("/api/v1/files/%d/content", lib.duneEPUBFile),
		fmt.Sprintf("/api/v1/books/%d/cover", lib.dune),
	}

	for _, path := range paths {
		if res := h.getAnonymous(path); res.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, res.Code)
		}
	}
}
