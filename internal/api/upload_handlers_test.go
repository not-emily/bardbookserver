package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/not-emily/bardbookserver/internal/upload"
)

// fixtureEPUB writes the mimetype entry first and uncompressed, as the spec
// requires and as the uploader's sniffer insists.
func fixtureEPUB(t *testing.T) []byte {
	t.Helper()

	var out bytes.Buffer
	zw := zip.NewWriter(&out)

	mimetype, err := zw.CreateHeader(&zip.FileHeader{
		Name: "mimetype", Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mimetype.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}

	entries := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>The Fellowship of the Ring</dc:title>
    <dc:creator id="a1">J.R.R. Tolkien</dc:creator>
    <meta property="role" refines="#a1">aut</meta>
    <dc:language>en</dc:language>
  </metadata>
  <manifest/><spine/>
</package>`,
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	// Stored rather than deflated, so the file is large enough to span several
	// chunks and the resume path has something to resume.
	padding, err := zw.CreateHeader(&zip.FileHeader{
		Name: "OEBPS/pad.bin", Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	filler := make([]byte, 20000)
	for i := range filler {
		filler[i] = byte(i % 251)
	}
	if _, err := padding.Write(filler); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// smallChunks makes a test fixture span several chunks without pretending a
// production chunk is kilobytes.
func (h *harness) smallChunks(chunkBytes int64) {
	h.t.Helper()
	h.server.uploads = upload.NewStore(h.db.DB, upload.Limits{
		MaxBytes: h.cfg.Upload.MaxBytes(), ChunkBytes: chunkBytes,
	})
	h.handler = h.server.handler()
}

func (h *harness) createUpload(token string, sourceID int64, name string, body []byte) uploadDTO {
	h.t.Helper()

	sum := sha256.Sum256(body)
	res := h.sendJSON(http.MethodPost, "/api/v1/uploads", token, map[string]any{
		"source_id":   sourceID,
		"filename":    name,
		"format":      "epub",
		"total_bytes": len(body),
		"sha256":      hex.EncodeToString(sum[:]),
	})
	if res.Code != http.StatusCreated {
		h.t.Fatalf("create upload returned %d: %s", res.Code, res.Body)
	}

	var dto uploadDTO
	h.decode(res, &dto)
	return dto
}

func (h *harness) putChunk(token, id string, offset int64, chunk []byte) *httptest.ResponseRecorder {
	h.t.Helper()

	r := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/v1/uploads/%s/chunks/%d", id, offset), bytes.NewReader(chunk))
	r.Header.Set("Authorization", "Bearer "+token)
	return h.serve(r)
}

func TestAnUploadedEPUBLandsInTheLibrary(t *testing.T) {
	h := newHarness(t)
	h.smallChunks(4096)
	root := t.TempDir()
	sourceID := h.addSource("Books", root, true)
	body := fixtureEPUB(t)

	session := h.createUpload(h.token, sourceID, "fellowship.epub", body)
	if session.ChunkBytes < 1 {
		t.Fatalf("session reports chunk_bytes %d", session.ChunkBytes)
	}

	for offset := int64(0); offset < int64(len(body)); offset += session.ChunkBytes {
		end := min(offset+session.ChunkBytes, int64(len(body)))
		if res := h.putChunk(h.token, session.ID, offset, body[offset:end]); res.Code != http.StatusOK {
			t.Fatalf("chunk at %d returned %d: %s", offset, res.Code, res.Body)
		}
	}

	res := h.sendJSON(http.MethodPost,
		"/api/v1/uploads/"+session.ID+"/finalize", h.token, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("finalize returned %d: %s", res.Code, res.Body)
	}

	// Filed into the convention, not left where it was assembled.
	var landed string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ".epub" {
			landed = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if landed == "" {
		t.Fatal("no epub was filed into the source")
	}
	if rel, _ := filepath.Rel(root, landed); filepath.Dir(rel) == "." {
		t.Errorf("the upload landed at the source root as %q, not under an author", rel)
	}
	if filepath.Base(filepath.Dir(landed)) == upload.IngestDir {
		t.Error("the upload was left in the ingest directory")
	}

	// A scan was queued, which is what indexes it.
	if n := h.oneValue(
		`SELECT COUNT(*) FROM jobs WHERE kind = ?`, "scan.source"); n != 1 {
		t.Errorf("%d scans were queued, want 1", n)
	}
}

func TestResumingAnUploadOverTheAPI(t *testing.T) {
	h := newHarness(t)
	h.smallChunks(4096)
	sourceID := h.addSource("Books", t.TempDir(), true)
	body := fixtureEPUB(t)

	session := h.createUpload(h.token, sourceID, "fellowship.epub", body)
	if res := h.putChunk(h.token, session.ID, 0, body[:session.ChunkBytes]); res.Code != http.StatusOK {
		t.Fatalf("first chunk returned %d", res.Code)
	}

	var status uploadDTO
	h.getJSONAs("/api/v1/uploads/"+session.ID, h.token, &status)
	if len(status.Missing) == 0 {
		t.Fatal("nothing is reported missing after one chunk of several")
	}
	for _, ordinal := range status.Missing {
		if ordinal == 0 {
			t.Error("the delivered chunk is reported missing")
		}
	}

	res := h.sendJSON(http.MethodPost,
		"/api/v1/uploads/"+session.ID+"/finalize", h.token, nil)
	if res.Code != http.StatusConflict {
		t.Errorf("finalizing an incomplete upload returned %d, want 409", res.Code)
	}
}

func TestAnUploadIsRefusedForANonUploader(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)
	_, reader := h.addUser("reader", false)

	res := h.sendJSON(http.MethodPost, "/api/v1/uploads", reader, map[string]any{
		"source_id": sourceID, "filename": "x.epub", "format": "epub",
		"total_bytes": 1024,
		"sha256":      "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if res.Code != http.StatusForbidden {
		t.Fatalf("a non-uploader got %d, want 403", res.Code)
	}
	if !bytes.Contains(res.Body.Bytes(), []byte("upload_not_allowed")) {
		t.Errorf("the denial does not say why: %s", res.Body)
	}
}

// can_upload is per account from the start, so opening it up is a default
// change rather than new plumbing.
func TestCanUploadLetsANonAdminUpload(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)
	userID, token := h.addUser("helper", false)
	h.exec(`UPDATE users SET can_upload = 1 WHERE id = ?`, userID)

	body := fixtureEPUB(t)
	sum := sha256.Sum256(body)
	res := h.sendJSON(http.MethodPost, "/api/v1/uploads", token, map[string]any{
		"source_id": sourceID, "filename": "x.epub", "format": "epub",
		"total_bytes": len(body), "sha256": hex.EncodeToString(sum[:]),
	})
	if res.Code != http.StatusCreated {
		t.Errorf("an account with can_upload got %d: %s", res.Code, res.Body)
	}
}

func TestAnotherAccountsUploadIsAbsent(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)
	session := h.createUpload(h.token, sourceID, "fellowship.epub", fixtureEPUB(t))

	otherID, other := h.addUser("other", false)
	h.exec(`UPDATE users SET can_upload = 1 WHERE id = ?`, otherID)

	res := h.getAs("/api/v1/uploads/"+session.ID, other)
	if res.Code != http.StatusNotFound {
		t.Errorf("another account's upload returned %d, want 404", res.Code)
	}
}

func TestADeclaredFormatMustMatchTheContents(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)

	body := []byte("%PDF-1.7 this is not an epub")
	session := h.createUpload(h.token, sourceID, "lying.epub", body)

	res := h.putChunk(h.token, session.ID, 0, body)
	if res.Code != http.StatusBadRequest {
		t.Errorf("a mismatched format returned %d, want 400", res.Code)
	}
}

func TestOversizeIsRejectedAtCreation(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)

	res := h.sendJSON(http.MethodPost, "/api/v1/uploads", h.token, map[string]any{
		"source_id": sourceID, "filename": "huge.m4b", "format": "m4b",
		"total_bytes": h.cfg.Upload.MaxBytes() + 1,
		"sha256":      "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversize upload returned %d, want 413", res.Code)
	}
}

func TestAnUnwritableSourceFailsBeforeAnyBytes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test sets")
	}
	h := newHarness(t)

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	sourceID := h.addSource("Locked", locked, true)

	res := h.sendJSON(http.MethodPost, "/api/v1/uploads", h.token, map[string]any{
		"source_id": sourceID, "filename": "x.epub", "format": "epub",
		"total_bytes": 1024,
		"sha256":      "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if res.Code != http.StatusConflict {
		t.Errorf("an unwritable source returned %d, want 409", res.Code)
	}
}

func TestUploadRejectsNonsenseAtCreation(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)
	good := map[string]any{
		"source_id": sourceID, "filename": "x.epub", "format": "epub",
		"total_bytes": 1024,
		"sha256":      "0000000000000000000000000000000000000000000000000000000000000000",
	}

	cases := map[string]func(m map[string]any){
		"no source":      func(m map[string]any) { m["source_id"] = 0 },
		"missing source": func(m map[string]any) { m["source_id"] = 9999 },
		"no filename":    func(m map[string]any) { m["filename"] = "  " },
		"odd format":     func(m map[string]any) { m["format"] = "cbz" },
		"short hash":     func(m map[string]any) { m["sha256"] = "abc" },
		"unhex hash": func(m map[string]any) {
			m["sha256"] = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
		},
		"no size": func(m map[string]any) { m["total_bytes"] = 0 },
	}
	for name, mangle := range cases {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{}
			for k, v := range good {
				body[k] = v
			}
			mangle(body)

			res := h.sendJSON(http.MethodPost, "/api/v1/uploads", h.token, body)
			if res.Code == http.StatusCreated {
				t.Errorf("%s was accepted", name)
			}
		})
	}
}
