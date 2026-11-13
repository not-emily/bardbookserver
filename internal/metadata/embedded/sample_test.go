package embedded

import (
	"os"
	"path/filepath"
	"testing"
)

// sampleDir holds real books, which never enter the repo. Tests that want one
// skip when it is absent, so CI stays green without them.
const sampleDir = "../../../../sample-media"

func sample(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(sampleDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("sample media %s not present", name)
	}
	return p
}
