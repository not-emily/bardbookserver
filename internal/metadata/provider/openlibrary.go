package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

const (
	openLibraryBase   = "https://openlibrary.org"
	openLibraryCovers = "https://covers.openlibrary.org"
)

// olSearchFields narrows a default response that is mostly holdings data.
const olSearchFields = "key,title,subtitle,author_name,first_publish_year," +
	"isbn,publisher,language,number_of_pages_median,cover_i,subject"

// olMaxSubjects caps a pooled list that runs into the hundreds.
const olMaxSubjects = 40

type openLibrary struct {
	client *client
	base   string
	covers string
}

func newOpenLibrary(c *client) *openLibrary {
	c.limiter = newLimiter(politeInterval)
	return &openLibrary{client: c, base: openLibraryBase, covers: openLibraryCovers}
}

func (o *openLibrary) Name() string { return config.ProviderOpenLibrary }

// Open Library catalogues works, so it answers for an audiobook's title and
// author even knowing nothing about narration.
func (o *openLibrary) Serves(model.Kind) bool { return true }

func (o *openLibrary) Search(ctx context.Context, q Query) ([]Candidate, error) {
	target := o.base + "/search.json?" + o.searchParams(q).Encode()

	var resp olSearchResponse
	if err := o.client.getJSON(ctx, target, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]Candidate, 0, len(resp.Docs))
	for _, doc := range resp.Docs {
		if doc.Key == "" || doc.Title == "" {
			continue
		}
		out = append(out, o.candidate(q, doc))
	}
	return out, nil
}

// searchParams prefers the ISBN, which answers with one work rather than
// everything sharing a title.
func (o *openLibrary) searchParams(q Query) url.Values {
	params := url.Values{"fields": {olSearchFields}, "limit": {"5"}}

	if isbn := metadata.NormalizeISBN(q.ISBN); isbn != "" {
		params.Set("q", "isbn:"+isbn)
		return params
	}

	params.Set("title", q.Title)
	if len(q.Authors) > 0 {
		params.Set("author", q.Authors[0])
	}
	return params
}

func (o *openLibrary) candidate(q Query, doc olDoc) Candidate {
	c := Candidate{
		Provider: o.Name(),
		ID:       doc.Key,
		Title:    doc.Title,
		Subtitle: doc.Subtitle,
		Authors:  doc.AuthorName,
		Year:     doc.FirstPublishYear,
		ISBN:     doc.pickISBN(q.ISBN),
		payload:  doc,
	}
	if doc.CoverID > 0 {
		c.CoverURL = fmt.Sprintf("%s/b/id/%d-L.jpg", o.covers, doc.CoverID)
	}
	return c
}

// Fetch spends a second request only on the description; the search response
// holds everything else.
func (o *openLibrary) Fetch(ctx context.Context, q Query, c Candidate) (metadata.Metadata, error) {
	doc, ok := c.payload.(olDoc)
	if !ok {
		return metadata.Metadata{}, fmt.Errorf("openlibrary: candidate %q carries no search result", c.ID)
	}

	md := metadata.Metadata{
		Title:     doc.Title,
		Subtitle:  doc.Subtitle,
		Authors:   doc.AuthorName,
		Publisher: first(doc.Publisher),
		ISBN:      c.ISBN,
		Subjects:  doc.Subject,
	}
	if doc.FirstPublishYear > 0 {
		md.PublishedOn = strconv.Itoa(doc.FirstPublishYear)
	}
	// Languages are pooled across editions — The Hobbit answers with 32 — so
	// only a single entry says anything about this book.
	if len(doc.Language) == 1 {
		md.Language = metadata.NormalizeLanguage(doc.Language[0])
	}
	// A median across editions is an estimate, so it is offered only where the
	// file could not say.
	if q.WantPageCount && doc.PagesMedian != nil && *doc.PagesMedian > 0 {
		md.PageCount = doc.PagesMedian
	}

	work, err := o.work(ctx, doc.Key)
	if err != nil {
		// Costs the description alone; the candidate still stands.
		o.client.logger.Debug("openlibrary work unavailable",
			"work", doc.Key, "error", err)
	} else {
		md.Description = work.Description.Value
		if len(md.Subjects) == 0 {
			md.Subjects = work.Subjects
		}
	}
	md.Subjects = capSubjects(md.Subjects)

	if q.WantCover && c.CoverURL != "" {
		if cover, err := o.cover(ctx, c.CoverURL); err != nil {
			o.client.logger.Debug("openlibrary cover unavailable",
				"url", c.CoverURL, "error", err)
		} else {
			md.Cover = cover
		}
	}
	return md, nil
}

// work follows one hop, since a merged work answers under its old key with
// nothing but a pointer. One hop only, so a cycle cannot hold the scan.
func (o *openLibrary) work(ctx context.Context, key string) (olWork, error) {
	var work olWork
	if err := o.client.getJSON(ctx, o.base+key+".json", &work); err != nil {
		return work, err
	}
	if !work.redirect() {
		return work, nil
	}

	var merged olWork
	if err := o.client.getJSON(ctx, o.base+work.Location+".json", &merged); err != nil {
		return work, err
	}
	if merged.redirect() {
		return merged, fmt.Errorf("openlibrary: %s redirects more than once", key)
	}
	return merged, nil
}

func (o *openLibrary) cover(ctx context.Context, target string) (*metadata.Cover, error) {
	body, err := o.client.getBytes(ctx, target)
	if err != nil {
		return nil, err
	}
	// A missing cover comes back as a 1-pixel placeholder, not a 404.
	if len(body) < 1024 {
		return nil, fmt.Errorf("cover %s is %d bytes, which is a placeholder", target, len(body))
	}
	return &metadata.Cover{Data: body, MIME: "image/jpeg"}, nil
}

type olSearchResponse struct {
	Docs []olDoc `json:"docs"`
}

type olDoc struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	AuthorName       []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	ISBN             []string `json:"isbn"`
	Publisher        []string `json:"publisher"`
	Language         []string `json:"language"`
	PagesMedian      *int     `json:"number_of_pages_median"`
	CoverID          int      `json:"cover_i"`
	Subject          []string `json:"subject"`
}

// pickISBN prefers what the file said, since a doc lists every edition's ISBN
// and only one of them is this book's.
func (d olDoc) pickISBN(want string) string {
	if wanted := metadata.ISBN13(want); wanted != "" {
		for _, candidate := range d.ISBN {
			if metadata.ISBN13(candidate) == wanted {
				return metadata.NormalizeISBN(candidate)
			}
		}
	}
	for _, candidate := range d.ISBN {
		if isbn := metadata.NormalizeISBN(candidate); isbn != "" {
			return isbn
		}
	}
	return ""
}

type olWork struct {
	Type        olKey    `json:"type"`
	Location    string   `json:"location"`
	Description olText   `json:"description"`
	Subjects    []string `json:"subjects"`
}

type olKey struct {
	Key string `json:"key"`
}

func (w olWork) redirect() bool {
	return w.Type.Key == "/type/redirect" && strings.HasPrefix(w.Location, "/works/")
}

// olText absorbs two shapes: older records hold a bare string, newer ones a
// typed object.
type olText struct {
	Value string
}

func (t *olText) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		t.Value = strings.TrimSpace(text)
		return nil
	}

	var object struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	t.Value = strings.TrimSpace(object.Value)
	return nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func capSubjects(subjects []string) []string {
	if len(subjects) > olMaxSubjects {
		return subjects[:olMaxSubjects]
	}
	return subjects
}
