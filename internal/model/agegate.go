package model

// EffectiveMinAge resolves the age required to read a book, inheriting the
// most restrictive source default when the book sets none.
//
// nil means unknown and denies, so every undecided path fails closed.
// db.EffectiveMinAgeSQL is the SQL twin, held to this one by
// TestEffectiveMinAgeMatchesSQL.
func EffectiveMinAge(bookMinAge *int, sourceDefaults []*int) *int {
	if bookMinAge != nil {
		return bookMinAge
	}
	if len(sourceDefaults) == 0 {
		return nil
	}

	max := 0
	for _, d := range sourceDefaults {
		if d == nil {
			return nil
		}
		if *d > max {
			max = *d
		}
	}
	return &max
}
