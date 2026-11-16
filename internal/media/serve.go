package media

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// FileETag identifies the bytes a client holds. files.content_hash stays empty
// until something reads a whole file, so size and mtime stand in: the scanner
// restats every file on every pass, so changed bytes change the tag.
func FileETag(id, size int64, modified time.Time, contentHash string) string {
	if contentHash != "" {
		return `"` + contentHash + `"`
	}
	return fmt.Sprintf(`"%d-%d-%d"`, id, size, modified.Unix())
}

var contentTypes = map[string]string{
	"epub": "application/epub+zip",
	"pdf":  "application/pdf",
	"m4b":  "audio/mp4",
	"mp3":  "audio/mpeg",
}

func ContentType(format string) string {
	if t, ok := contentTypes[format]; ok {
		return t
	}
	return "application/octet-stream"
}

// Serve defers to http.ServeContent, which gets ranges, If-Range and
// multipart 206 responses right.
func Serve(w http.ResponseWriter, r *http.Request, path, format, etag, downloadName string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", ContentType(format))
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	if downloadName != "" {
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`inline; filename="%s"`, downloadName))
	}

	// The name is only used for type sniffing, which the explicit header above
	// has already settled.
	http.ServeContent(w, r, "", info.ModTime(), f)
	return nil
}

// DownloadName is built from the book's title rather than the file's name, so
// a response never carries anything about the library's layout.
func DownloadName(title, format string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', '"', '\r', '\n', 0:
			return '-'
		}
		return r
	}, title)

	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}
	return cleaned + "." + format
}
