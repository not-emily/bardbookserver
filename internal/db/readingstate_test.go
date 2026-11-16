package db

import (
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

// The Go and SQL reading-state implementations must agree, because the facet
// filters in SQL while handlers and clients act on the Go value. Divergence
// would let a listing show a state its own filter disagrees with.
func TestReadingStateMatchesSQL(t *testing.T) {
	d := openTestDB(t)

	userID := mustExec(t, d,
		`INSERT INTO users (username, display_name, password_hash) VALUES ('emily','Emily','h')`)
	sourceID := addSource(t, d, "Books", "/srv/books", ptr(0))

	tests := []struct {
		name     string
		marked   model.Marked
		percents []float64
		expected model.ReadingState
	}{
		{"no progress at all", model.MarkedNone, nil, model.StateUnread},
		{"opened but not started", model.MarkedNone, []float64{0}, model.StateUnread},
		{"part way through", model.MarkedNone, []float64{0.4}, model.StateReading},
		{"barely started", model.MarkedNone, []float64{0.001}, model.StateReading},
		{"exactly at the threshold", model.MarkedNone, []float64{0.95}, model.StateFinished},
		{"almost all the way", model.MarkedNone, []float64{0.94}, model.StateReading},
		{"right to the end", model.MarkedNone, []float64{1}, model.StateFinished},
		{"furthest edition wins", model.MarkedNone, []float64{0.1, 0.5}, model.StateReading},
		{"furthest edition finishes it", model.MarkedNone, []float64{0.1, 0.96}, model.StateFinished},
		{"marked finished without reading", model.MarkedFinished, nil, model.StateFinished},
		{"marked finished part way", model.MarkedFinished, []float64{0.3}, model.StateFinished},
		{"abandoned part way", model.MarkedAbandoned, []float64{0.6}, model.StateAbandoned},
		{"abandoned beats a finished percent", model.MarkedAbandoned, []float64{0.99}, model.StateAbandoned},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bookID := addBook(t, d, tc.name, nil)

			var furthest *float64
			for _, percent := range tc.percents {
				editionID := addEdition(t, d, bookID, sourceID, model.FormatEPUB)
				mustExec(t, d, `
					INSERT INTO progress (user_id, edition_id, device_id, percent)
					VALUES (?, ?, NULL, ?)`, userID, editionID, percent)

				if furthest == nil || percent > *furthest {
					furthest = ptr(percent)
				}
			}
			if tc.marked != model.MarkedNone {
				mustExec(t, d,
					`INSERT INTO user_books (user_id, book_id, marked) VALUES (?, ?, ?)`,
					userID, bookID, string(tc.marked))
			}

			fromGo := model.DeriveReadingState(tc.marked, furthest)

			var fromSQL string
			query := `SELECT ` + ReadingStateSQL("b", userID) + ` FROM books b WHERE b.id = ?`
			if err := d.QueryRow(query, bookID).Scan(&fromSQL); err != nil {
				t.Fatalf("reading state query: %v", err)
			}

			if fromGo != tc.expected {
				t.Errorf("Go = %q, want %q", fromGo, tc.expected)
			}
			if model.ReadingState(fromSQL) != tc.expected {
				t.Errorf("SQL = %q, want %q", fromSQL, tc.expected)
			}
			if string(fromGo) != fromSQL {
				t.Errorf("Go and SQL disagree: %q vs %q", fromGo, fromSQL)
			}
		})
	}
}

// Every state needs to be reachable, or the facet offers a value nothing can
// ever match.
func TestEveryReadingStateIsProduced(t *testing.T) {
	produced := map[model.ReadingState]bool{
		model.DeriveReadingState(model.MarkedNone, nil):           true,
		model.DeriveReadingState(model.MarkedNone, ptr(0.5)):      true,
		model.DeriveReadingState(model.MarkedNone, ptr(1.0)):      true,
		model.DeriveReadingState(model.MarkedAbandoned, ptr(0.5)): true,
	}

	for _, state := range model.ReadingStates {
		if !produced[state] {
			t.Errorf("no input produces %q", state)
		}
	}
}

// One user's progress must not reach another's state, which is the whole
// premise of a private space.
func TestReadingStateIsPerUser(t *testing.T) {
	d := openTestDB(t)

	emily := mustExec(t, d,
		`INSERT INTO users (username, display_name, password_hash) VALUES ('emily','Emily','h')`)
	someone := mustExec(t, d,
		`INSERT INTO users (username, display_name, password_hash) VALUES ('someone','Someone','h')`)

	sourceID := addSource(t, d, "Books", "/srv/books", ptr(0))
	bookID := addBook(t, d, "Dune", nil)
	editionID := addEdition(t, d, bookID, sourceID, model.FormatEPUB)

	mustExec(t, d, `
		INSERT INTO progress (user_id, edition_id, device_id, percent)
		VALUES (?, ?, NULL, 0.5)`, emily, editionID)

	for _, tc := range []struct {
		userID int64
		want   model.ReadingState
	}{
		{emily, model.StateReading},
		{someone, model.StateUnread},
	} {
		var got string
		query := `SELECT ` + ReadingStateSQL("b", tc.userID) + ` FROM books b WHERE b.id = ?`
		if err := d.QueryRow(query, bookID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if model.ReadingState(got) != tc.want {
			t.Errorf("user %d sees %q, want %q", tc.userID, got, tc.want)
		}
	}
}
