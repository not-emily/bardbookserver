// Package provider enriches a scanned edition from online metadata services.
// Every provider is off until the config names it, because a lookup sends a
// title the user owns to a third party.
package provider

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

// Query is what the file itself knows, which is both the search input and the
// yardstick a candidate is scored against.
type Query struct {
	Title   string
	Authors []string
	ISBN    string
	ASIN    string
	Kind    model.Kind

	// The file wins on anything it can measure — its own page count, chapter
	// marks and cover art — so these ask the provider to fill only what is
	// genuinely absent. Without them the precedence chain would let an
	// estimate overwrite a measurement, and a library of EPUBs with embedded
	// art would download 5000 covers it already has.
	WantCover     bool
	WantPageCount bool
	WantChapters  bool
}

// Searchable reports whether there is anything worth asking about. A file with
// neither a title nor an identifier would only produce a random book.
func (q Query) Searchable() bool {
	return q.ISBN != "" || q.ASIN != "" || q.Title != ""
}

// Candidate is one provider's offer, carrying what scoring needs to judge it
// and what Fix Match needs to show it.
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

	// Score is filled by Rank, never by the provider: how well a candidate
	// matches is this package's judgement, not the service's.
	Score float64

	// payload is what the search already told this provider, so Fetch does not
	// ask again for what it has.
	payload any
}

type Provider interface {
	Name() string

	// Serves keeps a provider out of a lookup it cannot answer: Audnexus knows
	// only audiobooks, and asking it about an EPUB spends a request to learn
	// nothing.
	Serves(model.Kind) bool

	Search(ctx context.Context, q Query) ([]Candidate, error)
	Fetch(ctx context.Context, q Query, c Candidate) (metadata.Metadata, error)
}

// Registry holds the enabled providers in precedence order. Registration is
// explicit — there is no name-to-type lookup — so removing a provider that
// died, as Goodreads did in 2020, is deleting one case below.
type Registry struct {
	providers []Provider
	logger    *slog.Logger
}

// NewRegistry is the explicit form: the caller names the providers and their
// order. New is this with the config deciding which ones.
func NewRegistry(logger *slog.Logger, providers ...Provider) *Registry {
	return &Registry{providers: providers, logger: logger}
}

// New builds the registry from the config. Order is precedence order: an
// earlier provider's answer fills a field a later one cannot overwrite.
func New(cfg config.Metadata, database *sql.DB, logger *slog.Logger) *Registry {
	r := &Registry{logger: logger}

	shared := &http.Client{Timeout: cfg.Timeout()}
	cache := NewCache(database, logger)

	for _, name := range []string{
		config.ProviderOpenLibrary,
		config.ProviderGoogleBooks,
		config.ProviderAudnexus,
	} {
		if !cfg.ProviderEnabled(name) {
			continue
		}
		c := &client{provider: name, http: shared, cache: cache, logger: logger}

		switch name {
		case config.ProviderOpenLibrary:
			r.providers = append(r.providers, newOpenLibrary(c))
		default:
			// A name the config accepts but nothing implements yet. Saying so
			// is the whole point of an explicit registry: enabling a provider
			// and getting silence is the failure mode this avoids.
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

// Lookup asks every enabled provider that serves this kind and merges what
// they return in registration order. Failures come back as warnings rather
// than an error: metadata is an enrichment, and a provider that times out must
// leave the scan successful on embedded metadata alone.
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
		// One download is enough: a later provider's artwork would be thrown
		// away by the merge anyway.
		if md.Cover != nil {
			q.WantCover = false
		}
		found = append(found, md)
	}
	return metadata.Resolve(found...), warnings
}

func lookupOne(ctx context.Context, p Provider, q Query) (metadata.Metadata, bool, error) {
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

// QueryFor builds a lookup from everything known locally, which is both the
// search input and the record of what is still missing.
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
