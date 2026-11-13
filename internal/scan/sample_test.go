package scan

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// sampleDir holds real books, which never enter the repo. Tests that want one
// skip when it is absent, so CI stays green without them.
const sampleDir = "../../../sample-media"

func sampleSet(t *testing.T, dir string) []string {
	t.Helper()
	d := filepath.Join(sampleDir, dir)
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Skipf("sample set %s not present", dir)
	}

	var paths []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".mp3" {
			abs, err := filepath.Abs(filepath.Join(d, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, abs)
		}
	}
	if len(paths) == 0 {
		t.Skipf("sample set %s holds no mp3s", dir)
	}
	sort.Strings(paths)
	return paths
}
