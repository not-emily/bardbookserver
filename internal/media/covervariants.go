package media

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// MaxCoverWidth bounds what a client may ask for; anything larger is served
// from the full-size copy anyway.
const MaxCoverWidth = 4000

// BestVariant picks the smallest cached width that covers want, falling back
// to the full-size copy. Covers smaller than a configured width have no file
// at that width, so the list is consulted rather than assumed.
func (c *Cache) BestVariant(hash string, want int) (path string, width int, ok bool) {
	variants := c.Variants(hash)
	if len(variants) == 0 {
		return "", 0, false
	}

	if want > 0 {
		for _, w := range variants {
			if w != FullWidth && w >= want {
				return c.Path(hash, w), w, true
			}
		}
	}

	largest := variants[len(variants)-1]
	return c.Path(hash, largest), largest, true
}

// CoverETag changes with the width as well as the bytes, so a client that
// asked for a thumbnail is not handed a full-size 304.
func CoverETag(hash string, width int) string {
	return fmt.Sprintf(`"%s-%d"`, hash, width)
}

// ServeCover revalidates rather than letting a client hold a cover for a
// fixed time: the hash behind a book's cover changes when its metadata does.
func ServeCover(w http.ResponseWriter, r *http.Request, path, etag string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")

	http.ServeContent(w, r, "", info.ModTime().UTC().Truncate(time.Second), f)
	return nil
}
