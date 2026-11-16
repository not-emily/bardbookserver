// Package provider enriches a scanned edition from online metadata services.
// Every provider is off until the config names it: a lookup sends a title the
// user owns to a third party.
package provider

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

type Query struct {
	Title   string
	Authors []string
	ISBN    string
	ASIN    string
	Kind    model.Kind

	// The file wins on anything it can measure, so these ask a provider to fill
	// only what is absent. Without them an estimate would outrank a measurement.
	WantCover     bool
	WantPageCount bool
	WantChapters  bool

	// Pinned records chosen by hand, keyed by provider. A pinned provider is
	// fetched rather than scored, so a rescan cannot decide differently.
	Pinned map[string]string
}

// Searchable is false for a file with no title and no identifier, which could
// only ever match a random book.
func (q Query) Searchable() bool {
	return q.ISBN != "" || q.ASIN != "" || q.Title != ""
}

type Candidate struct {
	Provider string
	ID       string
	Title    string
	Subtitle string
	Authors  []string
	Year     int
	ISBN     string
	ASIN     string
	CoverURL string

	// Score is filled by Rank, never by a provider.
	Score float64

	// payload carries what the search returned, so Fetch need not ask again.
	payload any
}

type Provider interface {
	Name() string

	// Serves saves a request: Audnexus knows nothing about an EPUB.
	Serves(model.Kind) bool

	Search(ctx context.Context, q Query) ([]Candidate, error)
	Fetch(ctx context.Context, q Query, c Candidate) (metadata.Metadata, error)
}

type Registry struct {
	providers []Provider
	logger    *slog.Logger
}

func NewRegistry(logger *slog.Logger, providers ...Provider) *Registry {
	return &Registry{providers: providers, logger: logger}
}

// New registers the enabled providers in precedence order: the first to know a
// field owns it. Audnexus leads because it alone knows narration, and it
// declines every ebook, so Open Library still leads for those.
func New(cfg config.Metadata, database *sql.DB, logger *slog.Logger) *Registry {
	r := &Registry{logger: logger}

	shared := &http.Client{Timeout: cfg.Timeout()}
	cache := NewCache(database, logger)

	for _, name := range []string{
		config.ProviderAudnexus,
		config.ProviderOpenLibrary,
		config.ProviderGoogleBooks,
	} {
		if !cfg.ProviderEnabled(name) {
			continue
		}
		c := &client{provider: name, http: shared, cache: cache, logger: logger}

		switch name {
		case config.ProviderOpenLibrary:
			r.providers = append(r.providers, newOpenLibrary(c))
		case config.ProviderGoogleBooks:
			r.providers = append(r.providers, newGoogleBooks(c, cfg.GoogleBooksKey))
		case config.ProviderAudnexus:
			r.providers = append(r.providers, newAudnexus(c))
		default:
			// Enabling a provider and getting silence is the failure this avoids.
			logger.Warn("metadata provider is not implemented yet", "provider", name)
		}
	}
	return r
}

func (r *Registry) Enabled() bool { return r != nil && len(r.providers) > 0 }

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Name())
	}
	return out
}

// Lookup merges what the enabled providers return. A failure is a warning, not
// an error: a dead provider must leave the scan successful on embedded
// metadata.
func (r *Registry) Lookup(ctx context.Context, q Query) (metadata.Metadata, []error) {
	if !r.Enabled() || !q.Searchable() {
		return metadata.Metadata{}, nil
	}

	var (
		found    []metadata.Metadata
		warnings []error
	)
	for _, p := range r.providers {
		if !p.Serves(q.Kind) {
			continue
		}

		md, matched, err := lookupOne(ctx, p, q)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("%s lookup for %q: %w", p.Name(), q.Title, err))
			continue
		}
		if !matched {
			continue
		}
		// A later provider's artwork would be discarded by the merge anyway.
		if md.Cover != nil {
			q.WantCover = false
		}
		found = append(found, md)
	}
	return metadata.Resolve(found...), warnings
}

func lookupOne(ctx context.Context, p Provider, q Query) (metadata.Metadata, bool, error) {
	if id := q.Pinned[p.Name()]; id != "" {
		return fetchByID(ctx, p, q, id)
	}

	candidates, err := p.Search(ctx, q)
	if err != nil {
		return metadata.Metadata{}, false, err
	}

	best, ok := Best(q, candidates)
	if !ok {
		return metadata.Metadata{}, false, nil
	}

	md, err := p.Fetch(ctx, q, best)
	if err != nil {
		return metadata.Metadata{}, false, err
	}
	return md, true, nil
}

// fetchByID re-runs the search and finds the record again. The response cache
// makes that free, and it saves holding a candidate's payload between being
// shown and being chosen.
func fetchByID(ctx context.Context, p Provider, q Query, id string) (metadata.Metadata, bool, error) {
	candidates, err := p.Search(ctx, q)
	if err != nil {
		return metadata.Metadata{}, false, err
	}

	for _, c := range candidates {
		if c.ID != id {
			continue
		}
		md, err := p.Fetch(ctx, q, c)
		if err != nil {
			return metadata.Metadata{}, false, err
		}
		return md, true, nil
	}
	return metadata.Metadata{}, false, fmt.Errorf("%s no longer offers %q", p.Name(), id)
}

// Candidates keeps what Best would have refused, since Fix Match exists to
// show a person exactly that.
func (r *Registry) Candidates(ctx context.Context, q Query) ([]Candidate, []error) {
	if !r.Enabled() || !q.Searchable() {
		return nil, nil
	}

	var (
		out      []Candidate
		warnings []error
	)
	for _, p := range r.providers {
		if !p.Serves(q.Kind) {
			continue
		}
		found, err := p.Search(ctx, q)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("%s search for %q: %w", p.Name(), q.Title, err))
			continue
		}
		out = append(out, found...)
	}
	return Rank(q, out), warnings
}

// Chosen fetches one named record, for a person applying a Fix Match.
func (r *Registry) Chosen(ctx context.Context, q Query, name, id string) (metadata.Metadata, error) {
	for _, p := range r.providers {
		if p.Name() != name {
			continue
		}
		md, ok, err := fetchByID(ctx, p, q, id)
		if err != nil {
			return metadata.Metadata{}, err
		}
		if !ok {
			return metadata.Metadata{}, fmt.Errorf("%s has no record %q", name, id)
		}
		return md, nil
	}
	return metadata.Metadata{}, fmt.Errorf("provider %q is not enabled", name)
}

// parsePosition reads a position a provider writes as free text, such as
// "Book 2".
func parsePosition(v string) *float64 {
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return !(r >= '0' && r <= '9') && r != '.'
	})
	for _, field := range fields {
		if n, err := strconv.ParseFloat(field, 64); err == nil {
			return &n
		}
	}
	return nil
}

// QueryFor asks about everything known locally, since an untagged file knows
// its title only from its path.
func QueryFor(local metadata.Metadata, kind model.Kind, locked model.FieldSet) Query {
	return Query{
		Title:         local.Title,
		Authors:       local.Authors,
		ISBN:          local.ISBN,
		ASIN:          local.ASIN,
		Kind:          kind,
		WantCover:     local.Cover == nil && !locked.Has(model.FieldCover),
		WantPageCount: local.PageCount == nil,
		WantChapters:  len(local.Chapters) == 0,
	}
}
