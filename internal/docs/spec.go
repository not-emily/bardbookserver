// Package docs turns docs/api/openapi.yaml into the reference a client author
// reads. The YAML is the source of truth; everything else is generated from it
// or checked against it.
package docs

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecPath is the source of truth, resolved from this file so a test can find
// it whatever directory it runs in.
func SpecPath() string {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("docs", "api", "openapi.yaml")
	}
	return filepath.Join(filepath.Dir(self), "..", "..", "docs", "api", "openapi.yaml")
}

// MarkdownPath is the generated reference.
func MarkdownPath() string {
	return filepath.Join(filepath.Dir(SpecPath()), "..", "api.md")
}

// Spec is the parsed document. It stays a map rather than a typed struct
// because the whole point is to publish what the YAML says, not a Go view of
// a subset of it.
type Spec struct {
	Document map[string]any
}

func Load() (*Spec, error) { return LoadFrom(SpecPath()) }

func LoadFrom(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("parse spec: empty document")
	}
	return &Spec{Document: doc}, nil
}

// Operation is a documented method and path.
type Operation struct {
	Method string
	Path   string
	Detail map[string]any
}

func (o Operation) String() string { return o.Method + " " + o.Path }

// httpMethods are the keys under a path item that are operations; the rest,
// such as `parameters` or `summary`, are not.
var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// Operations lists what the spec documents, sorted so output is stable.
func (s *Spec) Operations() []Operation {
	paths, _ := s.Document["paths"].(map[string]any)

	var out []Operation
	for path, item := range paths {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range httpMethods {
			detail, ok := fields[method].(map[string]any)
			if !ok {
				continue
			}
			out = append(out, Operation{
				Method: strings.ToUpper(method),
				Path:   path,
				Detail: detail,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Operation finds one, or false. Clients of this are tests asking "what does
// the spec promise for this response".
func (s *Spec) Operation(method, path string) (Operation, bool) {
	for _, op := range s.Operations() {
		if op.Method == strings.ToUpper(method) && op.Path == path {
			return op, true
		}
	}
	return Operation{}, false
}

// ResponseSchema is what a response is promised to match. The shared
// components ride along, because a subschema lifted out of the document still
// contains `$ref`s that resolve against its root.
//
// Reporting false means the spec promises no JSON body, which for a 204 is
// the right answer and for anything else is itself worth failing over.
func (s *Spec) ResponseSchema(method, path, status string) (map[string]any, bool) {
	op, ok := s.Operation(method, path)
	if !ok {
		return nil, false
	}

	responses, _ := op.Detail["responses"].(map[string]any)
	response, _ := responses[status].(map[string]any)
	content, _ := response["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	if schema == nil {
		return nil, false
	}

	rooted := make(map[string]any, len(schema)+1)
	maps.Copy(rooted, schema)
	rooted["components"] = s.Components()
	return rooted, true
}

// DocumentsStatus reports whether the operation mentions this status at all,
// so a response nobody wrote down is distinguishable from one with no body.
func (s *Spec) DocumentsStatus(method, path, status string) bool {
	op, ok := s.Operation(method, path)
	if !ok {
		return false
	}
	responses, _ := op.Detail["responses"].(map[string]any)
	_, found := responses[status]
	return found
}

// Components is the shared schema pool, carried alongside any subschema so a
// `$ref` resolves against the same root.
func (s *Spec) Components() map[string]any {
	components, _ := s.Document["components"].(map[string]any)
	return components
}

func (s *Spec) Schemas() map[string]any {
	schemas, _ := s.Components()["schemas"].(map[string]any)
	return schemas
}
