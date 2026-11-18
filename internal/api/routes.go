package api

import "net/http"

// access decides which mux a route is registered on. Public is the exception
// and is spelled out per route, so adding one unauthenticated is deliberate
// rather than a side effect of where the line was typed.
type access int

const (
	authenticated access = iota
	public
)

type route struct {
	Method  string
	Path    string
	Access  access
	Handler http.HandlerFunc
}

// apiRoutes is the whole /api/v1 surface in one place. It is a table rather
// than a run of HandleFunc calls because the documentation tests compare it
// against openapi.yaml, and a registration they cannot read is a promise
// nothing checks.
func (s *Server) apiRoutes() []route {
	return []route{
		{"POST", "/api/v1/auth/login", public, s.handleLogin},
		{"POST", "/api/v1/auth/logout", authenticated, s.handleLogout},
		{"GET", "/api/v1/auth/me", authenticated, s.handleMe},
		{"POST", "/api/v1/auth/switch", authenticated, s.handleSwitchProfile},

		{"GET", "/api/v1/devices", authenticated, s.handleListDevices},
		{"DELETE", "/api/v1/devices/{id}", authenticated, s.handleRevokeDevice},

		{"GET", "/api/v1/books", authenticated, s.handleListBooks},
		{"GET", "/api/v1/books/{id}", authenticated, s.handleGetBook},
		{"GET", "/api/v1/books/{id}/cover", authenticated, s.handleBookCover},
		{"GET", "/api/v1/files/{id}/content", authenticated, s.handleFileContent},
		{"GET", "/api/v1/search", authenticated, s.handleSearch},
		{"GET", "/api/v1/facets", authenticated, s.handleListFacets},
		{"GET", "/api/v1/facets/{name}", authenticated, s.handleGetFacet},
		{"GET", "/api/v1/series", authenticated, s.handleListSeries},
		{"GET", "/api/v1/series/{id}", authenticated, s.handleGetSeries},

		{"GET", "/api/v1/collections", authenticated, s.handleListCollections},
		{"GET", "/api/v1/collections/{id}", authenticated, s.handleGetCollection},
		{"POST", "/api/v1/collections", authenticated, s.handleCreateCollection},
		{"PATCH", "/api/v1/collections/{id}", authenticated, s.handleUpdateCollection},
		{"DELETE", "/api/v1/collections/{id}", authenticated, s.handleDeleteCollection},
		{"PUT", "/api/v1/collections/{id}/books/{bookID}", authenticated, s.handleAddToCollection},
		{"DELETE", "/api/v1/collections/{id}/books/{bookID}", authenticated, s.handleRemoveFromCollection},

		{"GET", "/api/v1/editions/{id}/progress", authenticated, s.handleGetProgress},
		{"PUT", "/api/v1/editions/{id}/progress", authenticated, s.handleSetProgress},
		{"PUT", "/api/v1/books/{id}/saved", authenticated, s.handleSaveBook},
		{"DELETE", "/api/v1/books/{id}/saved", authenticated, s.handleUnsaveBook},
		{"PUT", "/api/v1/books/{id}/rating", authenticated, s.handleSetRating},
		{"DELETE", "/api/v1/books/{id}/rating", authenticated, s.handleClearRating},
		{"PUT", "/api/v1/books/{id}/state", authenticated, s.handleSetState},
		{"GET", "/api/v1/history", authenticated, s.handleHistory},
		{"GET", "/api/v1/sync", authenticated, s.handleSync},

		{"POST", "/api/v1/editions/{id}/handoff", authenticated, s.handleHandoff},
		{"GET", "/api/v1/books/{id}/handoff", authenticated, s.handleBookHandoffTargets},

		{"GET", "/api/v1/annotations", authenticated, s.handleListAnnotations},
		{"GET", "/api/v1/annotations/changes", authenticated, s.handleAnnotationChanges},
		{"PUT", "/api/v1/annotations/{uuid}", authenticated, s.handleSaveAnnotation},
		{"DELETE", "/api/v1/annotations/{uuid}", authenticated, s.handleDeleteAnnotation},

		{"POST", "/api/v1/uploads", authenticated, s.handleCreateUpload},
		{"GET", "/api/v1/uploads/{id}", authenticated, s.handleUploadStatus},
		{"PUT", "/api/v1/uploads/{id}/chunks/{offset}", authenticated, s.handleUploadChunk},
		{"POST", "/api/v1/uploads/{id}/finalize", authenticated, s.handleFinalizeUpload},

		{"GET", "/api/v1/users/{id}/overrides", authenticated, s.handleListOverrides},
		{"PUT", "/api/v1/users/{id}/overrides/{bookID}", authenticated, s.handleSetOverride},
		{"DELETE", "/api/v1/users/{id}/overrides/{bookID}", authenticated, s.handleClearOverride},

		{"GET", "/api/v1/books/{id}/match", authenticated, s.handleSearchMatches},
		{"POST", "/api/v1/books/{id}/match", authenticated, s.handleApplyMatch},
		{"DELETE", "/api/v1/books/{id}/match", authenticated, s.handleClearMatch},
		{"GET", "/api/v1/groupings/uncertain", authenticated, s.handleListUncertainGroupings},
		{"POST", "/api/v1/editions/{id}/group", authenticated, s.handleMergeEdition},
		{"DELETE", "/api/v1/editions/{id}/group", authenticated, s.handleSplitEdition},
	}
}

// Operation is a method and path, which is the unit openapi.yaml documents.
type Operation struct {
	Method string
	Path   string
}

func (o Operation) String() string { return o.Method + " " + o.Path }

// APIOperations is the surface the documentation must cover. The handlers are
// never called, so a zero Server is enough to enumerate it.
func APIOperations() []Operation {
	routes := (&Server{}).apiRoutes()

	out := make([]Operation, 0, len(routes))
	for _, r := range routes {
		out = append(out, Operation{Method: r.Method, Path: r.Path})
	}
	return out
}
