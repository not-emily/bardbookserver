package db

// EffectiveMinAgeSQL is the SQL twin of model.EffectiveMinAge, taking the
// alias of the books table. It exists in SQL because v1 phase 9 sorts and
// filters on it.
//
// The COUNT comparison is load-bearing: SQLite's MAX ignores NULLs, so a
// plain COALESCE(b.min_age, MAX(s.default_min_age)) fails open for a book
// whose editions include a source with no default set.
func EffectiveMinAgeSQL(books string) string {
	return `COALESCE(` + books + `.min_age, (
		SELECT CASE
			WHEN COUNT(*) > 0 AND COUNT(*) = COUNT(s.default_min_age)
			THEN MAX(s.default_min_age)
			ELSE NULL
		END
		FROM editions e
		JOIN sources s ON s.id = e.source_id
		WHERE e.book_id = ` + books + `.id
	))`
}
