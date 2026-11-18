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
