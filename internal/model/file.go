package model

import "time"

type File struct {
	ID              int64
	EditionID       int64
	Path            string
	SizeBytes       int64
	ModifiedAt      time.Time
	ContentHash     string
	Ordinal         int
	DurationSeconds *float64
	CreatedAt       time.Time
}

type Chapter struct {
	ID           int64
	EditionID    int64
	Ordinal      int
	Title        string
	StartSeconds *float64
	Href         string
}
