package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

// tree builds a fixture library, creating parent directories as needed.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func walk(t *testing.T, root string) *WalkResult {
	t.Helper()
	res, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Warnings {
		t.Logf("warning: %v", w)
	}
	return res
}

// names reports each candidate as "format:file,file" relative to root, which
// makes the grouping decisions readable in a failure message.
func names(root string, res *WalkResult) []string {
	out := make([]string, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		line := string(c.Format) + ":"
		for i, f := range c.Files {
			rel, _ := filepath.Rel(root, f.Path)
			if i > 0 {
				line += ","
			}
			line += filepath.ToSlash(rel)
		}
		out = append(out, line)
	}
	return out
}

func assertCandidates(t *testing.T, root string, res *WalkResult, want ...string) {
	t.Helper()
	got := names(root, res)
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %v, want %d %v", len(got), got, len(want), want)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("missing candidate %q; got %v", w, got)
		}
	}
}

func TestEbooksAreOneEditionPerFile(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Tolkien/The Hobbit.pdf",
		"Tolkien/notes.txt",
		"Tolkien/cover.jpg",
	)
	assertCandidates(t, root, walk(t, root),
		"epub:Tolkien/The Hobbit.epub",
		"pdf:Tolkien/The Hobbit.pdf",
	)
}

func TestM4BsNeverGroup(t *testing.T) {
	// A flat folder of self-contained audiobooks is a common layout, so each
	// file has to stay its own edition.
	root := tree(t,
		"Audiobooks/Dune.m4b",
		"Audiobooks/Neuromancer.m4b",
		"Audiobooks/Snow Crash.m4a",
	)
	assertCandidates(t, root, walk(t, root),
		"m4b:Audiobooks/Dune.m4b",
		"m4b:Audiobooks/Neuromancer.m4b",
		"m4b:Audiobooks/Snow Crash.m4a",
	)
}

func TestNumberedMP3FolderIsOneOrderedEdition(t *testing.T) {
	root := tree(t,
		"Dune/01 - Opening.mp3",
		"Dune/02 - Arrakis.mp3",
		"Dune/10 - Finale.mp3",
		"Dune/09 - Duel.mp3",
	)
	res := walk(t, root)
	assertCandidates(t, root, res,
		"mp3:Dune/01 - Opening.mp3,Dune/02 - Arrakis.mp3,Dune/09 - Duel.mp3,Dune/10 - Finale.mp3")

	if got := res.Candidates[0].Format; got != model.FormatMP3 {
		t.Errorf("format %q, want mp3", got)
	}
	if got := res.Candidates[0].Format.Kind(); got != model.KindAudiobook {
		t.Errorf("kind %q, want audiobook", got)
	}
}

func TestPrefixedMP3FolderIsOneEdition(t *testing.T) {
	root := tree(t,
		"Neuromancer/Neuromancer Chapter 1.mp3",
		"Neuromancer/Neuromancer Chapter 2.mp3",
		"Neuromancer/Neuromancer Chapter 11.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Neuromancer/Neuromancer Chapter 1.mp3,"+
			"Neuromancer/Neuromancer Chapter 2.mp3,"+
			"Neuromancer/Neuromancer Chapter 11.mp3")
}

func TestUnrelatedMP3sInOneFolderStaySeparate(t *testing.T) {
	// The hazard case: a drop folder of single-file audiobooks must not merge
	// into one twelve-hour edition.
	root := tree(t,
		"Shelf/Dune.mp3",
		"Shelf/Neuromancer.mp3",
		"Shelf/Snow Crash.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Shelf/Dune.mp3",
		"mp3:Shelf/Neuromancer.mp3",
		"mp3:Shelf/Snow Crash.mp3",
	)
}

func TestSharedArticleIsNotASharedPrefix(t *testing.T) {
	root := tree(t,
		"Shelf/The Hobbit.mp3",
		"Shelf/The Fellowship.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Shelf/The Hobbit.mp3",
		"mp3:Shelf/The Fellowship.mp3",
	)
}

func TestAStrayFileDoesNotShatterANumberedFolder(t *testing.T) {
	root := tree(t,
		"Dune/01 - One.mp3",
		"Dune/02 - Two.mp3",
		"Dune/03 - Three.mp3",
		"Dune/Bonus Interview.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Dune/01 - One.mp3,Dune/02 - Two.mp3,Dune/03 - Three.mp3,Dune/Bonus Interview.mp3")
}

func TestNumericTitlesAreNotTrackNumbers(t *testing.T) {
	root := tree(t,
		"Shelf/1984.mp3",
		"Shelf/2001.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Shelf/1984.mp3",
		"mp3:Shelf/2001.mp3",
	)
}

func TestLoneMP3IsItsOwnEdition(t *testing.T) {
	root := tree(t, "Dune/Dune.mp3")
	assertCandidates(t, root, walk(t, root), "mp3:Dune/Dune.mp3")
}

func TestDiscFoldersMergeIntoOneEdition(t *testing.T) {
	root := tree(t,
		"Dune/Disc 1/01.mp3",
		"Dune/Disc 1/02.mp3",
		"Dune/Disc 2/01.mp3",
		"Dune/CD10/01.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Dune/CD10/01.mp3,Dune/Disc 1/01.mp3,Dune/Disc 1/02.mp3,Dune/Disc 2/01.mp3")
}

func TestPerBookSubfoldersDoNotMerge(t *testing.T) {
	// The same shape as a multi-disc rip, but the names are titles, so each
	// folder has to stay its own edition.
	root := tree(t,
		"Herbert/Dune/01.mp3",
		"Herbert/Dune/02.mp3",
		"Herbert/Dune Messiah/01.mp3",
		"Herbert/Dune Messiah/02.mp3",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Herbert/Dune/01.mp3,Herbert/Dune/02.mp3",
		"mp3:Herbert/Dune Messiah/01.mp3,Herbert/Dune Messiah/02.mp3",
	)
}

func TestDiscFoldersHoldingEbooksDoNotMerge(t *testing.T) {
	root := tree(t,
		"Dune/Disc 1/01.mp3",
		"Dune/Disc 2/bonus.epub",
	)
	assertCandidates(t, root, walk(t, root),
		"mp3:Dune/Disc 1/01.mp3",
		"epub:Dune/Disc 2/bonus.epub",
	)
}

func TestHiddenAndJunkEntriesAreIgnored(t *testing.T) {
	root := tree(t,
		"Dune/Dune.epub",
		"Dune/.hidden.epub",
		"Dune/.DS_Store",
		"@eaDir/thumb.epub",
		"__MACOSX/Dune.epub",
	)
	assertCandidates(t, root, walk(t, root), "epub:Dune/Dune.epub")
}

func TestWalkFollowsSymlinksWithoutLooping(t *testing.T) {
	root := tree(t, "Real/Dune.epub")

	if err := os.Symlink(filepath.Join(root, "Real"), filepath.Join(root, "Link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A link back to the root would spin forever without the visited set.
	if err := os.Symlink(root, filepath.Join(root, "Real", "Loop")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res := walk(t, root)
	if len(res.Candidates) != 1 {
		t.Errorf("got %d candidates %v, want the one real file", len(res.Candidates), names(root, res))
	}
}

func TestWalkRejectsAMissingRoot(t *testing.T) {
	if _, err := Walk(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("walking a missing root succeeded")
	}
}

func TestNaturalLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"02.mp3", "10.mp3", true},
		{"10.mp3", "02.mp3", false},
		{"Chapter 2", "Chapter 10", true},
		{"track1", "track1a", true},
		{"a", "B", true},
		{"01", "1", false},
	}
	for _, c := range cases {
		if got := naturalLess(c.a, c.b); got != c.want {
			t.Errorf("naturalLess(%q, %q) = %t, want %t", c.a, c.b, got, c.want)
		}
	}
}
