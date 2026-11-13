package grouping

import "strings"

type Reason string

const (
	ReasonManual     Reason = "manual"
	ReasonIdentifier Reason = "identifier"
	ReasonExactTitle Reason = "exact_title"
	ReasonLooseTitle Reason = "loose_title"
	ReasonNew        Reason = "new"
)

const (
	ConfidenceManual     = 1.0
	ConfidenceIdentifier = 1.0
	ConfidenceExactTitle = 0.9
	ConfidenceLooseTitle = 0.7
	ConfidenceNew        = 1.0

	// CertainEnough divides a grouping Bard stands behind from one it queues
	// for a human.
	CertainEnough = 0.9
)

func Confidence(r Reason) float64 {
	switch r {
	case ReasonManual:
		return ConfidenceManual
	case ReasonIdentifier:
		return ConfidenceIdentifier
	case ReasonExactTitle:
		return ConfidenceExactTitle
	case ReasonLooseTitle:
		return ConfidenceLooseTitle
	default:
		return ConfidenceNew
	}
}

type Candidate struct {
	Title   string
	Authors []string
	ISBN    string
	ASIN    string
}

// Keys are the lookups an edition offers, strongest first. An empty string
// means that key is unavailable and must not be matched on.
type Keys struct {
	Identifier string
	Exact      string
	Loose      string
}

func (c Candidate) Keys() Keys {
	title := strings.TrimSpace(c.Title)
	if title == "" {
		return Keys{Identifier: c.identifier()}
	}

	// Title alone would merge two different books called "Collected Poems".
	author := c.firstAuthor()
	if author == "" {
		return Keys{Identifier: c.identifier()}
	}

	keys := Keys{
		Identifier: c.identifier(),
		Exact:      NormalizeTitle(title) + "|" + author,
	}
	// Stored even when it repeats Exact, so a subtitled edition has something
	// to find on a title that carries no subtitle.
	if loose := NormalizeTitle(StripSubtitle(title)); loose != "" {
		keys.Loose = loose + "|" + author
	}
	return keys
}

func (c Candidate) identifier() string {
	if isbn := strings.TrimSpace(c.ISBN); isbn != "" {
		return "isbn:" + isbn
	}
	if asin := strings.TrimSpace(c.ASIN); asin != "" {
		return "asin:" + asin
	}
	return ""
}

func (c Candidate) firstAuthor() string {
	for _, a := range c.Authors {
		if normalized := NormalizeAuthor(a); normalized != "" {
			return normalized
		}
	}
	return ""
}
