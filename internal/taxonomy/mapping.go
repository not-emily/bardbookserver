package taxonomy

import (
	"strings"
	"unicode"
)

// Key is the normalised form a mapping is stored under, so one entered as
// "Fantasy Fiction" also catches "fantasy fiction".
func Key(subject string) string { return normalize(subject) }

// Classifier adds the mappings an admin entered to the built-in table. A
// mapping they made wins, since it exists to correct what the table got wrong.
type Classifier struct {
	extra map[string]string
}

func NewClassifier(extra map[string]string) Classifier {
	return Classifier{extra: extra}
}

// Classify with no admin mappings, for callers that have none.
func Classify(subjects []string) (slugs, raw []string) {
	return Classifier{}.Classify(subjects)
}

// Classify sorts subject strings into canonical genre slugs and the rest. The
// rest is returned rather than discarded, so a gap in the alias table degrades
// the facet instead of losing data.
func (c Classifier) Classify(subjects []string) (slugs, raw []string) {
	seenSlug := map[string]bool{}
	seenRaw := map[string]bool{}

	for _, subject := range subjects {
		matched := false
		for _, key := range parts(subject) {
			if noise[key] {
				continue
			}
			slug, ok := c.lookup(key)
			if !ok {
				continue
			}
			matched = true
			if !seenSlug[slug] {
				seenSlug[slug] = true
				slugs = append(slugs, slug)
			}
		}

		// Kept whole rather than per part, so a heading nothing understood
		// stays readable to whoever extends the table.
		if key := normalize(subject); !matched && key != "" && !noise[key] && !seenRaw[key] {
			seenRaw[key] = true
			raw = append(raw, strings.TrimSpace(subject))
		}
	}
	return slugs, raw
}

// parts splits a BISAC heading, because "FICTION / Fantasy / Epic" arrives
// whole from some providers and already split from others.
func parts(subject string) []string {
	split := strings.Split(subject, "/")
	out := make([]string, 0, len(split))

	for _, part := range split {
		if key := normalize(part); key != "" {
			out = append(out, key)
		}
	}
	return out
}

// lookup tries the alias table, then the same string without the genre words
// providers tack on. "science fiction" is an alias, so it is matched before
// anything can strip "fiction" off it.
func (c Classifier) lookup(key string) (string, bool) {
	if slug, ok := c.extra[key]; ok {
		if _, known := BySlug(slug); known {
			return slug, true
		}
	}
	if slug, ok := aliases[key]; ok {
		return slug, true
	}

	for _, suffix := range []string{
		" fiction", " nonfiction", " stories", " novels", " novel", " books",
	} {
		stripped, found := strings.CutSuffix(key, suffix)
		if !found || stripped == "" {
			continue
		}
		if slug, ok := c.extra[stripped]; ok {
			if _, known := BySlug(slug); known {
				return slug, true
			}
		}
		if slug, ok := aliases[stripped]; ok {
			return slug, true
		}
	}
	return "", false
}

// normalize folds the punctuation and conjunctions providers disagree about,
// so "Comics & Graphic Novels" and "comics and graphic novels" are one key.
func normalize(subject string) string {
	lower := strings.ToLower(strings.TrimSpace(subject))
	lower = strings.ReplaceAll(lower, "&", " and ")

	var b strings.Builder
	for _, r := range lower {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ':
			b.WriteRune(r)
		case r == '-' || r == '/':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// noise is dropped rather than kept: these say nothing about a book and would
// otherwise attach to every row in raw_subjects.
var noise = map[string]bool{
	"fiction":                   true,
	"nonfiction":                true,
	"non fiction":               true,
	"general":                   true,
	"books":                     true,
	"ebooks":                    true,
	"audiobooks":                true,
	"texts":                     true,
	"literature":                true,
	"literature and fiction":    true,
	"accessible book":           true,
	"protected daisy":           true,
	"in library":                true,
	"overdrive":                 true,
	"large type books":          true,
	"new york times bestseller": true,
}

// aliases maps what providers actually say onto the canonical list. The same
// book arrives as "FICTION / Fantasy / Epic", "Fantasy fiction", "fantasy" and
// "Juvenile Fiction / Fantasy & Magic" depending on where it came from, which
// is why this table exists and why it will keep growing.
var aliases = map[string]string{
	"adventure":                 "adventure",
	"adventure and adventurers": "adventure",
	"action and adventure":      "adventure",
	"sea stories":               "adventure",
	"survival":                  "adventure",

	"art":                 "art",
	"art and photography": "art",
	"architecture":        "art",
	"design":              "art",
	"photography":         "art",

	"biography":                   "biography",
	"biography and autobiography": "biography",
	"biographies":                 "biography",
	"biographies and memoirs":     "biography",
	"autobiography":               "biography",

	"business":               "business",
	"business and economics": "business",
	"business and money":     "business",
	"management":             "business",
	"entrepreneurship":       "business",
	"marketing":              "business",

	"classics":             "classics",
	"classic literature":   "classics",
	"literary collections": "classics",

	"comics":                    "comics",
	"comics and graphic novels": "comics",
	"graphic novels":            "comics",
	"manga":                     "comics",

	"cooking":        "cooking",
	"cookbooks":      "cooking",
	"food and drink": "cooking",
	"food and wine":  "cooking",

	"crime":                 "crime",
	"detective":             "crime",
	"detective and mystery": "crime",
	"police procedural":     "crime",
	"noir":                  "crime",

	"drama":           "drama",
	"plays":           "drama",
	"theatre":         "drama",
	"performing arts": "drama",

	"dystopian":        "dystopian",
	"dystopias":        "dystopian",
	"post apocalyptic": "dystopian",
	"apocalyptic":      "dystopian",

	"economics": "economics",
	"finance":   "economics",
	"investing": "economics",

	"education":  "education",
	"study aids": "education",
	"teaching":   "education",

	"essays":             "essays",
	"literary criticism": "essays",
	"criticism":          "essays",

	"fantasy":           "fantasy",
	"epic fantasy":      "fantasy",
	"high fantasy":      "fantasy",
	"urban fantasy":     "fantasy",
	"magic":             "fantasy",
	"fantasy and magic": "fantasy",
	"wizards":           "fantasy",
	"dragons":           "fantasy",
	"sword and sorcery": "fantasy",

	"health":              "health",
	"health and wellness": "health",
	"fitness":             "health",
	"medical":             "health",
	"diet":                "health",
	"mindfulness":         "health",

	"historical":          "historical-fiction",
	"historical fiction":  "historical-fiction",
	"alternative history": "historical-fiction",

	"history":          "history",
	"world history":    "history",
	"ancient history":  "history",
	"military history": "history",
	"archaeology":      "history",

	"horror":        "horror",
	"ghost":         "horror",
	"ghost stories": "horror",
	"supernatural":  "horror",
	"vampires":      "horror",

	"humour": "humour",
	"humor":  "humour",
	"comedy": "humour",
	"satire": "humour",

	"language":                      "language",
	"language arts and disciplines": "language",
	"linguistics":                   "language",
	"foreign language study":        "language",
	"writing":                       "language",

	"literary fiction":     "literary-fiction",
	"literary":             "literary-fiction",
	"contemporary fiction": "literary-fiction",
	"contemporary":         "literary-fiction",

	"memoir":           "memoir",
	"memoirs":          "memoir",
	"personal memoirs": "memoir",
	"diaries":          "memoir",

	"music":     "music",
	"composers": "music",
	"musicians": "music",

	"mystery":                       "mystery",
	"mysteries":                     "mystery",
	"mystery and detective":         "mystery",
	"mystery thriller and suspense": "mystery",
	"whodunit":                      "mystery",
	"cozy mystery":                  "mystery",

	"mythology":         "mythology",
	"myths":             "mythology",
	"myths and legends": "mythology",
	"folklore":          "mythology",
	"fairy tales":       "mythology",
	"legends":           "mythology",

	"nature":             "nature",
	"nature and ecology": "nature",
	"environment":        "nature",
	"animals":            "nature",
	"gardening":          "nature",

	"philosophy": "philosophy",
	"ethics":     "philosophy",
	"logic":      "philosophy",

	"poetry": "poetry",
	"poems":  "poetry",
	"verse":  "poetry",

	"politics":                     "politics",
	"politics and social sciences": "politics",
	"political science":            "politics",
	"government":                   "politics",
	"social science":               "politics",
	"sociology":                    "politics",

	"psychology":        "psychology",
	"mental health":     "psychology",
	"neuroscience":      "psychology",
	"cognitive science": "psychology",

	"reference":     "reference",
	"encyclopedias": "reference",
	"dictionaries":  "reference",
	"almanacs":      "reference",

	"religion":                  "religion",
	"religion and spirituality": "religion",
	"spirituality":              "religion",
	"theology":                  "religion",
	"christianity":              "religion",
	"buddhism":                  "religion",
	"islam":                     "religion",
	"judaism":                   "religion",

	"romance":         "romance",
	"love stories":    "romance",
	"romantic":        "romance",
	"romantic comedy": "romance",

	"science":          "science",
	"physics":          "science",
	"biology":          "science",
	"chemistry":        "science",
	"astronomy":        "science",
	"mathematics":      "science",
	"science and math": "science",

	"science fiction":             "science-fiction",
	"sci fi":                      "science-fiction",
	"scifi":                       "science-fiction",
	"science fiction and fantasy": "science-fiction",
	"space opera":                 "science-fiction",
	"cyberpunk":                   "science-fiction",
	"time travel":                 "science-fiction",
	"hard science fiction":        "science-fiction",

	"self help":            "self-help",
	"personal growth":      "self-help",
	"personal development": "self-help",
	"motivational":         "self-help",
	"productivity":         "self-help",

	"short stories": "short-stories",
	"anthologies":   "short-stories",
	"collections":   "short-stories",
	"anthology":     "short-stories",

	"sports":              "sports",
	"sports and outdoors": "sports",
	"games":               "sports",

	"technology":                 "technology",
	"computers":                  "technology",
	"computer science":           "technology",
	"engineering":                "technology",
	"programming":                "technology",
	"artificial intelligence":    "technology",
	"technology and engineering": "technology",

	"thriller":               "thriller",
	"thrillers":              "thriller",
	"suspense":               "thriller",
	"espionage":              "thriller",
	"spy stories":            "thriller",
	"psychological thriller": "thriller",
	"legal thriller":         "thriller",

	"travel":         "travel",
	"travel writing": "travel",
	"exploration":    "travel",

	"true crime": "true-crime",

	"war":              "war",
	"war stories":      "war",
	"military":         "war",
	"world war ii":     "war",
	"world war 2":      "war",
	"military fiction": "war",

	"western":  "western",
	"westerns": "western",
}
