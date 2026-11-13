package model

import "time"

type Book struct {
	ID             int64
	Title          string
	SortTitle      string
	Subtitle       string
	Description    string
	MatchKey       string
	MinAge         *int
	SeriesID       *int64
	SeriesPosition *float64
	LockedFields   FieldSet
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// FieldSet marks hand-edited fields so a metadata refresh leaves them alone.
type FieldSet uint64

func (f FieldSet) Has(field FieldSet) bool { return f&field != 0 }

func (f FieldSet) With(field FieldSet) FieldSet { return f | field }

func (f FieldSet) Without(field FieldSet) FieldSet { return f &^ field }

const (
	FieldTitle FieldSet = 1 << iota
	FieldSubtitle
	FieldDescription
	FieldSeries
	FieldMinAge
	FieldCover
	FieldAuthors
	FieldNarrators
	FieldGenres
	FieldPublisher
	FieldISBN
	FieldASIN
	FieldLanguage
)

// fieldNames are the stable names a lock is written out under. Changing one
// silently unlocks that field in every sidecar already on disk.
var fieldNames = []struct {
	field FieldSet
	name  string
}{
	{FieldTitle, "title"},
	{FieldSubtitle, "subtitle"},
	{FieldDescription, "description"},
	{FieldSeries, "series"},
	{FieldMinAge, "min_age"},
	{FieldCover, "cover"},
	{FieldAuthors, "authors"},
	{FieldNarrators, "narrators"},
	{FieldGenres, "genres"},
	{FieldPublisher, "publisher"},
	{FieldISBN, "isbn"},
	{FieldASIN, "asin"},
	{FieldLanguage, "language"},
}

// Names lists the set's fields in declaration order, so the output is stable
// enough to compare two sidecars byte for byte.
func (f FieldSet) Names() []string {
	var out []string
	for _, entry := range fieldNames {
		if f.Has(entry.field) {
			out = append(out, entry.name)
		}
	}
	return out
}

// ParseFields ignores names it does not know, so a sidecar written by a newer
// Bard still loads here.
func ParseFields(names []string) FieldSet {
	var out FieldSet
	for _, name := range names {
		for _, entry := range fieldNames {
			if entry.name == name {
				out = out.With(entry.field)
			}
		}
	}
	return out
}
