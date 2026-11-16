package model

import "time"

// Position is where one device left off in one edition. The ebook position and
// the audio position are different places, so progress never collapses to a
// book.
type Position struct {
	EditionID  int64
	DeviceID   *int64
	DeviceName string
	CFI        *string
	Page       *int
	Seconds    *float64
	FileIndex  *int
	Percent    float64
	ClientSeq  int64
	UpdatedAt  time.Time
}

type ReadingState string

const (
	StateUnread    ReadingState = "unread"
	StateReading   ReadingState = "reading"
	StateFinished  ReadingState = "finished"
	StateAbandoned ReadingState = "abandoned"
)

var ReadingStates = []ReadingState{StateUnread, StateReading, StateFinished, StateAbandoned}

// FinishedPercent is where a book counts as read. Short of 1.0 because an
// audiobook ends in credits and an ebook in back matter, and nobody listens to
// an acknowledgements section to make a shelf correct.
const FinishedPercent = 0.95

// Marked is the subset of states a person sets by hand. Everything else
// follows from progress, which is what keeps "continue reading" right with
// nobody maintaining it.
type Marked string

const (
	MarkedNone      Marked = ""
	MarkedFinished  Marked = "finished"
	MarkedAbandoned Marked = "abandoned"
)

// DeriveReadingState resolves a book's state from the furthest progress across
// its editions. db.ReadingStateSQL is the twin, held to this one by
// TestReadingStateMatchesSQL.
//
// A hand-set mark wins: abandoning a book you are 60% through has to survive
// the progress that would otherwise call it "reading".
func DeriveReadingState(marked Marked, furthestPercent *float64) ReadingState {
	switch marked {
	case MarkedAbandoned:
		return StateAbandoned
	case MarkedFinished:
		return StateFinished
	}

	if furthestPercent == nil || *furthestPercent <= 0 {
		return StateUnread
	}
	if *furthestPercent >= FinishedPercent {
		return StateFinished
	}
	return StateReading
}
