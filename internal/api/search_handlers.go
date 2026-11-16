package api

import (
	"net/http"
	"strings"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/policy"
)

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if term == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}

	limit, offset, ok := pageParams(w, r)
	if !ok {
		return
	}

	match := db.FTSMatch(term)
	if match == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"books": []bookDTO{}, "total": 0, "limit": limit, "offset": offset,
		})
		return
	}

	q := policy.NewBookScope(CurrentUser(r.Context())).Apply(db.NewSearchQuery(match))
	q, err := db.ApplyBookFilters(q, r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if q, ok = sorted(w, r, q, byRank); !ok {
		return
	}

	s.writeBookPage(w, r, q, limit, offset)
}

func byRank(q *db.Query) *db.Query { return q.OrderBy(db.RankOrder...) }
