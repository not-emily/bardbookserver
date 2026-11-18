package api

import (
	"sort"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/docs"
)

func loadSpec(t *testing.T) *docs.Spec {
	t.Helper()
	spec, err := docs.Load()
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	return spec
}

func documented(t *testing.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	for _, op := range loadSpec(t).Operations() {
		out[op.String()] = true
	}
	return out
}

func implemented() map[string]bool {
	out := map[string]bool{}
	for _, op := range APIOperations() {
		out[op.String()] = true
	}
	return out
}

func listed(names map[string]bool) string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, "  "+name)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func missingFrom(have, want map[string]bool) map[string]bool {
	out := map[string]bool{}
	for name := range want {
		if !have[name] {
			out[name] = true
		}
	}
	return out
}

// The spec is hand-written, so nothing but this makes it match the server.
// It is deliberately loud while the reference is incomplete: the count is the
// burn-down list for documenting the API, and a quiet test is how that gets
// abandoned half-finished.
func TestEveryRouteIsDocumented(t *testing.T) {
	missing := missingFrom(documented(t), implemented())

	if len(missing) > 0 {
		t.Errorf("%d route(s) with no operation in docs/api/openapi.yaml:\n%s",
			len(missing), listed(missing))
	}
}

// Worse than an undocumented route: a promise the server does not keep, which
// is how a reference rots after an endpoint is renamed.
func TestEveryDocumentedOperationExists(t *testing.T) {
	orphaned := missingFrom(implemented(), documented(t))

	if len(orphaned) > 0 {
		t.Errorf("%d operation(s) in docs/api/openapi.yaml with no route:\n%s",
			len(orphaned), listed(orphaned))
	}
}

// Both tests above compare two sets, and a comparison that is silently empty
// on either side passes while checking nothing. These prove it bites.

func TestCoverageDetectsAnUndocumentedRoute(t *testing.T) {
	missing := missingFrom(map[string]bool{}, implemented())

	if len(missing) != len(APIOperations()) {
		t.Errorf("an empty spec left %d of %d routes undetected",
			len(APIOperations())-len(missing), len(APIOperations()))
	}
}

func TestCoverageDetectsAnOrphanedOperation(t *testing.T) {
	fabricated := map[string]bool{"GET /api/v1/invented": true}

	orphaned := missingFrom(implemented(), fabricated)
	if len(orphaned) != 1 || !orphaned["GET /api/v1/invented"] {
		t.Errorf("an operation with no route was not detected: %v", orphaned)
	}
}

// The route table is what both tests read, so an empty one would make them
// agree about nothing.
func TestRouteTableIsNotEmpty(t *testing.T) {
	if len(APIOperations()) == 0 {
		t.Fatal("apiRoutes() is empty")
	}

	for _, op := range APIOperations() {
		if !strings.HasPrefix(op.Path, "/api/v1/") {
			t.Errorf("%s is not under /api/v1/", op)
		}
		if op.Method != strings.ToUpper(op.Method) {
			t.Errorf("%s has a lowercase method", op)
		}
	}
}
