package db

import (
	"reflect"
	"testing"
)

func TestQueryBasic(t *testing.T) {
	q := NewQuery("books b").Select("b.id", "b.title")
	sql, args := q.SQL()

	if want := "SELECT b.id, b.title FROM books b"; sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want none", args)
	}
}

func TestQueryComposesIndependentFilters(t *testing.T) {
	q := NewQuery("books b").Select("b.id")
	q.Where("b.min_age <= ?", 11)
	q.Where("b.series_id = ?", int64(4))

	sql, args := q.SQL()
	if want := "SELECT b.id FROM books b WHERE b.min_age <= ? AND b.series_id = ?"; sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{11, int64(4)}) {
		t.Errorf("args = %v, want [11 4]", args)
	}
}

// Column, join and where parameters must appear in the order their
// placeholders do, or every argument after the first lands in the wrong slot.
func TestQueryArgumentOrderFollowsSQL(t *testing.T) {
	q := NewQuery("books b")
	q.Select("b.id")
	q.SelectExpr("(b.min_age <= ?) AS unlocked", 11)
	q.Join("JOIN editions e ON e.book_id = b.id AND e.kind = ?", "audiobook")
	q.Where("b.title LIKE ?", "%potter%")

	sql, args := q.SQL()
	want := "SELECT b.id, (b.min_age <= ?) AS unlocked FROM books b " +
		"JOIN editions e ON e.book_id = b.id AND e.kind = ? WHERE b.title LIKE ?"
	if sql != want {
		t.Errorf("SQL = %q,\nwant %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{11, "audiobook", "%potter%"}) {
		t.Errorf("args = %v, want [11 audiobook %%potter%%]", args)
	}
}

func TestQueryOrderGroupAndPagination(t *testing.T) {
	q := NewQuery("books b").Select("b.id")
	q.GroupBy("b.id").OrderBy("b.sort_title ASC").Limit(25).Offset(50)

	sql, _ := q.SQL()
	want := "SELECT b.id FROM books b GROUP BY b.id ORDER BY b.sort_title ASC LIMIT 25 OFFSET 50"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
}

func TestCountSQLKeepsFiltersAndDropsTheRest(t *testing.T) {
	q := NewQuery("books b")
	q.Select("b.id")
	q.SelectExpr("(b.min_age <= ?) AS unlocked", 11)
	q.Where("b.series_id = ?", int64(4))
	q.OrderBy("b.sort_title").Limit(25).Offset(50)

	sql, args := q.CountSQL()
	want := "SELECT COUNT(*) FROM books b WHERE b.series_id = ?"
	if sql != want {
		t.Errorf("CountSQL = %q, want %q", sql, want)
	}
	// The column parameter goes with the column that was dropped.
	if !reflect.DeepEqual(args, []any{int64(4)}) {
		t.Errorf("args = %v, want [4]", args)
	}
}

func TestCountSQLWrapsGroupedQueries(t *testing.T) {
	q := NewQuery("books b").Select("b.id")
	q.Join("JOIN book_genres bg ON bg.book_id = b.id")
	q.Where("bg.genre_id IN (?, ?)", 1, 2)
	q.GroupBy("b.id")

	sql, args := q.CountSQL()
	want := "SELECT COUNT(*) FROM (SELECT * FROM books b " +
		"JOIN book_genres bg ON bg.book_id = b.id WHERE bg.genre_id IN (?, ?) GROUP BY b.id)"
	if sql != want {
		t.Errorf("CountSQL = %q,\nwant %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{1, 2}) {
		t.Errorf("args = %v, want [1 2]", args)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	base := NewQuery("books b").Select("b.id").Where("b.id > ?", 1)

	derived := base.Clone()
	derived.Where("b.min_age <= ?", 11)

	baseSQL, baseArgs := base.SQL()
	derivedSQL, derivedArgs := derived.SQL()

	if baseSQL == derivedSQL {
		t.Error("clone shares clause state with its source")
	}
	if len(baseArgs) != 1 || len(derivedArgs) != 2 {
		t.Errorf("args leaked between clones: base %v, derived %v", baseArgs, derivedArgs)
	}
}
