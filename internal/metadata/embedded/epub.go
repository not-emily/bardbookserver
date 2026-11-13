package embedded

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

// maxCoverBytes keeps a mislabelled interior illustration from being loaded
// whole; real cover art sits far below it.
const maxCoverBytes = 16 << 20

// marcRoles maps the relator codes both EPUB generations use. Anything else is
// dropped: an unroled dc:contributor is as often "Project Gutenberg" as a person.
var marcRoles = map[string]string{
	"aut": "author",
	"nrt": "narrator",
	"trl": "translator",
	"ill": "illustrator",
	"edt": "editor",
}

type opfPackage struct {
	Version  string      `xml:"version,attr"`
	Metadata opfMetadata `xml:"metadata"`
	Manifest struct {
		Items []opfItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		TOC string `xml:"toc,attr"`
	} `xml:"spine"`
}

type opfMetadata struct {
	Titles       []string        `xml:"title"`
	Languages    []string        `xml:"language"`
	Publishers   []string        `xml:"publisher"`
	Dates        []string        `xml:"date"`
	Descriptions []string        `xml:"description"`
	Subjects     []string        `xml:"subject"`
	Creators     []opfCreator    `xml:"creator"`
	Contributors []opfCreator    `xml:"contributor"`
	Identifiers  []opfIdentifier `xml:"identifier"`
	Metas        []opfMeta       `xml:"meta"`
}

type opfCreator struct {
	ID   string `xml:"id,attr"`
	Role string `xml:"role,attr"`
	Name string `xml:",chardata"`
}

type opfIdentifier struct {
	Scheme string `xml:"scheme,attr"`
	Value  string `xml:",chardata"`
}

// opfMeta covers both generations: EPUB 2 writes name/content pairs, EPUB 3
// writes a property with chardata and refines pointing at what it describes.
type opfMeta struct {
	ID       string `xml:"id,attr"`
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Refines  string `xml:"refines,attr"`
	Value    string `xml:",chardata"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

func ParseEPUB(path string) (metadata.Metadata, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return metadata.Metadata{}, fmt.Errorf("open epub %s: %w", path, err)
	}
	defer zr.Close()

	entries := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		entries[f.Name] = f
	}

	opfPath, err := rootfilePath(entries)
	if err != nil {
		return metadata.Metadata{}, fmt.Errorf("epub %s: %w", path, err)
	}

	raw, err := readEntry(entries, opfPath)
	if err != nil {
		return metadata.Metadata{}, fmt.Errorf("epub %s: %w", path, err)
	}
	var pkg opfPackage
	if err := xml.Unmarshal(raw, &pkg); err != nil {
		return metadata.Metadata{}, fmt.Errorf("epub %s: parse opf: %w", path, err)
	}

	return buildEPUB(&pkg, entries, opfPath), nil
}

func buildEPUB(pkg *opfPackage, entries map[string]*zip.File, opfPath string) metadata.Metadata {
	md := metadata.Metadata{
		Subjects: pkg.Metadata.Subjects,
	}
	if len(pkg.Metadata.Titles) > 0 {
		md.Title = pkg.Metadata.Titles[0]
	}
	// A second dc:title is conventionally the subtitle; EPUB 3 can also say so
	// outright with title-type.
	if len(pkg.Metadata.Titles) > 1 {
		md.Subtitle = pkg.Metadata.Titles[1]
	}
	if len(pkg.Metadata.Languages) > 0 {
		md.Language = normalizeLanguage(pkg.Metadata.Languages[0])
	}
	if len(pkg.Metadata.Publishers) > 0 {
		md.Publisher = pkg.Metadata.Publishers[0]
	}
	if len(pkg.Metadata.Dates) > 0 {
		md.PublishedOn = normalizeDate(pkg.Metadata.Dates[0])
	}
	if len(pkg.Metadata.Descriptions) > 0 {
		md.Description = pkg.Metadata.Descriptions[0]
	}

	md.Authors, md.Narrators = epubContributors(pkg)
	md.ISBN, md.ASIN = epubIdentifiers(pkg)
	md.Series, md.SeriesPosition = epubSeries(pkg)
	md.Cover = epubCover(pkg, entries, opfPath)
	md.Chapters = epubChapters(pkg, entries, opfPath)
	return md
}

// epubContributors reads roles from either generation: an opf:role attribute,
// or a refining meta keyed on the creator's id.
func epubContributors(pkg *opfPackage) (authors, narrators []string) {
	refined := map[string]string{}
	for _, m := range pkg.Metadata.Metas {
		if localProperty(m.Property) == "role" && m.Refines != "" {
			refined[strings.TrimPrefix(m.Refines, "#")] = strings.TrimSpace(m.Value)
		}
	}

	collect := func(people []opfCreator, defaultRole string) {
		for _, p := range people {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				continue
			}
			code := p.Role
			if code == "" {
				code = refined[p.ID]
			}
			role, known := marcRoles[strings.ToLower(strings.TrimSpace(code))]
			if !known {
				if code != "" {
					continue
				}
				role = defaultRole
			}
			switch role {
			case "author":
				authors = append(authors, name)
			case "narrator":
				narrators = append(narrators, name)
			}
		}
	}

	// A dc:creator with no role is the author; a dc:contributor with no role
	// could be anyone, so it needs one to count.
	collect(pkg.Metadata.Creators, "author")
	collect(pkg.Metadata.Contributors, "")
	return authors, narrators
}

func epubIdentifiers(pkg *opfPackage) (isbn, asin string) {
	for _, id := range pkg.Metadata.Identifiers {
		scheme := strings.ToUpper(strings.TrimSpace(id.Scheme))
		value := strings.TrimSpace(id.Value)
		lower := strings.ToLower(value)

		switch {
		case scheme == "ISBN" || strings.HasPrefix(lower, "urn:isbn:"):
			if v := normalizeISBN(strings.TrimPrefix(lower, "urn:isbn:")); v != "" && isbn == "" {
				isbn = v
			}
		case strings.Contains(scheme, "ASIN") || scheme == "AMAZON":
			if asin == "" {
				asin = value
			}
		}
	}

	for _, m := range pkg.Metadata.Metas {
		switch strings.ToLower(m.Name) {
		case "calibre:isbn":
			if v := normalizeISBN(m.Content); v != "" && isbn == "" {
				isbn = v
			}
		case "calibre:asin", "calibre:amazon":
			if asin == "" {
				asin = strings.TrimSpace(m.Content)
			}
		}
	}
	return isbn, asin
}

func epubSeries(pkg *opfPackage) (string, *float64) {
	var (
		name         string
		collectionID string
		position     *float64
	)

	// EPUB 3: a collection element, with its place in the series carried by a
	// separate meta refining it.
	for _, m := range pkg.Metadata.Metas {
		if localProperty(m.Property) == "belongs-to-collection" {
			name, collectionID = strings.TrimSpace(m.Value), m.ID
			break
		}
	}
	if name != "" {
		for _, m := range pkg.Metadata.Metas {
			if localProperty(m.Property) != "group-position" {
				continue
			}
			if refines := strings.TrimPrefix(m.Refines, "#"); refines == collectionID || refines == "" {
				if v, err := strconv.ParseFloat(strings.TrimSpace(m.Value), 64); err == nil {
					position = &v
					break
				}
			}
		}
	}

	// EPUB 2, and what Calibre writes whatever the version.
	for _, m := range pkg.Metadata.Metas {
		switch strings.ToLower(m.Name) {
		case "calibre:series":
			if name == "" {
				name = strings.TrimSpace(m.Content)
			}
		case "calibre:series_index":
			if v, err := strconv.ParseFloat(strings.TrimSpace(m.Content), 64); err == nil && position == nil {
				position = &v
			}
		}
	}
	return name, position
}

func epubCover(pkg *opfPackage, entries map[string]*zip.File, opfPath string) *metadata.Cover {
	var href, mime string

	for _, item := range pkg.Manifest.Items {
		if strings.Contains(item.Properties, "cover-image") {
			href, mime = item.Href, item.MediaType
			break
		}
	}
	if href == "" {
		// EPUB 2 points at a manifest id from a metadata meta instead.
		var coverID string
		for _, m := range pkg.Metadata.Metas {
			if strings.EqualFold(m.Name, "cover") {
				coverID = strings.TrimSpace(m.Content)
				break
			}
		}
		for _, item := range pkg.Manifest.Items {
			if item.ID == coverID && coverID != "" {
				href, mime = item.Href, item.MediaType
				break
			}
		}
	}
	if href == "" {
		return nil
	}

	data, err := readEntry(entries, resolveHref(opfPath, href))
	if err != nil || len(data) == 0 {
		return nil
	}
	return &metadata.Cover{Data: data, MIME: mime}
}

// epubChapters prefers the EPUB 3 nav document and falls back to the EPUB 2
// NCX, since Project Gutenberg and other old files ship only the latter.
func epubChapters(pkg *opfPackage, entries map[string]*zip.File, opfPath string) []metadata.Chapter {
	for _, item := range pkg.Manifest.Items {
		if !strings.Contains(item.Properties, "nav") {
			continue
		}
		if data, err := readEntry(entries, resolveHref(opfPath, item.Href)); err == nil {
			if chapters := parseNav(data); len(chapters) > 0 {
				return chapters
			}
		}
	}

	for _, item := range pkg.Manifest.Items {
		isNCX := item.ID == pkg.Spine.TOC ||
			item.MediaType == "application/x-dtbncx+xml"
		if !isNCX {
			continue
		}
		if data, err := readEntry(entries, resolveHref(opfPath, item.Href)); err == nil {
			if chapters := parseNCX(data); len(chapters) > 0 {
				return chapters
			}
		}
	}
	return nil
}

type ncxDoc struct {
	NavPoints []ncxNavPoint `xml:"navMap>navPoint"`
}

type ncxNavPoint struct {
	NavLabel struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxNavPoint `xml:"navPoint"`
}

func parseNCX(data []byte) []metadata.Chapter {
	var doc ncxDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}

	var out []metadata.Chapter
	var walk func([]ncxNavPoint)
	walk = func(points []ncxNavPoint) {
		for _, p := range points {
			title := strings.Join(strings.Fields(p.NavLabel.Text), " ")
			if title != "" || p.Content.Src != "" {
				out = append(out, metadata.Chapter{Title: title, Href: p.Content.Src})
			}
			walk(p.Children)
		}
	}
	walk(doc.NavPoints)
	return out
}

// parseNav walks tokens rather than modelling the document, because a nav list
// nests to arbitrary depth and the chapters table is flat.
func parseNav(data []byte) []metadata.Chapter {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var (
		out       []metadata.Chapter
		inTOC     bool
		navDepth  int
		anchor    *metadata.Chapter
		anchorTxt strings.Builder
	)

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "nav":
				if inTOC {
					navDepth++
					continue
				}
				if navType(t) {
					inTOC, navDepth = true, 1
				}
			case "a":
				if inTOC && anchor == nil {
					anchor = &metadata.Chapter{Href: attr(t, "href")}
					anchorTxt.Reset()
				}
			}
		case xml.CharData:
			if anchor != nil {
				anchorTxt.Write(t)
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "a":
				if anchor != nil {
					anchor.Title = strings.Join(strings.Fields(anchorTxt.String()), " ")
					if anchor.Title != "" || anchor.Href != "" {
						out = append(out, *anchor)
					}
					anchor = nil
				}
			case "nav":
				if inTOC {
					if navDepth--; navDepth == 0 {
						return out
					}
				}
			}
		}
	}
	return out
}

// navType reports whether this nav element is the table of contents. A
// document with exactly one nav and no epub:type still means its only list.
func navType(e xml.StartElement) bool {
	for _, a := range e.Attr {
		if a.Name.Local == "type" && strings.Contains(strings.ToLower(a.Value), "toc") {
			return true
		}
	}
	for _, a := range e.Attr {
		if a.Name.Local == "type" || a.Name.Local == "role" {
			return strings.Contains(strings.ToLower(a.Value), "toc")
		}
	}
	return true
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

type container struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

func rootfilePath(entries map[string]*zip.File) (string, error) {
	raw, err := readEntry(entries, "META-INF/container.xml")
	if err != nil {
		return "", err
	}
	var c container
	if err := xml.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	for _, rf := range c.Rootfiles {
		if rf.FullPath != "" {
			return rf.FullPath, nil
		}
	}
	return "", fmt.Errorf("container.xml names no rootfile")
}

// resolveHref makes a manifest href absolute within the zip: hrefs are
// relative to the OPF, which usually is not at the archive root.
func resolveHref(opfPath, href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if decoded, err := url.PathUnescape(href); err == nil {
		href = decoded
	}
	return path.Join(path.Dir(opfPath), href)
}

func readEntry(entries map[string]*zip.File, name string) ([]byte, error) {
	f, ok := entries[name]
	if !ok {
		return nil, fmt.Errorf("missing zip entry %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxCoverBytes))
	if err != nil {
		return nil, fmt.Errorf("read zip entry %s: %w", name, err)
	}
	return data, nil
}

func localProperty(property string) string {
	if i := strings.IndexByte(property, ':'); i >= 0 {
		return strings.ToLower(property[i+1:])
	}
	return strings.ToLower(property)
}
