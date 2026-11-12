package db

import (
	"strconv"
	"strings"
)

// Query lets policy scopes, facet filters, sorts and pagination each add
// clauses without knowing about the others, so a filtered listing cannot
// bypass the scope limiting it.
//
// Arguments are tracked per section and concatenated in SQL order, keeping a
// parameterised column aligned with its placeholder.
type Query struct {
	table   string
	cols    []string
	colArgs []any

	joins    []string
	joinArgs []any

	wheres    []string
	whereArgs []any

	groups []string
	orders []string

	limit  int
	offset int
}

func NewQuery(table string) *Query {
	return &Query{table: table, limit: -1, offset: -1}
}

func (q *Query) Select(cols ...string) *Query {
	q.cols = append(q.cols, cols...)
	return q
}

func (q *Query) SelectExpr(expr string, args ...any) *Query {
	q.cols = append(q.cols, expr)
	q.colArgs = append(q.colArgs, args...)
	return q
}

func (q *Query) Join(clause string, args ...any) *Query {
	q.joins = append(q.joins, clause)
	q.joinArgs = append(q.joinArgs, args...)
	return q
}

// Where conditions are combined with AND.
func (q *Query) Where(clause string, args ...any) *Query {
	q.wheres = append(q.wheres, clause)
	q.whereArgs = append(q.whereArgs, args...)
	return q
}

func (q *Query) GroupBy(exprs ...string) *Query {
	q.groups = append(q.groups, exprs...)
	return q
}

func (q *Query) OrderBy(exprs ...string) *Query {
	q.orders = append(q.orders, exprs...)
	return q
}

func (q *Query) Limit(n int) *Query {
	q.limit = n
	return q
}

func (q *Query) Offset(n int) *Query {
	q.offset = n
	return q
}

func (q *Query) Clone() *Query {
	return &Query{
		table:     q.table,
		cols:      append([]string(nil), q.cols...),
		colArgs:   append([]any(nil), q.colArgs...),
		joins:     append([]string(nil), q.joins...),
		joinArgs:  append([]any(nil), q.joinArgs...),
		wheres:    append([]string(nil), q.wheres...),
		whereArgs: append([]any(nil), q.whereArgs...),
		groups:    append([]string(nil), q.groups...),
		orders:    append([]string(nil), q.orders...),
		limit:     q.limit,
		offset:    q.offset,
	}
}

func (q *Query) SQL() (string, []any) {
	var b strings.Builder
	args := make([]any, 0, len(q.colArgs)+len(q.joinArgs)+len(q.whereArgs)+2)

	b.WriteString("SELECT ")
	if len(q.cols) == 0 {
		b.WriteString("*")
	} else {
		b.WriteString(strings.Join(q.cols, ", "))
	}
	args = append(args, q.colArgs...)

	b.WriteString(" FROM ")
	b.WriteString(q.table)

	for _, j := range q.joins {
		b.WriteString(" ")
		b.WriteString(j)
	}
	args = append(args, q.joinArgs...)

	if len(q.wheres) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(q.wheres, " AND "))
	}
	args = append(args, q.whereArgs...)

	if len(q.groups) > 0 {
		b.WriteString(" GROUP BY ")
		b.WriteString(strings.Join(q.groups, ", "))
	}
	if len(q.orders) > 0 {
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(q.orders, ", "))
	}
	if q.limit >= 0 {
		b.WriteString(" LIMIT " + strconv.Itoa(q.limit))
	}
	if q.offset > 0 {
		b.WriteString(" OFFSET " + strconv.Itoa(q.offset))
	}

	return b.String(), args
}

// CountSQL drops result columns, ordering and pagination, keeping every filter.
func (q *Query) CountSQL() (string, []any) {
	c := q.Clone()
	c.cols = nil
	c.colArgs = nil
	c.orders = nil
	c.limit = -1
	c.offset = -1

	if len(c.groups) > 0 {
		inner, args := c.SQL()
		return "SELECT COUNT(*) FROM (" + inner + ")", args
	}

	c.Select("COUNT(*)")
	return c.SQL()
}
