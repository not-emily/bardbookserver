package provider

import (
	"slices"
	"sort"
	"strings"

	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/metadata"
)

// MatchThreshold is met exactly by an exact title with no author to confirm
// it, which is all a file naming no author can offer.
const MatchThreshold = 0.6

// Score uses grouping's normalizers, so the rule that decides two editions are
// one book decides whether a provider found the right one.
func Score(q Query, c Candidate) float64 {
	if isbn := metadata.ISBN13(q.ISBN); isbn != "" && isbn == metadata.ISBN13(c.ISBN) {
		return 1
	}
	if q.ASIN != "" && strings.EqualFold(q.ASIN, c.ASIN) {
		return 1
	}

	title := titleScore(q.Title, c.Title)
	if title == 0 {
		return 0
	}

	switch authorAgreement(q.Authors, c.Authors) {
	case authorsAgree:
		return title + 0.4
	case authorsUnknown:
		return title
	default:
		// Halving keeps even an exact title below the threshold: a named author
		// that does not match is evidence against.
		return title / 2
	}
}

func titleScore(want, got string) float64 {
	a, b := grouping.NormalizeTitle(want), grouping.NormalizeTitle(got)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 0.6
	}

	// An ebook and its audiobook are often subtitled differently.
	if grouping.NormalizeTitle(grouping.StripSubtitle(want)) ==
		grouping.NormalizeTitle(grouping.StripSubtitle(got)) {
		return 0.5
	}
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		return 0.35
	}
	return 0
}

type agreement int

const (
	authorsDiffer agreement = iota
	authorsAgree
	authorsUnknown
)

func authorAgreement(want, got []string) agreement {
	ours, theirs := authorTokens(want), authorTokens(got)
	if len(ours) == 0 || len(theirs) == 0 {
		return authorsUnknown
	}

	for _, a := range ours {
		for _, b := range theirs {
			if namesAgree(a, b) {
				return authorsAgree
			}
		}
	}
	return authorsDiffer
}

// namesAgree accepts containment rather than equality, or "Tolkien" from an
// author folder would never match "J. R. R. Tolkien" from a provider.
func namesAgree(a, b []string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(a) == 0 {
		return false
	}
	for _, token := range a {
		if !slices.Contains(b, token) {
			return false
		}
	}
	return true
}

func authorTokens(names []string) [][]string {
	out := make([][]string, 0, len(names))
	for _, name := range names {
		if tokens := strings.Fields(grouping.NormalizeAuthor(name)); len(tokens) > 0 {
			out = append(out, tokens)
		}
	}
	return out
}

// Rank keeps candidates below the threshold, since Fix Match exists to show a
// person what the automatic match refused.
func Rank(q Query, candidates []Candidate) []Candidate {
	ranked := make([]Candidate, len(candidates))
	copy(ranked, candidates)

	for i := range ranked {
		ranked[i].Score = Score(q, ranked[i])
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

// Best declines a weak field rather than guessing: a wrong match takes a
// person to notice and undo, a missed one costs only enrichment.
func Best(q Query, candidates []Candidate) (Candidate, bool) {
	ranked := Rank(q, candidates)
	if len(ranked) == 0 || ranked[0].Score < MatchThreshold {
		return Candidate{}, false
	}
	return ranked[0], true
}
