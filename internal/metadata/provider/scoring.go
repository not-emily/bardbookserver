package provider

import (
	"slices"
	"sort"
	"strings"

	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/metadata"
)

// MatchThreshold is where a candidate stops being a guess. An exact title with
// no author to confirm it lands exactly here: the file said nothing more, so
// there is nothing further to check.
const MatchThreshold = 0.6

// Score rates a candidate against what the file itself said. An identifier is
// conclusive; otherwise the title carries the match and the author confirms
// it. Normalization is grouping's, so the rule that decides two editions are
// one book is the rule that decides a provider found the right one.
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
		// Both sides named an author and none of them match. That is evidence
		// against, so halving keeps even an exact title below the threshold.
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

	// An ebook and its audiobook are often subtitled differently, which is the
	// same allowance the grouping matcher makes.
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

// namesAgree accepts one name being contained in the other, because Bard's own
// path convention yields an author folder that is often a surname alone where a
// provider returns the full name. Requiring equality would refuse every match
// in a library laid out the way Bard recommends.
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

// Rank scores every candidate and orders them best first. It is what Fix Match
// shows a person, so a candidate below the threshold is kept rather than
// dropped: the automatic match refused it, and the whole point of asking is
// that a person can see it anyway.
func Rank(q Query, candidates []Candidate) []Candidate {
	ranked := make([]Candidate, len(candidates))
	copy(ranked, candidates)

	for i := range ranked {
		ranked[i].Score = Score(q, ranked[i])
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

// Best is the unattended decision: the highest scoring candidate, or none when
// the field is too weak to call. A wrong match is worse than no match, because
// it takes a person to notice and undo it.
func Best(q Query, candidates []Candidate) (Candidate, bool) {
	ranked := Rank(q, candidates)
	if len(ranked) == 0 || ranked[0].Score < MatchThreshold {
		return Candidate{}, false
	}
	return ranked[0], true
}
