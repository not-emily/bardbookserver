package model

import "time"

type Format string

const (
	FormatEPUB Format = "epub"
	FormatPDF  Format = "pdf"
	FormatM4B  Format = "m4b"
	FormatMP3  Format = "mp3"
)

type Kind string

const (
	KindEbook     Kind = "ebook"
	KindAudiobook Kind = "audiobook"
)

func (f Format) Kind() Kind {
	switch f {
	case FormatM4B, FormatMP3:
		return KindAudiobook
	default:
		return KindEbook
	}
}

type Edition struct {
	ID              int64
	BookID          int64
	SourceID        int64
	Format          Format
	Kind            Kind
	Language        string
	Publisher       string
	PublishedOn     string
	ISBN            string
	ASIN            string
	PageCount       *int
	DurationSeconds *float64
	Abridged        bool
	LockedFields    FieldSet
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
