// Package media caches cover art on disk.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"golang.org/x/image/draw"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// Widths clients ask for: a grid thumbnail and a detail view. Anything larger
// is served from the full-size copy.
var Widths = []int{180, 480}

const jpegQuality = 82

// FullWidth names the native-size copy in Path and Variants.
const FullWidth = 0

// Cache is a directory of covers keyed by the hash of the source bytes. It
// holds no index: the file layout is the index, so a lost database costs
// nothing but a rescan.
type Cache struct {
	dir string
}

func NewCache(dir string) *Cache { return &Cache{dir: dir} }

// Store writes the full-size cover and every variant smaller than it, and
// returns the hash to record on the edition. Identical bytes are stored once,
// so re-running a scan is free.
func (c *Cache) Store(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("cover is empty")
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	dir := c.dirFor(hash)
	full := filepath.Join(dir, "full.jpg")
	if _, err := os.Stat(full); err == nil {
		return hash, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode cover: %w", err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create cover dir: %w", err)
	}
	if err := writeJPEG(full, img); err != nil {
		return "", err
	}

	// Only ever downscale: enlarging a small cover makes it worse and costs
	// disk to do it.
	srcWidth := img.Bounds().Dx()
	for _, w := range Widths {
		if w >= srcWidth {
			continue
		}
		if err := writeJPEG(filepath.Join(dir, strconv.Itoa(w)+".jpg"), resize(img, w)); err != nil {
			return "", err
		}
	}
	return hash, nil
}

// Path locates a cached file. FullWidth asks for the native-size copy.
func (c *Cache) Path(hash string, width int) string {
	name := "full.jpg"
	if width != FullWidth {
		name = strconv.Itoa(width) + ".jpg"
	}
	return filepath.Join(c.dirFor(hash), name)
}

// Variants reports the widths actually cached, ascending, with FullWidth last.
// A cover smaller than a variant has no file at that width.
func (c *Cache) Variants(hash string) []int {
	var out []int
	for _, w := range Widths {
		if _, err := os.Stat(c.Path(hash, w)); err == nil {
			out = append(out, w)
		}
	}
	sort.Ints(out)
	if _, err := os.Stat(c.Path(hash, FullWidth)); err == nil {
		out = append(out, FullWidth)
	}
	return out
}

// dirFor shards on the first byte of the hash so no directory ends up with
// thousands of entries.
func (c *Cache) dirFor(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(c.dir, "bad", hash)
	}
	return filepath.Join(c.dir, hash[:2], hash)
}

func resize(src image.Image, width int) image.Image {
	bounds := src.Bounds()
	height := bounds.Dy() * width / bounds.Dx()
	if height < 1 {
		height = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	return dst
}

func writeJPEG(path string, img image.Image) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return f.Close()
}
