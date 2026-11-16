// Package taxonomy turns provider subject strings into a usable genre facet.
// The canonical list is a maintained artifact, trimmed by hand from BISAC's
// top-level headings rather than derived from what providers happen to return.
package taxonomy

// Genre is one entry in the canonical list. Slug is the stable identity: the
// facet filters on it and renaming a Name leaves existing rows alone.
type Genre struct {
	Name string
	Slug string
}

// Canonical is flat on purpose. A hierarchy would make the facet ask which
// level it is showing, and "Epic Fantasy under Fantasy" is a distinction
// provider data is far too dirty to support.
var Canonical = []Genre{
	{"Adventure", "adventure"},
	{"Art", "art"},
	{"Biography", "biography"},
	{"Business", "business"},
	{"Classics", "classics"},
	{"Comics & Graphic Novels", "comics"},
	{"Cooking", "cooking"},
	{"Crime", "crime"},
	{"Drama", "drama"},
	{"Dystopian", "dystopian"},
	{"Economics", "economics"},
	{"Education", "education"},
	{"Essays", "essays"},
	{"Fantasy", "fantasy"},
	{"Health", "health"},
	{"Historical Fiction", "historical-fiction"},
	{"History", "history"},
	{"Horror", "horror"},
	{"Humour", "humour"},
	{"Language", "language"},
	{"Literary Fiction", "literary-fiction"},
	{"Memoir", "memoir"},
	{"Music", "music"},
	{"Mystery", "mystery"},
	{"Mythology", "mythology"},
	{"Nature", "nature"},
	{"Philosophy", "philosophy"},
	{"Poetry", "poetry"},
	{"Politics", "politics"},
	{"Psychology", "psychology"},
	{"Reference", "reference"},
	{"Religion", "religion"},
	{"Romance", "romance"},
	{"Science", "science"},
	{"Science Fiction", "science-fiction"},
	{"Self-Help", "self-help"},
	{"Short Stories", "short-stories"},
	{"Sports", "sports"},
	{"Technology", "technology"},
	{"Thriller", "thriller"},
	{"Travel", "travel"},
	{"True Crime", "true-crime"},
	{"War", "war"},
	{"Western", "western"},
}

var bySlug = func() map[string]Genre {
	out := make(map[string]Genre, len(Canonical))
	for _, g := range Canonical {
		out[g.Slug] = g
	}
	return out
}()

func BySlug(slug string) (Genre, bool) {
	g, ok := bySlug[slug]
	return g, ok
}
