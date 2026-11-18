package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/not-emily/bardbookserver/internal/docs"
)

// The drift test that matters: real responses from the real handlers, checked
// against the schemas the spec claims for them. A fixture compared with a
// hand-written schema would only prove one person was consistent twice.

// jsonValue re-decodes through encoding/json, since a validator works on JSON
// types and the YAML gives Go ones.
func jsonValue(t *testing.T, v any) any {
	t.Helper()

	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("not representable as JSON: %v", err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func compile(t *testing.T, schema map[string]any) *jsonschema.Schema {
	t.Helper()

	const id = "https://bard.test/schema"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, jsonValue(t, schema)); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	compiled, err := compiler.Compile(id)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return compiled
}

// assertMatches is the whole check: this response, against what the spec
// promises for this operation and status.
func assertMatches(t *testing.T, spec *docs.Spec, method, path string, res *httptest.ResponseRecorder) {
	t.Helper()

	status := strconv.Itoa(res.Code)
	if !spec.DocumentsStatus(method, path, status) {
		t.Errorf("%s %s returned %s, which the spec does not document", method, path, status)
		return
	}

	schema, ok := spec.ResponseSchema(method, path, status)
	if !ok {
		if body := bytes.TrimSpace(res.Body.Bytes()); len(body) > 0 {
			t.Errorf("%s %s -> %s documents no JSON body but sent %s", method, path, status, body)
		}
		return
	}

	var body any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Errorf("%s %s -> %s is not JSON: %v", method, path, status, err)
		return
	}

	if err := compile(t, schema).Validate(body); err != nil {
		t.Errorf("%s %s -> %s does not match its schema:\n%v", method, path, status, err)
	}
}

func TestDocumentedResponsesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)

	h := newHarness(t)
	lib := h.seedLibrary()
	seriesID := h.oneValue(`SELECT id FROM series LIMIT 1`)

	// Every GET whose response the spec describes, driven for real.
	cases := []struct{ path, specPath string }{
		{"/api/v1/auth/me", "/api/v1/auth/me"},
		{"/api/v1/devices", "/api/v1/devices"},
		{"/api/v1/books?limit=2", "/api/v1/books"},
		{fmt.Sprintf("/api/v1/books/%d", lib.dune), "/api/v1/books/{id}"},
		{"/api/v1/search?q=dune", "/api/v1/search"},
		{"/api/v1/facets", "/api/v1/facets"},
		{"/api/v1/facets/genre", "/api/v1/facets/{name}"},
		{"/api/v1/series", "/api/v1/series"},
		{fmt.Sprintf("/api/v1/series/%d", seriesID), "/api/v1/series/{id}"},
	}
	for _, tc := range cases {
		assertMatches(t, spec, "GET", tc.specPath, h.get(tc.path))
	}
}

// The private-space endpoints need state before they have a shape worth
// checking, so this drives them rather than reading them cold.
func TestReadingResponsesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	h := newHarness(t)
	lib := h.seedLibrary()

	progress := fmt.Sprintf("/api/v1/editions/%d/progress", lib.duneEPUB)
	assertMatches(t, spec, "PUT", "/api/v1/editions/{id}/progress",
		h.sendJSON("PUT", progress, h.token, map[string]any{
			"cfi": "epubcfi(/6/14!/4/2/2/1:0)", "page": 120, "percent": 0.42, "client_seq": 1,
		}))
	assertMatches(t, spec, "GET", "/api/v1/editions/{id}/progress", h.get(progress))

	// Divergence changes the shape, so it is checked rather than assumed to
	// be the same object with a flag flipped.
	other := h.addDevice("Second device")
	assertMatches(t, spec, "PUT", "/api/v1/editions/{id}/progress",
		h.sendJSON("PUT", progress, other, map[string]any{
			"cfi": "epubcfi(/6/2!/4/2/2/1:0)", "page": 8, "percent": 0.04, "client_seq": 1,
		}))

	saved := fmt.Sprintf("/api/v1/books/%d/saved", lib.dune)
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/saved", h.sendJSON("PUT", saved, h.token, nil))
	assertMatches(t, spec, "DELETE", "/api/v1/books/{id}/saved", h.sendJSON("DELETE", saved, h.token, nil))

	rating := fmt.Sprintf("/api/v1/books/%d/rating", lib.dune)
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/rating",
		h.sendJSON("PUT", rating, h.token, map[string]any{"rating": 4}))
	assertMatches(t, spec, "DELETE", "/api/v1/books/{id}/rating", h.sendJSON("DELETE", rating, h.token, nil))

	state := fmt.Sprintf("/api/v1/books/%d/state", lib.dune)
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/state",
		h.sendJSON("PUT", state, h.token, map[string]any{"state": "finished"}))
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/state",
		h.sendJSON("PUT", state, h.token, map[string]any{"state": nil}))

	assertMatches(t, spec, "GET", "/api/v1/history", h.get("/api/v1/history"))
	assertMatches(t, spec, "GET", "/api/v1/sync", h.get("/api/v1/sync"))

	assertMatches(t, spec, "GET", "/api/v1/books/{id}/handoff",
		h.get(fmt.Sprintf("/api/v1/books/%d/handoff", lib.dune)))
	assertMatches(t, spec, "POST", "/api/v1/editions/{id}/handoff",
		h.sendJSON("POST", fmt.Sprintf("/api/v1/editions/%d/handoff", lib.duneEPUB), h.token,
			map[string]any{"to_edition_id": lib.duneM4B, "percent": 0.42}))
}

func TestReadingErrorsMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	h := newHarness(t)
	lib := h.seedLibrary()

	assertMatches(t, spec, "GET", "/api/v1/editions/{id}/progress",
		h.get("/api/v1/editions/99999/progress"))
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/rating",
		h.sendJSON("PUT", fmt.Sprintf("/api/v1/books/%d/rating", lib.dune), h.token,
			map[string]any{"rating": 99}))
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/state",
		h.sendJSON("PUT", fmt.Sprintf("/api/v1/books/%d/state", lib.dune), h.token,
			map[string]any{"state": "sideways"}))
	assertMatches(t, spec, "PUT", "/api/v1/books/{id}/saved",
		h.sendJSON("PUT", "/api/v1/books/99999/saved", h.token, nil))
}

func TestCurationResponsesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	h := newHarness(t)
	lib := h.seedLibrary()

	assertMatches(t, spec, "POST", "/api/v1/collections",
		h.sendJSON("POST", "/api/v1/collections", h.token,
			map[string]any{"name": "Winter", "description": "Cold books"}))
	assertMatches(t, spec, "GET", "/api/v1/collections", h.get("/api/v1/collections"))
	assertMatches(t, spec, "GET", "/api/v1/collections/{id}", h.get("/api/v1/collections/1"))

	inCollection := fmt.Sprintf("/api/v1/collections/1/books/%d", lib.dune)
	assertMatches(t, spec, "PUT", "/api/v1/collections/{id}/books/{bookID}",
		h.sendJSON("PUT", inCollection, h.token, nil))
	assertMatches(t, spec, "DELETE", "/api/v1/collections/{id}/books/{bookID}",
		h.sendJSON("DELETE", inCollection, h.token, nil))
	assertMatches(t, spec, "PATCH", "/api/v1/collections/{id}",
		h.sendJSON("PATCH", "/api/v1/collections/1", h.token, map[string]any{"name": "Winter Reading"}))
	assertMatches(t, spec, "DELETE", "/api/v1/collections/{id}",
		h.sendJSON("DELETE", "/api/v1/collections/1", h.token, nil))

	const uuid = "11111111-1111-4111-8111-111111111111"
	assertMatches(t, spec, "PUT", "/api/v1/annotations/{uuid}",
		h.sendJSON("PUT", "/api/v1/annotations/"+uuid, h.token, map[string]any{
			"edition_id": lib.duneEPUB, "kind": "highlight",
			"cfi": "epubcfi(/6/14!/4/2/2/1:0)", "percent": 0.42, "quote": "A beginning",
		}))
	assertMatches(t, spec, "GET", "/api/v1/annotations",
		h.get(fmt.Sprintf("/api/v1/annotations?edition_id=%d", lib.duneEPUB)))
	assertMatches(t, spec, "GET", "/api/v1/annotations/changes",
		h.get("/api/v1/annotations/changes?since=0"))
	assertMatches(t, spec, "DELETE", "/api/v1/annotations/{uuid}",
		h.sendJSON("DELETE", "/api/v1/annotations/"+uuid, h.token, nil))

	assertMatches(t, spec, "GET", "/api/v1/groupings/uncertain", h.get("/api/v1/groupings/uncertain"))
	assertMatches(t, spec, "GET", "/api/v1/users/{id}/overrides", h.get("/api/v1/users/1/overrides"))

	override := fmt.Sprintf("/api/v1/users/1/overrides/%d", lib.dune)
	assertMatches(t, spec, "PUT", "/api/v1/users/{id}/overrides/{bookID}",
		h.sendJSON("PUT", override, h.token, map[string]any{"allowed": true}))
	assertMatches(t, spec, "DELETE", "/api/v1/users/{id}/overrides/{bookID}",
		h.sendJSON("DELETE", override, h.token, nil))

	// Both refuse before doing anything when nothing is configured, and that
	// refusal is as much a documented shape as a success.
	assertMatches(t, spec, "GET", "/api/v1/books/{id}/match",
		h.get(fmt.Sprintf("/api/v1/books/%d/match", lib.dune)))
	assertMatches(t, spec, "POST", "/api/v1/uploads",
		h.sendJSON("POST", "/api/v1/uploads", h.token, map[string]any{
			"source_id": lib.enabledSource, "filename": "Probe.epub", "format": "epub",
			"total_bytes": 1024,
			"sha256":      "0000000000000000000000000000000000000000000000000000000000000000",
		}))
	assertMatches(t, spec, "GET", "/api/v1/uploads/{id}", h.get("/api/v1/uploads/nope"))
}

// The error shapes are as much a promise as the success ones, and are what a
// client branches on.
func TestDocumentedErrorsMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	h := newHarness(t)
	h.seedLibrary()

	assertMatches(t, spec, "GET", "/api/v1/books/{id}", h.get("/api/v1/books/99999"))
	assertMatches(t, spec, "GET", "/api/v1/books", h.get("/api/v1/books?nonsense=1"))
	assertMatches(t, spec, "GET", "/api/v1/books", h.get("/api/v1/books?limit=9999"))
	assertMatches(t, spec, "GET", "/api/v1/books", h.get("/api/v1/books?sort=sideways"))
	assertMatches(t, spec, "GET", "/api/v1/facets/{name}", h.get("/api/v1/facets/invented"))
	assertMatches(t, spec, "GET", "/api/v1/series/{id}", h.get("/api/v1/series/99999"))
	assertMatches(t, spec, "GET", "/api/v1/books/{id}/cover", h.get("/api/v1/books/99999/cover"))
	assertMatches(t, spec, "GET", "/api/v1/files/{id}/content", h.get("/api/v1/files/99999/content"))
}

func TestLoginResponsesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	h := newHarness(t)

	login := func(body any) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(encoded))
		r.Header.Set("Content-Type", "application/json")
		return h.serve(r)
	}

	assertMatches(t, spec, "POST", "/api/v1/auth/login", login(map[string]any{
		"username": "emily", "password": "correct horse battery staple", "device_name": "Probe",
	}))
	assertMatches(t, spec, "POST", "/api/v1/auth/login", login(map[string]any{
		"username": "emily", "password": "wrong",
	}))
	assertMatches(t, spec, "POST", "/api/v1/auth/login", login(map[string]any{
		"username": "emily",
	}))
}

// Proving the check bites. A validator that accepts anything would make every
// assertion above vacuous.
func TestResponseValidationDetectsAWrongShape(t *testing.T) {
	spec := loadSpec(t)

	schema, ok := spec.ResponseSchema("GET", "/api/v1/series", "200")
	if !ok {
		t.Fatal("no schema for the series listing")
	}

	compiled := compile(t, schema)
	if err := compiled.Validate(map[string]any{"series": "not an array"}); err == nil {
		t.Error("a string passed where an array of series was promised")
	}
	if err := compiled.Validate(map[string]any{}); err == nil {
		t.Error("a response missing its required property was accepted")
	}
}
