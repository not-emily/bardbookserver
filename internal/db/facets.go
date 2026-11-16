package db

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Facets filter with EXISTS rather than a join. A book with two matching
// editions has to appear once: a duplicated row inflates the counts and
// breaks pagination, which is the same class of bug as post-filtering.

type FacetValue struct {
	Value string
	Label string
	Count int
}

type facet struct {
	where func(values []string) (string, []any)

	// list counts the facet's values over an already-scoped set of book ids,
	// yielding value, label and count.
	list func(bookIDs string, args []any) (string, []any)
}

// reservedParams are listing controls rather than facets, so an unknown
// parameter can be reported instead of silently filtering nothing.
var reservedParams = map[string]bool{
	"limit":  true,
	"offset": true,
	"sort":   true,
	"q":      true,
}

// publicationYear takes the earliest year any edition claims.
const publicationYear = `(SELECT MIN(substr(e.published_on, 1, 4)) FROM editions e
	WHERE e.book_id = ` + BooksAlias + `.id AND e.published_on IS NOT NULL
	  AND length(e.published_on) >= 4)`

var bookFacets = map[string]facet{
	"format":   editionColumnFacet("format"),
	"kind":     editionColumnFacet("kind"),
	"language": editionColumnFacet("language"),

	"genre": {
		where: func(values []string) (string, []any) {
			return `EXISTS (SELECT 1 FROM book_genres bg
			          JOIN genres g ON g.id = bg.genre_id
			         WHERE bg.book_id = ` + BooksAlias + `.id
			           AND g.slug IN (` + inList(len(values)) + `))`, anyOf(values)
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT g.slug, g.name, COUNT(DISTINCT bg.book_id)
			          FROM book_genres bg JOIN genres g ON g.id = bg.genre_id
			         WHERE bg.book_id IN (` + bookIDs + `)
			         GROUP BY g.id ORDER BY g.name`, args
		},
	},

	"author": {
		where: func(values []string) (string, []any) {
			return `EXISTS (SELECT 1 FROM contributions ct
			         WHERE ct.book_id = ` + BooksAlias + `.id AND ct.role = 'author'
			           AND ct.contributor_id IN (` + inList(len(values)) + `))`, anyOf(values)
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT CAST(c.id AS TEXT), c.name, COUNT(DISTINCT ct.book_id)
			          FROM contributions ct JOIN contributors c ON c.id = ct.contributor_id
			         WHERE ct.book_id IN (` + bookIDs + `) AND ct.role = 'author'
			         GROUP BY c.id ORDER BY c.sort_name`, args
		},
	},

	"narrator": {
		where: func(values []string) (string, []any) {
			return `EXISTS (SELECT 1 FROM contributions ct
			          JOIN editions e ON e.id = ct.edition_id
			         WHERE e.book_id = ` + BooksAlias + `.id AND ct.role = 'narrator'
			           AND ct.contributor_id IN (` + inList(len(values)) + `))`, anyOf(values)
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT CAST(c.id AS TEXT), c.name, COUNT(DISTINCT e.book_id)
			          FROM contributions ct
			          JOIN contributors c ON c.id = ct.contributor_id
			          JOIN editions e ON e.id = ct.edition_id
			         WHERE e.book_id IN (` + bookIDs + `) AND ct.role = 'narrator'
			         GROUP BY c.id ORDER BY c.sort_name`, args
		},
	},

	"series": {
		where: func(values []string) (string, []any) {
			return BooksAlias + `.series_id IN (` + inList(len(values)) + `)`, anyOf(values)
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT CAST(s.id AS TEXT), s.name, COUNT(*)
			          FROM books bk JOIN series s ON s.id = bk.series_id
			         WHERE bk.id IN (` + bookIDs + `)
			         GROUP BY s.id ORDER BY s.sort_name`, args
		},
	},

	"year": {
		where: func(values []string) (string, []any) {
			return publicationYear + ` IN (` + inList(len(values)) + `)`, anyOf(values)
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT year, year, COUNT(*) FROM (
			          SELECT ` + publicationYear + ` AS year FROM books ` + BooksAlias + `
			           WHERE ` + BooksAlias + `.id IN (` + bookIDs + `))
			         WHERE year IS NOT NULL
			         GROUP BY year ORDER BY year DESC`, args
		},
	},

	"length": {
		where: func(values []string) (string, []any) {
			var (
				clauses []string
				args    []any
			)
			for _, value := range values {
				bucket, ok := lengthBucketFor(value)
				if !ok {
					continue
				}
				if bucket.Max == 0 {
					clauses = append(clauses, LengthSeconds+" >= ?")
					args = append(args, bucket.Min)
					continue
				}
				clauses = append(clauses, "("+LengthSeconds+" >= ? AND "+LengthSeconds+" < ?)")
				args = append(args, bucket.Min, bucket.Max)
			}
			if len(clauses) == 0 {
				return "1 = 0", nil
			}
			return "(" + strings.Join(clauses, " OR ") + ")", args
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT value, label, COUNT(*) FROM (
			          SELECT ` + lengthCase(false) + ` AS value,
			                 ` + lengthCase(true) + ` AS label,
			                 ` + lengthOrderCase() + ` AS ord
			            FROM books ` + BooksAlias + `
			           WHERE ` + BooksAlias + `.id IN (` + bookIDs + `))
			         WHERE value IS NOT NULL
			         GROUP BY value, label, ord ORDER BY ord`, args
		},
	},
}

// Max 0 means no upper bound.
type lengthBucket struct {
	Key, Label string
	Min, Max   float64
}

var lengthBuckets = []lengthBucket{
	{"short", "Under 5 hours", 0, 5 * 3600},
	{"medium", "5 to 12 hours", 5 * 3600, 12 * 3600},
	{"long", "12 to 25 hours", 12 * 3600, 25 * 3600},
	{"epic", "Over 25 hours", 25 * 3600, 0},
}

func lengthBucketFor(key string) (lengthBucket, bool) {
	for _, bucket := range lengthBuckets {
		if bucket.Key == key {
			return bucket, true
		}
	}
	return lengthBucket{}, false
}

// lengthCase and lengthOrderCase are generated from lengthBuckets so the
// filter, the labels and the ordering cannot drift apart.
func lengthCase(label bool) string {
	var b strings.Builder
	b.WriteString("CASE WHEN " + LengthSeconds + " IS NULL THEN NULL")
	for _, bucket := range lengthBuckets {
		out := bucket.Key
		if label {
			out = bucket.Label
		}
		if bucket.Max == 0 {
			b.WriteString(" ELSE '" + out + "'")
			continue
		}
		b.WriteString(fmt.Sprintf(" WHEN %s < %d THEN '%s'", LengthSeconds, int64(bucket.Max), out))
	}
	b.WriteString(" END")
	return b.String()
}

func lengthOrderCase() string {
	var b strings.Builder
	b.WriteString("CASE")
	for i, bucket := range lengthBuckets {
		if bucket.Max == 0 {
			b.WriteString(fmt.Sprintf(" ELSE %d", i))
			continue
		}
		b.WriteString(fmt.Sprintf(" WHEN %s < %d THEN %d", LengthSeconds, int64(bucket.Max), i))
	}
	b.WriteString(" END")
	return b.String()
}

func editionColumnFacet(column string) facet {
	return facet{
		where: func(values []string) (string, []any) {
			return `EXISTS (SELECT 1 FROM editions e
			         WHERE e.book_id = ` + BooksAlias + `.id
			           AND e.` + column + ` IN (` + inList(len(values)) + `))`, anyOf(values)
		},
		list: func(bookIDs string, args []any) (string, []any) {
			return `SELECT e.` + column + `, e.` + column + `, COUNT(DISTINCT e.book_id)
			          FROM editions e
			         WHERE e.book_id IN (` + bookIDs + `)
			           AND e.` + column + ` IS NOT NULL AND e.` + column + ` <> ''
			         GROUP BY e.` + column + ` ORDER BY e.` + column, args
		},
	}
}

func FacetNames() []string {
	out := make([]string, 0, len(bookFacets))
	for name := range bookFacets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ApplyBookFilters composes every facet present in params onto q. Values of
// one facet are OR'd, separate facets are AND'd.
func ApplyBookFilters(q *Query, params url.Values) (*Query, error) {
	for _, name := range FacetNames() {
		values := nonEmpty(params[name])
		if len(values) == 0 {
			continue
		}
		clause, args := bookFacets[name].where(values)
		q = q.Where(clause, args...)
	}

	for name := range params {
		if _, ok := bookFacets[name]; !ok && !reservedParams[name] {
			return nil, fmt.Errorf("unknown filter %q", name)
		}
	}
	return q, nil
}

// FacetValues counts one facet over the books q already permits, so a count
// cannot report a book the caller may not see.
func (d *DB) FacetValues(ctx context.Context, q *Query, name string) ([]FacetValue, error) {
	f, ok := bookFacets[name]
	if !ok {
		return nil, fmt.Errorf("unknown facet %q", name)
	}

	bookIDs, idArgs := q.IDsSQL(BooksAlias + ".id")
	query, args := f.list(bookIDs, idArgs)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s facet: %w", name, err)
	}
	defer rows.Close()

	out := make([]FacetValue, 0, 16)
	for rows.Next() {
		var v FacetValue
		if err := rows.Scan(&v.Value, &v.Label, &v.Count); err != nil {
			return nil, fmt.Errorf("scan %s facet value: %w", name, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) AllFacetValues(ctx context.Context, q *Query) (map[string][]FacetValue, error) {
	out := make(map[string][]FacetValue, len(bookFacets))
	for _, name := range FacetNames() {
		values, err := d.FacetValues(ctx, q, name)
		if err != nil {
			return nil, err
		}
		out[name] = values
	}
	return out, nil
}

func inList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func anyOf(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
