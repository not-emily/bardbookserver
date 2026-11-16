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
// title and description but knows nothing about narration.
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

// googleQuery scopes the fields, since a bare search ranks anything mentioning
// the words.
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

// googleEscape drops the characters that would close a field term early.
func googleEscape(v string) string {
	return strings.NewReplacer(`"`, " ", `\`, " ").Replace(v)
}

// Fetch needs no second request; the volume carries everything but the cover.
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
	if info.MaturityRating != "" {
		mature := info.MaturityRating == "MATURE"
		md.Mature = &mature
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

// googleSubjects keeps the BISAC headings whole; the taxonomy splits them, so
// a heading stays readable in raw_subjects when nothing maps.
func googleSubjects(categories []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(categories))

	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" || seen[strings.ToLower(category)] {
			continue
		}
		seen[strings.ToLower(category)] = true

		out = append(out, category)
		if len(out) == googleMaxSubjects {
			return out
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

// best takes the largest offered; the thumbnail is around 128px.
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
