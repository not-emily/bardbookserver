package embedded

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

func ParsePDF(path string) (metadata.Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return metadata.Metadata{}, fmt.Errorf("open pdf %s: %w", path, err)
	}
	defer f.Close()

	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	info, err := api.PDFInfo(context.Background(), f, path, nil, false, conf)
	if err != nil {
		return metadata.Metadata{}, fmt.Errorf("read pdf %s: %w", path, err)
	}

	md := metadata.Metadata{
		Title:       strings.TrimSpace(info.Title),
		Subjects:    info.Keywords,
		PublishedOn: normalizeDate(pdfDate(info.CreationDate)),
	}
	if author := strings.TrimSpace(info.Author); author != "" {
		md.Authors = splitPeople(author)
	}
	if info.PageCount > 0 {
		count := info.PageCount
		md.PageCount = &count
	}
	// Subject is a free-text abstract in practice, not a genre.
	if subject := strings.TrimSpace(info.Subject); subject != "" {
		md.Description = subject
	}

	md.Cover = pdfCover(f, conf)
	return md, nil
}

// pdfCover takes the largest image on page one. Pure Go cannot rasterise a
// page, so a cover drawn from vectors or text yields nothing and the book
// shows a placeholder until a provider supplies one in Phase 8.
func pdfCover(f *os.File, conf *model.Configuration) *metadata.Cover {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}

	pages, err := api.ExtractImagesRaw(context.Background(), f, []string{"1"}, conf)
	if err != nil || len(pages) == 0 {
		return nil
	}

	var (
		best   []byte
		mime   string
		pixels int
	)
	for _, page := range pages {
		for _, img := range page {
			if img.Width*img.Height <= pixels {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(img, maxCoverBytes))
			if err != nil || len(data) == 0 {
				continue
			}
			best, pixels = data, img.Width*img.Height
			mime = "image/" + strings.TrimPrefix(strings.ToLower(img.FileType), ".")
		}
	}
	if best == nil {
		return nil
	}
	return &metadata.Cover{Data: best, MIME: mime}
}

// pdfDate converts the PDF D:YYYYMMDD form to something normalizeDate reads.
func pdfDate(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "D:"))
	if len(v) < 4 {
		return ""
	}
	for _, r := range v[:4] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if len(v) >= 8 {
		return v[:4] + "-" + v[4:6] + "-" + v[6:8]
	}
	return v[:4]
}
