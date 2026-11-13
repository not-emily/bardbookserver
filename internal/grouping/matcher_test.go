package grouping

import "testing"

func TestKeysCarryTitleAndAuthor(t *testing.T) {
	keys := Candidate{
		Title:   "The Fellowship of the Ring",
		Authors: []string{"J.R.R. Tolkien"},
	}.Keys()

	if keys.Exact != "fellowship of the ring|jrr tolkien" {
		t.Errorf("exact key %q", keys.Exact)
	}
	if keys.Identifier != "" {
		t.Errorf("identifier %q, want none", keys.Identifier)
	}
	// A title with no subtitle still offers a loose key, so a subtitled
	// edition of the same book has something to find.
	if keys.Loose != keys.Exact {
		t.Errorf("loose key %q, want it to repeat the exact key %q", keys.Loose, keys.Exact)
	}
}

func TestKeysPreferISBNAsTheIdentifier(t *testing.T) {
	keys := Candidate{Title: "Dune", Authors: []string{"Frank Herbert"},
		ISBN: "9780441172719", ASIN: "B000R34YKC"}.Keys()
	if keys.Identifier != "isbn:9780441172719" {
		t.Errorf("identifier %q, want the isbn", keys.Identifier)
	}

	keys = Candidate{Title: "Dune", Authors: []string{"Frank Herbert"},
		ASIN: "B000R34YKC"}.Keys()
	if keys.Identifier != "asin:B000R34YKC" {
		t.Errorf("identifier %q, want the asin when there is no isbn", keys.Identifier)
	}
}

// An ebook and its audiobook are often subtitled differently, which is the one
// case worth a second, looser attempt.
func TestKeysOfferALooseKeyWhenASubtitleExists(t *testing.T) {
	withSubtitle := Candidate{Title: "Dune: A Novel", Authors: []string{"Frank Herbert"}}.Keys()
	without := Candidate{Title: "Dune", Authors: []string{"Frank Herbert"}}.Keys()

	if withSubtitle.Exact == without.Exact {
		t.Error("the subtitle made no difference to the exact key")
	}
	if withSubtitle.Loose != without.Exact {
		t.Errorf("loose key %q, want it to equal the unsubtitled exact key %q",
			withSubtitle.Loose, without.Exact)
	}
}

// Two different books called "Collected Poems" would merge on title alone, and
// an over-eager merge is the expensive mistake.
func TestKeysRefuseToMatchOnTitleAlone(t *testing.T) {
	keys := Candidate{Title: "Collected Poems"}.Keys()
	if keys.Exact != "" || keys.Loose != "" {
		t.Errorf("an authorless edition offered title keys: %+v", keys)
	}

	// It can still be grouped by identifier, which names one manifestation.
	keys = Candidate{Title: "Collected Poems", ISBN: "9780441172719"}.Keys()
	if keys.Identifier == "" {
		t.Error("an authorless edition with an isbn offered no identifier")
	}
}

func TestKeysOfAnEmptyCandidate(t *testing.T) {
	if keys := (Candidate{}).Keys(); keys != (Keys{}) {
		t.Errorf("empty candidate offered %+v", keys)
	}
}

func TestKeysSkipBlankAuthors(t *testing.T) {
	keys := Candidate{Title: "Dune", Authors: []string{"", "  ", "Frank Herbert"}}.Keys()
	if keys.Exact != "dune|frank herbert" {
		t.Errorf("exact key %q, want the first real author used", keys.Exact)
	}
}

func TestConfidenceOrdering(t *testing.T) {
	if Confidence(ReasonIdentifier) <= Confidence(ReasonExactTitle) {
		t.Error("an identifier match is not more certain than a title match")
	}
	if Confidence(ReasonExactTitle) <= Confidence(ReasonLooseTitle) {
		t.Error("an exact title match is not more certain than a loose one")
	}
	if Confidence(ReasonLooseTitle) >= CertainEnough {
		t.Error("a loose match is certain enough to skip review, which defeats the queue")
	}
	if Confidence(ReasonExactTitle) < CertainEnough {
		t.Error("an exact title match would be queued for review, which would flood it")
	}
	// A book of its own is not a guess about anything.
	if Confidence(ReasonNew) < CertainEnough {
		t.Error("a new book would be queued for review")
	}
}

func TestEditionKeyPrefersAnIdentifier(t *testing.T) {
	if got := EditionKey("isbn:123", "Tolkien/fotr.epub"); got != "isbn:123" {
		t.Errorf("EditionKey = %q, want the identifier", got)
	}
	if got := EditionKey("", "Tolkien/fotr.epub"); got != "path:Tolkien/fotr.epub" {
		t.Errorf("EditionKey = %q, want the relative path", got)
	}
}

func TestRelativeKeyStaysRelative(t *testing.T) {
	if got := RelativeKey("/library", "/library/Tolkien/fotr.epub"); got != "Tolkien/fotr.epub" {
		t.Errorf("RelativeKey = %q", got)
	}
	// A path outside the root cannot be made relative, so it is used whole
	// rather than turned into a string of parent hops.
	if got := RelativeKey("/library", "/elsewhere/fotr.epub"); got != "/elsewhere/fotr.epub" {
		t.Errorf("RelativeKey = %q, want the absolute path", got)
	}
}

func TestNewGroupKeyIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		key, err := NewGroupKey()
		if err != nil {
			t.Fatal(err)
		}
		if key == "" {
			t.Fatal("empty group key")
		}
		if seen[key] {
			t.Fatalf("group key %q repeated", key)
		}
		seen[key] = true
	}
}
