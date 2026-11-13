package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeSize(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Errorf("%s is %s, want every cached variant to be jpeg", path, format)
	}
	return cfg.Width, cfg.Height
}

func TestStoreWritesFullSizeAndSmallerVariants(t *testing.T) {
	cache := NewCache(t.TempDir())

	hash, err := cache.Store(jpegBytes(t, 1000, 1500))
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Errorf("hash %q, want a sha256 hex digest", hash)
	}

	if w, h := decodeSize(t, cache.Path(hash, FullWidth)); w != 1000 || h != 1500 {
		t.Errorf("full size is %dx%d, want 1000x1500", w, h)
	}
	for _, want := range Widths {
		w, h := decodeSize(t, cache.Path(hash, want))
		if w != want {
			t.Errorf("variant %d is %d wide", want, w)
		}
		if ratio := float64(h) / float64(w); ratio < 1.45 || ratio > 1.55 {
			t.Errorf("variant %d is %dx%d, which does not keep the 2:3 aspect", want, w, h)
		}
	}

	if got := cache.Variants(hash); len(got) != len(Widths)+1 {
		t.Errorf("variants %v, want every width plus the full size", got)
	}
}

// Enlarging a small cover makes it worse, so a source narrower than a variant
// simply has no file at that width.
func TestStoreNeverUpscales(t *testing.T) {
	cache := NewCache(t.TempDir())

	hash, err := cache.Store(jpegBytes(t, 200, 300))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(cache.Path(hash, 180)); err != nil {
		t.Errorf("the 180 variant is missing for a 200px source: %v", err)
	}
	if _, err := os.Stat(cache.Path(hash, 480)); err == nil {
		t.Error("a 480 variant was written for a 200px source")
	}
	if got := cache.Variants(hash); len(got) != 2 || got[0] != 180 || got[1] != FullWidth {
		t.Errorf("variants %v, want [180 full]", got)
	}
}

func TestStoreIsContentAddressedAndIdempotent(t *testing.T) {
	cache := NewCache(t.TempDir())
	data := jpegBytes(t, 600, 900)

	first, err := cache.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cache.Path(first, 180))
	if err != nil {
		t.Fatal(err)
	}

	second, err := cache.Store(data)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("same bytes hashed to %q then %q", first, second)
	}

	// A re-scan must not rewrite what is already cached.
	again, err := os.Stat(cache.Path(first, 180))
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(info.ModTime()) {
		t.Error("storing identical bytes rewrote the cached variant")
	}

	other, err := cache.Store(jpegBytes(t, 601, 900))
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("different covers collided")
	}
}

func TestStoreAcceptsPNG(t *testing.T) {
	cache := NewCache(t.TempDir())

	hash, err := cache.Store(pngBytes(t, 800, 1200))
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := decodeSize(t, cache.Path(hash, 480)); w != 480 {
		t.Errorf("png variant is %d wide, want 480", w)
	}
}

func TestStoreRejectsWhatIsNotAnImage(t *testing.T) {
	cache := NewCache(t.TempDir())

	if _, err := cache.Store([]byte("this is not an image")); err == nil {
		t.Error("stored a non-image")
	}
	if _, err := cache.Store(nil); err == nil {
		t.Error("stored an empty cover")
	}
}
