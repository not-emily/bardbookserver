package provider

import "testing"

func TestAnIdentifierIsConclusive(t *testing.T) {
	// The file carries the ten-digit form and the provider the thirteen.
	q := Query{Title: "Something Else Entirely", ISBN: "0-441-17271-7"}
	c := Candidate{Title: "Dune", ISBN: "9780441172719"}

	if got := Score(q, c); got != 1 {
		t.Errorf("ISBN match scored %v, want 1", got)
	}

	asin := Query{Title: "No Match Here", ASIN: "b0036myrqa"}
	if got := Score(asin, Candidate{Title: "Red Rising", ASIN: "B0036MYRQA"}); got != 1 {
		t.Errorf("ASIN match scored %v, want 1", got)
	}
}

func TestATitleMatchesAloneWhenNobodyNamedAnAuthor(t *testing.T) {
	q := Query{Title: "The Hobbit"}
	c := Candidate{Title: "the hobbit"}

	score := Score(q, c)
	if score < MatchThreshold {
		t.Errorf("exact title scored %v, want at least the %v threshold", score, MatchThreshold)
	}
	if _, ok := Best(q, []Candidate{c}); !ok {
		t.Error("an exact title with nothing to contradict it should match")
	}
}

// The important half of scoring: a title can be exact and still be the wrong
// book, and a wrong match takes a person to notice and undo.
func TestADisagreeingAuthorCannotMatch(t *testing.T) {
	q := Query{Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"}}
	c := Candidate{Title: "The Hobbit", Authors: []string{"Some Other Person"}}

	if score := Score(q, c); score >= MatchThreshold {
		t.Errorf("a contradicted author scored %v, which would have matched", score)
	}
	if _, ok := Best(q, []Candidate{c}); ok {
		t.Error("a contradicted author should leave the book unmatched")
	}
}

func TestAnAgreeingAuthorSurvivesADifferentSubtitle(t *testing.T) {
	q := Query{
		Title:   "Red Rising: Book I of the Red Rising Saga",
		Authors: []string{"Pierce Brown"},
	}
	c := Candidate{Title: "Red Rising", Authors: []string{"Brown, Pierce"}}

	if _, ok := Best(q, []Candidate{c}); !ok {
		t.Errorf("subtitled title scored %v, want a match", Score(q, c))
	}
}

// Bard's own path convention yields an author folder that is often a surname
// alone, so requiring the full name would refuse every match in a library laid
// out the way the docs recommend.
func TestASurnameAgreesWithTheFullName(t *testing.T) {
	q := Query{Title: "The Hobbit", Authors: []string{"Tolkien"}}
	c := Candidate{Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"}}

	if _, ok := Best(q, []Candidate{c}); !ok {
		t.Errorf("a surname-only author scored %v, want a match", Score(q, c))
	}
}

func TestAnUnrelatedTitleScoresNothing(t *testing.T) {
	q := Query{Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"}}
	c := Candidate{Title: "Jonathan Strange & Mr Norrell", Authors: []string{"Susanna Clarke"}}

	if got := Score(q, c); got != 0 {
		t.Errorf("unrelated candidate scored %v, want 0", got)
	}
}

// Fix Match shows what the automatic match refused, so Rank must not drop it.
func TestRankOrdersEveryCandidateIncludingTheRejects(t *testing.T) {
	q := Query{Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"}}
	candidates := []Candidate{
		{ID: "wrong", Title: "The Hobbit", Authors: []string{"Nobody At All"}},
		{ID: "right", Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"}},
		{ID: "unrelated", Title: "Dune", Authors: []string{"Frank Herbert"}},
	}

	ranked := Rank(q, candidates)
	if len(ranked) != len(candidates) {
		t.Fatalf("ranked %d candidates, want all %d", len(ranked), len(candidates))
	}
	if ranked[0].ID != "right" {
		t.Errorf("best candidate is %q, want \"right\"", ranked[0].ID)
	}
	if ranked[len(ranked)-1].ID != "unrelated" {
		t.Errorf("worst candidate is %q, want \"unrelated\"", ranked[len(ranked)-1].ID)
	}
	for _, c := range ranked {
		if c.ID != "unrelated" && c.Score == 0 {
			t.Errorf("candidate %q carries no score", c.ID)
		}
	}
}
