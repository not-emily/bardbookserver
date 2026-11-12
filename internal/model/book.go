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
