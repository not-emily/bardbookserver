package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

const audnexusBase = "https://api.audnex.us"

type audnexus struct {
	client *client
	base   string
}

func newAudnexus(c *client) *audnexus {
	c.limiter = newLimiter(politeInterval)
	return &audnexus{client: c, base: audnexusBase}
}

func (a *audnexus) Name() string { return config.ProviderAudnexus }

func (a *audnexus) Serves(kind model.Kind) bool { return kind == model.KindAudiobook }

// Search is a lookup: Audnexus exposes /books/{asin} and nothing else, so a
// file with no ASIN cannot be matched here at all.
func (a *audnexus) Search(ctx context.Context, q Query) ([]Candidate, error) {
	asin := metadata.NormalizeASIN(q.ASIN)
	if asin == "" {
		return nil, nil
	}

	var book audnexusBook
	if err := a.client.getJSON(ctx, a.base+"/books/"+asin, &book); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	// A region restriction arrives as a 200 with an error object, not a fault.
	if book.Error.Code != "" {
		a.client.logger.Debug("audnexus declined",
			"asin", asin, "code", book.Error.Code, "message", book.Error.Message)
		return nil, nil
	}
	if book.ASIN == "" || book.Title == "" {
		return nil, nil
	}

	return []Candidate{{
		Provider: a.Name(),
		ID:       book.ASIN,
		Title:    book.Title,
		Subtitle: book.Subtitle,
		Authors:  book.names(book.Authors),
		Year:     yearOf(book.ReleaseDate),
		ISBN:     metadata.NormalizeISBN(book.ISBN),
		// Carried so Score reads the ASIN match as conclusive.
		ASIN:     book.ASIN,
		CoverURL: book.Image,
		payload:  book,
	}}, nil
}

func (a *audnexus) Fetch(ctx context.Context, q Query, c Candidate) (metadata.Metadata, error) {
	book, ok := c.payload.(audnexusBook)
	if !ok {
		return metadata.Metadata{}, fmt.Errorf("audnexus: candidate %q carries no book", c.ID)
	}

	md := metadata.Metadata{
		Title:       book.Title,
		Subtitle:    book.Subtitle,
		Description: book.Description,
		Authors:     book.names(book.Authors),
		Narrators:   book.names(book.Narrators),
		Publisher:   book.PublisherName,
		PublishedOn: metadata.NormalizeDate(book.ReleaseDate),
		ISBN:        c.ISBN,
		ASIN:        book.ASIN,
		Language:    audnexusLanguage(book.Language),
		Subjects:    book.genreNames(),
	}
	if book.SeriesPrimary.Name != "" {
		md.Series = book.SeriesPrimary.Name
		md.SeriesPosition = book.SeriesPrimary.position()
	}
	if book.FormatType != "" {
		abridged := strings.EqualFold(book.FormatType, "abridged")
		md.Abridged = &abridged
	}
	mature := book.IsAdult
	md.Mature = &mature

	// Never the duration: Audnexus reports Audible's edition to the minute,
	// and the file that plays is the authority on its own length.
	if q.WantChapters {
		if chapters, err := a.chapters(ctx, book.ASIN); err != nil {
			a.client.logger.Debug("audnexus chapters unavailable",
				"asin", book.ASIN, "error", err)
		} else {
			md.Chapters = chapters
		}
	}

	if q.WantCover && c.CoverURL != "" {
		if cover, err := a.cover(ctx, c.CoverURL); err != nil {
			a.client.logger.Debug("audnexus cover unavailable",
				"url", c.CoverURL, "error", err)
		} else {
			md.Cover = cover
		}
	}
	return md, nil
}

// chapters refuses data Audnexus marks inaccurate: landing confidently in the
// wrong chapter is worse than having no chapter list.
func (a *audnexus) chapters(ctx context.Context, asin string) ([]metadata.Chapter, error) {
	var resp audnexusChapters
	if err := a.client.getJSON(ctx, a.base+"/books/"+asin+"/chapters", &resp); err != nil {
		return nil, err
	}
	if !resp.IsAccurate {
		return nil, fmt.Errorf("audnexus marks %s chapters inaccurate", asin)
	}

	out := make([]metadata.Chapter, 0, len(resp.Chapters))
	for _, c := range resp.Chapters {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			continue
		}
		start := float64(c.StartOffsetMs) / 1000
		out = append(out, metadata.Chapter{Title: title, StartSeconds: &start})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("audnexus returned no usable chapters for %s", asin)
	}
	return out, nil
}

func (a *audnexus) cover(ctx context.Context, target string) (*metadata.Cover, error) {
	body, err := a.client.getBytes(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(body) < 1024 {
		return nil, fmt.Errorf("cover %s is %d bytes, which is not artwork", target, len(body))
	}
	return &metadata.Cover{Data: body, MIME: "image/jpeg"}, nil
}

// audnexusLanguage folds the English name onto the code every other source
// uses, so one book does not hold both "english" and "en".
func audnexusLanguage(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "english":
		return "en"
	case "german":
		return "de"
	case "french":
		return "fr"
	case "spanish":
		return "es"
	case "italian":
		return "it"
	case "portuguese":
		return "pt"
	case "dutch":
		return "nl"
	case "japanese":
		return "ja"
	case "russian":
		return "ru"
	case "danish":
		return "da"
	case "swedish":
		return "sv"
	case "norwegian":
		return "no"
	case "finnish":
		return "fi"
	case "polish":
		return "pl"
	case "":
		return ""
	}
	// Rejects a word it cannot read rather than inventing a code.
	return metadata.NormalizeLanguage(name)
}

type audnexusBook struct {
	ASIN          string           `json:"asin"`
	Title         string           `json:"title"`
	Subtitle      string           `json:"subtitle"`
	Description   string           `json:"description"`
	Authors       []audnexusPerson `json:"authors"`
	Narrators     []audnexusPerson `json:"narrators"`
	Genres        []audnexusGenre  `json:"genres"`
	PublisherName string           `json:"publisherName"`
	ReleaseDate   string           `json:"releaseDate"`
	Language      string           `json:"language"`
	FormatType    string           `json:"formatType"`
	ISBN          string           `json:"isbn"`
	Image         string           `json:"image"`
	IsAdult       bool             `json:"isAdult"`
	SeriesPrimary audnexusSeries   `json:"seriesPrimary"`

	Error audnexusError `json:"error"`
}

type audnexusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type audnexusPerson struct {
	Name string `json:"name"`
}

func (audnexusBook) names(people []audnexusPerson) []string {
	out := make([]string, 0, len(people))
	for _, p := range people {
		if name := strings.TrimSpace(p.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

type audnexusGenre struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// genreNames keeps the tags as well as the genres; raw_subjects holds whatever
// the taxonomy cannot map.
func (b audnexusBook) genreNames() []string {
	out := make([]string, 0, len(b.Genres))
	for _, g := range b.Genres {
		if name := strings.TrimSpace(g.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

type audnexusSeries struct {
	Name     string `json:"name"`
	Position string `json:"position"`
}

func (s audnexusSeries) position() *float64 {
	return parsePosition(s.Position)
}

type audnexusChapters struct {
	ASIN       string            `json:"asin"`
	IsAccurate bool              `json:"isAccurate"`
	Chapters   []audnexusChapter `json:"chapters"`
}

type audnexusChapter struct {
	Title         string `json:"title"`
	StartOffsetMs int64  `json:"startOffsetMs"`
	LengthMs      int64  `json:"lengthMs"`
}
