package db

import (
	"strconv"

	"github.com/not-emily/bardbookserver/internal/model"
)

// ReadingStateSQL is the SQL twin of model.DeriveReadingState, taking the
// alias of the books table. It exists in SQL because the reading-state facet
// filters and counts on it, and a post-filter would break pagination.
//
// The user id is interpolated rather than bound: the expression needs it three
// times, and the query builder concatenates arguments positionally, so mixing
// in numbered parameters would misalign every other clause. It is an int64
// from the authenticated session, never a string from a request.
func ReadingStateSQL(books string, userID int64) string {
	id := strconv.FormatInt(userID, 10)
	furthest := `(SELECT MAX(p.percent) FROM progress p
	               JOIN editions e ON e.id = p.edition_id
	              WHERE e.book_id = ` + books + `.id AND p.user_id = ` + id + `)`
	marked := `(SELECT ub.marked FROM user_books ub
	             WHERE ub.user_id = ` + id + ` AND ub.book_id = ` + books + `.id)`

	return `CASE
		WHEN ` + marked + ` = '` + string(model.MarkedAbandoned) + `'
			THEN '` + string(model.StateAbandoned) + `'
		WHEN ` + marked + ` = '` + string(model.MarkedFinished) + `'
			THEN '` + string(model.StateFinished) + `'
		WHEN ` + furthest + ` IS NULL OR ` + furthest + ` <= 0
			THEN '` + string(model.StateUnread) + `'
		WHEN ` + furthest + ` >= ` + strconv.FormatFloat(model.FinishedPercent, 'f', -1, 64) + `
			THEN '` + string(model.StateFinished) + `'
		ELSE '` + string(model.StateReading) + `'
	END`
}

// savedSQL yields the strings 'true' and 'false' so the facet's values read
// the way a client would send them.
func savedSQL(books string, userID int64) string {
	return `CASE WHEN EXISTS (
		SELECT 1 FROM user_books ub
		 WHERE ub.user_id = ` + strconv.FormatInt(userID, 10) + `
		   AND ub.book_id = ` + books + `.id AND ub.saved_at IS NOT NULL)
		THEN 'true' ELSE 'false' END`
}

func ratingSQL(books string, userID int64) string {
	return `(SELECT ub.rating FROM user_books ub
	          WHERE ub.user_id = ` + strconv.FormatInt(userID, 10) + `
	            AND ub.book_id = ` + books + `.id)`
}

func savedAtSQL(books string, userID int64) string {
	return `(SELECT ub.saved_at FROM user_books ub
	          WHERE ub.user_id = ` + strconv.FormatInt(userID, 10) + `
	            AND ub.book_id = ` + books + `.id)`
}

// FurthestPercentSQL is the progress a "continue reading" sort orders on.
func FurthestPercentSQL(books string, userID int64) string {
	return `(SELECT MAX(p.percent) FROM progress p
	          JOIN editions e ON e.id = p.edition_id
	         WHERE e.book_id = ` + books + `.id
	           AND p.user_id = ` + strconv.FormatInt(userID, 10) + `)`
}
