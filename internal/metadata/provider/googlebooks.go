package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

const googleBooksBase = "https://www.googleapis.com/books/v1"

// googleMaxSubjects caps the BISAC-shaped categories, which are few but can
// repeat across volumes.
const googleMaxSubjects = 20

type googleBooks struct {
	client *client
	base   string
	key    string
}

func newGoogleBooks(c *client, key string) *googleBooks {
	c.limiter = newLimiter(politeInterval)
	c.breaker = &breaker{}
	return &googleBooks{client: c, base: googleBooksBase, key: key}
}

func (g *googleBooks) Name() string { return config.ProviderGoogleBooks }

// Google Books catalogues print editions, so it answers for an audiobook's
// title, author and description but knows nothing about narration.
func (g *googleBooks) Serves(model.Kind) bool { return true }

func (g *googleBooks) Search(ctx context.Context, q Query) ([]Candidate, error) {
	params := url.Values{
		"q":          {googleQuery(q)},
		"maxResults": {"5"},
		"printType":  {"books"},
	}
	if g.key != "" {
		params.Set("key", g.key)
	}

	var resp googleVolumes
	if err := g.client.getJSON(ctx, g.base+"/volumes?"+params.Encode(), &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]Candidate, 0, len(resp.Items))
	for _, item := range resp.Items {
		info := item.VolumeInfo
		if item.ID == "" || info.Title == "" {
			continue
		}
		out = append(out, Candidate{
			Provider: g.Name(),
			ID:       item.ID,
			Title:    info.Title,
			Subtitle: info.Subtitle,
			Authors:  info.Authors,
			Year:     yearOf(info.PublishedDate),
			ISBN:     info.isbn(),
			CoverURL: info.ImageLinks.best(),
			payload:  item,
		})
	}
	return out, nil
}

// googleQuery uses the field-scoped syntax, because a bare title search ranks
// anything mentioning the words and quoting alone does not constrain the field.
func googleQuery(q Query) string {
	if isbn := metadata.NormalizeISBN(q.ISBN); isbn != "" {
		return "isbn:" + isbn
	}

	terms := []string{`intitle:"` + googleEscape(q.Title) + `"`}
	if len(q.Authors) > 0 {
		terms = append(terms, `inauthor:"`+googleEscape(q.Authors[0])+`"`)
	}
	return strings.Join(terms, " ")
}

// googleEscape drops the quote characters that would close the field term
// early. Nothing else in the syntax is meaningful inside quotes.
func googleEscape(v string) string {
	return strings.NewReplacer(`"`, " ", `\`, " ").Replace(v)
}

// Fetch needs no second request: the volume carries everything but the cover
// bytes.
func (g *googleBooks) Fetch(ctx context.Context, q Query, c Candidate) (metadata.Metadata, error) {
	item, ok := c.payload.(googleVolume)
	if !ok {
		return metadata.Metadata{}, fmt.Errorf("googlebooks: candidate %q carries no volume", c.ID)
	}
	info := item.VolumeInfo

	md := metadata.Metadata{
		Title:       info.Title,
		Subtitle:    info.Subtitle,
		Description: info.Description,
		Authors:     info.Authors,
		Publisher:   info.Publisher,
		PublishedOn: metadata.NormalizeDate(info.PublishedDate),
		ISBN:        c.ISBN,
		Language:    metadata.NormalizeLanguage(info.Language),
		Subjects:    googleSubjects(info.Categories),
	}
	if q.WantPageCount && info.PageCount > 0 {
		pages := info.PageCount
		md.PageCount = &pages
	}

	if q.WantCover && c.CoverURL != "" {
		if cover, err := g.cover(ctx, c.CoverURL); err != nil {
			g.client.logger.Debug("googlebooks cover unavailable",
				"url", c.CoverURL, "error", err)
		} else {
			md.Cover = cover
		}
	}
	return md, nil
}

func (g *googleBooks) cover(ctx context.Context, target string) (*metadata.Cover, error) {
	body, err := g.client.getBytes(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(body) < 1024 {
		return nil, fmt.Errorf("cover %s is %d bytes, which is not artwork", target, len(body))
	}
	return &metadata.Cover{Data: body, MIME: "image/jpeg"}, nil
}

// googleSubjects splits the BISAC-shaped headings into their parts, so
// "Fiction / Fantasy / Epic" offers the taxonomy three chances to map rather
// than one string to miss on.
func googleSubjects(categories []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(categories))

	for _, category := range categories {
		for _, part := range strings.Split(category, "/") {
			part = strings.TrimSpace(part)
			if part == "" || seen[strings.ToLower(part)] {
				continue
			}
			seen[strings.ToLower(part)] = true
			out = append(out, part)
			if len(out) == googleMaxSubjects {
				return out
			}
		}
	}
	return out
}

func yearOf(published string) int {
	date := metadata.NormalizeDate(published)
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

type googleVolumes struct {
	Items []googleVolume `json:"items"`
}

type googleVolume struct {
	ID         string           `json:"id"`
	VolumeInfo googleVolumeInfo `json:"volumeInfo"`
}

type googleVolumeInfo struct {
	Title               string             `json:"title"`
	Subtitle            string             `json:"subtitle"`
	Authors             []string           `json:"authors"`
	Publisher           string             `json:"publisher"`
	PublishedDate       string             `json:"publishedDate"`
	Description         string             `json:"description"`
	IndustryIdentifiers []googleIdentifier `json:"industryIdentifiers"`
	PageCount           int                `json:"pageCount"`
	Categories          []string           `json:"categories"`
	MaturityRating      string             `json:"maturityRating"`
	Language            string             `json:"language"`
	ImageLinks          googleImageLinks   `json:"imageLinks"`
}

type googleIdentifier struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

// isbn prefers the 13-digit form, which is what a modern edition carries.
func (i googleVolumeInfo) isbn() string {
	var ten string
	for _, id := range i.IndustryIdentifiers {
		switch id.Type {
		case "ISBN_13":
			if isbn := metadata.NormalizeISBN(id.Identifier); isbn != "" {
				return isbn
			}
		case "ISBN_10":
			if isbn := metadata.NormalizeISBN(id.Identifier); isbn != "" && ten == "" {
				ten = isbn
			}
		}
	}
	return ten
}

type googleImageLinks struct {
	ExtraLarge     string `json:"extraLarge"`
	Large          string `json:"large"`
	Medium         string `json:"medium"`
	Small          string `json:"small"`
	Thumbnail      string `json:"thumbnail"`
	SmallThumbnail string `json:"smallThumbnail"`
}

// best takes the largest offered, since the thumbnail is around 128px and the
// cover cache generates its own variants from whatever it is given.
func (l googleImageLinks) best() string {
	for _, link := range []string{
		l.ExtraLarge, l.Large, l.Medium, l.Small, l.Thumbnail, l.SmallThumbnail,
	} {
		if link != "" {
			return link
		}
	}
	return ""
}
