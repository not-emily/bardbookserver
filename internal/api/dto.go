package api

import (
	"fmt"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/media"
)

type seriesRefDTO struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Position *float64 `json:"position,omitempty"`
}

type bookDTO struct {
	ID                int64         `json:"id"`
	Title             string        `json:"title"`
	Subtitle          string        `json:"subtitle,omitempty"`
	SortTitle         string        `json:"sort_title"`
	Authors           []string      `json:"authors"`
	Narrators         []string      `json:"narrators,omitempty"`
	Series            *seriesRefDTO `json:"series,omitempty"`
	Formats           []string      `json:"formats"`
	Kinds             []string      `json:"kinds"`
	Languages         []string      `json:"languages,omitempty"`
	EditionCount      int           `json:"edition_count"`
	DurationSeconds   *float64      `json:"duration_seconds,omitempty"`
	PageCount         *int          `json:"page_count,omitempty"`
	PublishedOn       string        `json:"published_on,omitempty"`
	HasCover          bool          `json:"has_cover"`
	UncertainGrouping bool          `json:"uncertain_grouping,omitempty"`
	AddedAt           string        `json:"added_at"`
}

// newBookDTO is explicit rather than a serialized model: a book row carries
// min_age internals and its editions carry source paths.
func newBookDTO(row *db.BookRow) bookDTO {
	dto := bookDTO{
		ID:                row.ID,
		Title:             row.Title,
		Subtitle:          row.Subtitle,
		SortTitle:         row.SortTitle,
		Authors:           orEmpty(row.Authors),
		Narrators:         row.Narrators,
		Formats:           orEmpty(row.Formats),
		Kinds:             orEmpty(row.Kinds),
		Languages:         row.Languages,
		EditionCount:      row.EditionCount,
		DurationSeconds:   row.DurationSeconds,
		PageCount:         row.PageCount,
		PublishedOn:       row.PublishedOn,
		HasCover:          row.CoverHash != "",
		UncertainGrouping: uncertain(row.MinConfidence),
		AddedAt:           stamp(row.CreatedAt),
	}
	if row.SeriesID != nil {
		dto.Series = &seriesRefDTO{
			ID:       *row.SeriesID,
			Name:     row.SeriesName,
			Position: row.SeriesPosition,
		}
	}
	return dto
}

type genreDTO struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type chapterDTO struct {
	Ordinal      int      `json:"ordinal"`
	Title        string   `json:"title,omitempty"`
	StartSeconds *float64 `json:"start_seconds,omitempty"`
	Href         string   `json:"href,omitempty"`
}

type fileDTO struct {
	ID              int64    `json:"id"`
	Ordinal         int      `json:"ordinal"`
	SizeBytes       int64    `json:"size_bytes"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
	ETag            string   `json:"etag"`
	ContentURL      string   `json:"content_url"`
}

type editionDTO struct {
	ID                int64        `json:"id"`
	Format            string       `json:"format"`
	Kind              string       `json:"kind"`
	Language          string       `json:"language,omitempty"`
	Publisher         string       `json:"publisher,omitempty"`
	PublishedOn       string       `json:"published_on,omitempty"`
	ISBN              string       `json:"isbn,omitempty"`
	ASIN              string       `json:"asin,omitempty"`
	PageCount         *int         `json:"page_count,omitempty"`
	DurationSeconds   *float64     `json:"duration_seconds,omitempty"`
	Abridged          bool         `json:"abridged"`
	Narrators         []string     `json:"narrators,omitempty"`
	UncertainGrouping bool         `json:"uncertain_grouping,omitempty"`
	Files             []fileDTO    `json:"files"`
	Chapters          []chapterDTO `json:"chapters,omitempty"`
}

type bookDetailDTO struct {
	bookDTO
	Description string       `json:"description,omitempty"`
	Genres      []genreDTO   `json:"genres,omitempty"`
	Tags        []string     `json:"tags,omitempty"`
	Editions    []editionDTO `json:"editions"`
}

func newBookDetailDTO(detail *db.BookDetail) bookDetailDTO {
	dto := bookDetailDTO{
		bookDTO:     newBookDTO(&detail.Book),
		Description: detail.Book.Description,
		Tags:        detail.Tags,
		Editions:    make([]editionDTO, 0, len(detail.Editions)),
	}
	for _, g := range detail.Genres {
		dto.Genres = append(dto.Genres, genreDTO{Name: g.Name, Slug: g.Slug})
	}
	for i := range detail.Editions {
		dto.Editions = append(dto.Editions, newEditionDTO(&detail.Editions[i]))
	}
	return dto
}

func newEditionDTO(e *db.EditionDetail) editionDTO {
	dto := editionDTO{
		ID:                e.ID,
		Format:            e.Format,
		Kind:              e.Kind,
		Language:          e.Language,
		Publisher:         e.Publisher,
		PublishedOn:       e.PublishedOn,
		ISBN:              e.ISBN,
		ASIN:              e.ASIN,
		PageCount:         e.PageCount,
		DurationSeconds:   e.DurationSeconds,
		Abridged:          e.Abridged,
		Narrators:         e.Narrators,
		UncertainGrouping: uncertain(e.MatchConfidence),
		Files:             make([]fileDTO, 0, len(e.Files)),
	}
	for _, f := range e.Files {
		dto.Files = append(dto.Files, fileDTO{
			ID:              f.ID,
			Ordinal:         f.Ordinal,
			SizeBytes:       f.SizeBytes,
			DurationSeconds: f.DurationSeconds,
			ETag:            media.FileETag(f.ID, f.SizeBytes, f.ModifiedAt, f.ContentHash),
			ContentURL:      fmt.Sprintf("/api/v1/files/%d/content", f.ID),
		})
	}
	for _, c := range e.Chapters {
		dto.Chapters = append(dto.Chapters, chapterDTO{
			Ordinal:      c.Ordinal,
			Title:        c.Title,
			StartSeconds: c.StartSeconds,
			Href:         c.Href,
		})
	}
	return dto
}

// uncertain marks a grouping Bard guessed at, so a client can say so rather
// than present two editions as a settled fact.
func uncertain(confidence *float64) bool {
	return confidence != nil && *confidence < grouping.CertainEnough
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
