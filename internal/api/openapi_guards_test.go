package api

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/policy"
)

// A reason code is only worth branching on if it is written down, so adding
// one without documenting it should fail here rather than reach a client as a
// string nobody can look up.
func TestEveryPolicyReasonIsDocumented(t *testing.T) {
	denied, ok := loadSpec(t).Schemas()["Denied"].(map[string]any)
	if !ok {
		t.Fatal("the spec has no Denied schema")
	}
	properties, _ := denied["properties"].(map[string]any)
	reason, _ := properties["reason"].(map[string]any)
	listed, _ := reason["enum"].([]any)

	documented := make([]string, 0, len(listed))
	for _, entry := range listed {
		documented = append(documented, fmt.Sprint(entry))
	}

	for _, r := range policy.AllReasons() {
		if !slices.Contains(documented, string(r)) {
			t.Errorf("reason %q is not in the Denied enum in openapi.yaml", r)
		}
	}

	for _, name := range documented {
		if !slices.ContainsFunc(policy.AllReasons(), func(r policy.Reason) bool {
			return string(r) == name
		}) {
			t.Errorf("the spec documents reason %q, which no policy produces", name)
		}
	}
}

// A path in a response leaks the server's layout, which is why responses are
// explicit DTOs. An example is published just as widely, so the same rule
// applies to it.
func TestNoExampleLeaksAFilesystemPath(t *testing.T) {
	// URL paths are fine and common; these are the roots a real library sits
	// under, which is what must never appear.
	leaks := []string{"/home/", "/srv/", "/var/", "/mnt/", "/media/", "/Users/", `C:\`}

	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				walk(path+"/"+key, child)
			}
		case []any:
			for i, child := range v {
				walk(fmt.Sprintf("%s/%d", path, i), child)
			}
		case string:
			if !strings.Contains(path, "example") {
				return
			}
			for _, leak := range leaks {
				if strings.Contains(v, leak) {
					t.Errorf("example at %s contains a filesystem path: %q", path, v)
				}
			}
		}
	}
	walk("", loadSpec(t).Document)
}

// Proving the walker reaches examples at all, since one that matched nothing
// would pass silently however bad the spec got.
func TestExampleWalkerReachesExamples(t *testing.T) {
	found := 0

	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				walk(path+"/"+key, child)
			}
		case []any:
			for _, child := range v {
				walk(path, child)
			}
		case string:
			if strings.Contains(path, "example") {
				found++
			}
		}
	}
	walk("", loadSpec(t).Document)

	if found == 0 {
		t.Fatal("no example strings found, so the leak check examines nothing")
	}
	t.Logf("%d example strings checked", found)
}
