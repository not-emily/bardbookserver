package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Six characters groups "The Hobbit " while leaving two unrelated titles that
// merely share "The " apart.
const minSharedPrefix = 6

// maxDepth guards a pathological tree, and any symlink loop the visited set misses.
const maxDepth = 24

// Candidate is one prospective edition: a multi-part audiobook is one
// Candidate with many files.
type Candidate struct {
	Format model.Format
	Dir    string
	Files  []CandidateFile
}

type CandidateFile struct {
	Path    string
	Size    int64
	ModTime time.Time
}

type WalkResult struct {
	Candidates []Candidate

	// An unreadable directory or file degrades the scan rather than failing
	// it: one bad permission must not cost the whole library.
	Warnings []error
}

// Names must look like disc markers before their contents merge: the rival
// reading, a folder of per-book subdirectories, is commoner and merging those
// is worse than splitting a rip.
var discDir = regexp.MustCompile(`(?i)^(disc|disk|cd|part|pt|vol|volume)[\s._-]*0*\d+$`)

// trackPrefix matches a part number: "01", "1. Title", "003_Title", "01b - Title".
// A bare number stops at three digits so a numerically titled book ("1984.mp3")
// is not read as track 1984.
var trackPrefix = regexp.MustCompile(`(?i)^\d{1,3}[a-z]?([\s._-]+\S|$)`)

// Walk descends the tree itself rather than using filepath.WalkDir because
// grouping is decided per directory, which a file-at-a-time callback cannot do.
func Walk(root string) (*WalkResult, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat source %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source %s is not a directory", root)
	}

	w := &walker{res: &WalkResult{}, visited: map[string]bool{}}
	w.dir(root, 0)
	return w.res, nil
}

type walker struct {
	res *WalkResult

	// Resolved directories already walked. Self-hosters symlink drives into a
	// library tree, so links are followed, which means cycles are possible.
	visited map[string]bool
}

func (w *walker) warn(err error) { w.res.Warnings = append(w.res.Warnings, err) }

func (w *walker) dir(dir string, depth int) {
	if depth > maxDepth {
		w.warn(fmt.Errorf("skipped %s: nested deeper than %d directories", dir, maxDepth))
		return
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		w.warn(fmt.Errorf("resolve %s: %w", dir, err))
		return
	}
	if w.visited[resolved] {
		return
	}
	w.visited[resolved] = true

	entries, err := os.ReadDir(dir)
	if err != nil {
		w.warn(fmt.Errorf("read dir %s: %w", dir, err))
		return
	}

	var subdirs []string
	byFormat := map[model.Format][]CandidateFile{}

	for _, e := range entries {
		name := e.Name()
		if skipName(name) {
			continue
		}
		path := filepath.Join(dir, name)

		info, err := os.Stat(path)
		if err != nil {
			w.warn(fmt.Errorf("stat %s: %w", path, err))
			continue
		}
		if info.IsDir() {
			subdirs = append(subdirs, path)
			continue
		}
		format, ok := formatOf(name)
		if !ok || !info.Mode().IsRegular() {
			continue
		}
		byFormat[format] = append(byFormat[format],
			CandidateFile{Path: path, Size: info.Size(), ModTime: info.ModTime()})
	}

	sort.Slice(subdirs, func(i, j int) bool { return naturalLess(subdirs[i], subdirs[j]) })

	// Each ebook file is its own edition: two EPUBs side by side are two
	// editions, which Phase 5 may group into one book.
	for _, format := range []model.Format{model.FormatEPUB, model.FormatPDF} {
		for _, f := range byFormat[format] {
			w.emit(Candidate{Format: format, Dir: dir, Files: []CandidateFile{f}})
		}
	}

	// An M4B is a whole audiobook in one file, and a flat folder of them is a
	// common layout, so unlike MP3s they never group.
	m4bs := byFormat[model.FormatM4B]
	sort.Slice(m4bs, func(i, j int) bool { return naturalLess(m4bs[i].Path, m4bs[j].Path) })
	for _, f := range m4bs {
		w.emit(Candidate{Format: model.FormatM4B, Dir: dir, Files: []CandidateFile{f}})
	}

	for _, group := range groupMP3s(byFormat[model.FormatMP3]) {
		w.emit(Candidate{Format: model.FormatMP3, Dir: dir, Files: group})
	}

	if len(byFormat[model.FormatMP3]) == 0 && len(m4bs) == 0 {
		if files, ok := w.discSet(subdirs); ok {
			w.emit(Candidate{Format: model.FormatMP3, Dir: dir, Files: files})
			return
		}
	}

	for _, sub := range subdirs {
		w.dir(sub, depth+1)
	}
}

func (w *walker) emit(c Candidate) {
	for i := range c.Files {
		c.Files[i].Path = filepath.Clean(c.Files[i].Path)
	}
	w.res.Candidates = append(w.res.Candidates, c)
}

// discSet recognises a multi-disc rip: every subdirectory named like a disc and
// holding nothing but MP3s. Anything less uniform descends normally.
func (w *walker) discSet(subdirs []string) ([]CandidateFile, bool) {
	if len(subdirs) < 2 {
		return nil, false
	}

	var files []CandidateFile
	for _, sub := range subdirs {
		if !discDir.MatchString(filepath.Base(sub)) {
			return nil, false
		}
		entries, err := os.ReadDir(sub)
		if err != nil {
			return nil, false
		}

		var discFiles []CandidateFile
		for _, e := range entries {
			name := e.Name()
			if skipName(name) {
				continue
			}
			info, err := os.Stat(filepath.Join(sub, name))
			if err != nil || info.IsDir() {
				return nil, false
			}
			format, ok := formatOf(name)
			if !ok {
				continue
			}
			if format != model.FormatMP3 {
				return nil, false
			}
			discFiles = append(discFiles, CandidateFile{
				Path: filepath.Join(sub, name), Size: info.Size(), ModTime: info.ModTime(),
			})
		}
		if len(discFiles) == 0 {
			return nil, false
		}
		sort.Slice(discFiles, func(i, j int) bool { return naturalLess(discFiles[i].Path, discFiles[j].Path) })
		files = append(files, discFiles...)
	}

	for _, sub := range subdirs {
		if resolved, err := filepath.EvalSymlinks(sub); err == nil {
			w.visited[resolved] = true
		}
	}
	return files, true
}

// groupMP3s decides whether a directory's MP3s are the parts of one audiobook
// or separate books, which only the set can answer: a chapter-per-file
// audiobook and a drop folder look identical file by file.
func groupMP3s(files []CandidateFile) [][]CandidateFile {
	if len(files) == 0 {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return naturalLess(files[i].Path, files[j].Path) })
	if len(files) == 1 || oneAudiobook(files) {
		return [][]CandidateFile{files}
	}

	groups := make([][]CandidateFile, 0, len(files))
	for _, f := range files {
		groups = append(groups, []CandidateFile{f})
	}
	return groups
}

// oneAudiobook takes a majority of track prefixes rather than demanding every
// name carry one, so a stray "Bonus.mp3" does not shatter a numbered folder
// into one edition per part.
func oneAudiobook(files []CandidateFile) bool {
	names := make([]string, len(files))
	numbered := 0
	for i, f := range files {
		names[i] = stem(f.Path)
		if trackPrefix.MatchString(names[i]) {
			numbered++
		}
	}
	return numbered*2 > len(files) || len(commonPrefix(names)) >= minSharedPrefix
}

func commonPrefix(names []string) string {
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(strings.ToLower(n), strings.ToLower(prefix)) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

func formatOf(name string) (model.Format, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".epub":
		return model.FormatEPUB, true
	case ".pdf":
		return model.FormatPDF, true
	case ".m4b", ".m4a":
		// The same AAC-in-MP4 container; Apple-sourced audiobooks use .m4a.
		return model.FormatM4B, true
	case ".mp3":
		return model.FormatMP3, true
	}
	return "", false
}

func skipName(name string) bool {
	return strings.HasPrefix(name, ".") || name == "@eaDir" || name == "__MACOSX"
}

func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// naturalLess orders names so part 2 precedes part 10.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		if isDigit(ca) && isDigit(cb) {
			na, ni := readNumber(a, i)
			nb, nj := readNumber(b, j)
			if na != nb {
				return na < nb
			}
			i, j = ni, nj
			continue
		}
		la, lb := lower(ca), lower(cb)
		if la != lb {
			return la < lb
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}

func readNumber(s string, i int) (int64, int) {
	var n int64
	digits := 0
	for i < len(s) && isDigit(s[i]) {
		if digits < 18 {
			n = n*10 + int64(s[i]-'0')
			digits++
		}
		i++
	}
	return n, i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// paths lists a candidate's files in order, which is the shape the parsers want.
func (c Candidate) paths() []string {
	out := make([]string, len(c.Files))
	for i, f := range c.Files {
		out[i] = f.Path
	}
	return out
}
