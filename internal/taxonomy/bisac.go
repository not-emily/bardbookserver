package taxonomy

import "strings"

// Audience is what a subject string says about who a book is for, which BISAC
// encodes in the heading rather than in a rating.
type Audience string

const (
	AudienceUnknown    Audience = ""
	AudienceChildren   Audience = "children"
	AudienceYoungAdult Audience = "young_adult"
	AudienceAdult      Audience = "adult"
)

// suggestedAges are the ages the audiences suggest, not ages they prove.
// BISAC's JUV covers roughly 0-11 and YAF 12-18, so these are the ages a book
// becomes broadly appropriate at rather than the bottom of each range.
var suggestedAges = map[Audience]int{
	AudienceChildren:   8,
	AudienceYoungAdult: 12,
	AudienceAdult:      18,
}

// AudienceOf reads the BISAC audience out of one subject string, accepting the
// heading text, one part of a heading, and the letter codes some providers
// return instead.
func AudienceOf(subject string) Audience {
	for _, key := range parts(subject) {
		if audience, ok := audienceMarkers[key]; ok {
			return audience
		}
	}
	return audienceFromCode(subject)
}

// audienceFromCode reads the prefix of a BISAC code such as JUV037000. The
// digits are the specific heading, which the alias table covers better.
func audienceFromCode(subject string) Audience {
	code := strings.ToUpper(strings.TrimSpace(subject))
	if len(code) != 9 {
		return AudienceUnknown
	}
	for _, r := range code[3:] {
		if r < '0' || r > '9' {
			return AudienceUnknown
		}
	}

	switch code[:3] {
	case "JUV", "JNF":
		return AudienceChildren
	case "YAF", "YAN":
		return AudienceYoungAdult
	}
	return AudienceUnknown
}

var audienceMarkers = map[string]Audience{
	"juvenile":            AudienceChildren,
	"juvenile fiction":    AudienceChildren,
	"juvenile nonfiction": AudienceChildren,
	"juvenile literature": AudienceChildren,
	"childrens books":     AudienceChildren,
	"childrens fiction":   AudienceChildren,
	"children":            AudienceChildren,
	"childrens":           AudienceChildren,
	"picture books":       AudienceChildren,
	"early readers":       AudienceChildren,
	"middle grade":        AudienceChildren,
	"kids":                AudienceChildren,

	"young adult":            AudienceYoungAdult,
	"young adult fiction":    AudienceYoungAdult,
	"young adult nonfiction": AudienceYoungAdult,
	"teen":                   AudienceYoungAdult,
	"teens":                  AudienceYoungAdult,
	"teenagers":              AudienceYoungAdult,
	"ya":                     AudienceYoungAdult,

	"erotica": AudienceAdult,
	"adult":   AudienceAdult,
}

// SuggestAge derives an age from the audience markers in a book's subjects and
// from a provider's own maturity flag. It is a suggestion for a person to
// confirm: an auto-applied rating fails open, which is the failure the age
// gate exists to prevent.
//
// The strongest signal wins, so a book marked both juvenile and mature
// suggests the higher age rather than the lower.
func SuggestAge(subjects []string, mature *bool) *int {
	best := 0
	if mature != nil && *mature {
		best = suggestedAges[AudienceAdult]
	}

	for _, subject := range subjects {
		if age, ok := suggestedAges[AudienceOf(subject)]; ok && age > best {
			best = age
		}
	}

	if best == 0 {
		return nil
	}
	return &best
}
