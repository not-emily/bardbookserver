package docs

import (
	"os"
	"strings"
	"testing"
)

// The YAML is the source of truth only if the generated file cannot drift from
// it unnoticed.
func TestMarkdownIsNotStale(t *testing.T) {
	published, err := os.ReadFile(MarkdownPath())
	if err != nil {
		t.Fatalf("read the generated reference: %v (run scripts/docs)", err)
	}

	if got := load(t).Markdown(); got != string(published) {
		t.Error("docs/api.md does not match docs/api/openapi.yaml -- run scripts/docs")
	}
}

func TestMarkdownRendersAnOperation(t *testing.T) {
	spec, err := Parse([]byte(`
openapi: 3.1.0
info: {title: Bard, version: "9.9", summary: A summary.}
paths:
  /api/v1/things/{id}:
    get:
      summary: Fetch a thing
      description: The long form.
      security: []
      parameters:
        - {name: id, in: path, required: true, description: Which thing.}
      responses:
        "200":
          description: The thing.
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Thing"}
components:
  schemas:
    Thing:
      type: object
      required: [id]
      properties:
        id: {type: integer, format: int64}
        tags: {type: array, items: {type: string}}
        owner: {$ref: "#/components/schemas/Thing"}
`))
	if err != nil {
		t.Fatal(err)
	}

	out := spec.Markdown()
	for _, want := range []string{
		"# Bard API",
		"Version `9.9`",
		"## `GET /api/v1/things/{id}`",
		"**Fetch a thing**",
		"The long form.",
		"No authentication required.",
		"| `id` | path | yes | Which thing. |",
		"| `200` | [`Thing`](#thing) | The thing. |",
		"### Thing",
		"| `id` | integer (int64) | yes |",
		"| `tags` | string[] |",
		"| `owner` | [`Thing`](#thing) |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output is missing %q", want)
		}
	}
}

// A description spanning lines would otherwise break out of its table row.
func TestMarkdownFlattensDescriptionsInTables(t *testing.T) {
	spec, err := Parse([]byte(`
openapi: 3.1.0
info: {title: T, version: "1"}
paths:
  /a:
    get:
      responses:
        "200":
          description: |
            One line.
            A second | with a pipe.
`))
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(spec.Markdown(), "\n") {
		if strings.HasPrefix(line, "| `200`") {
			// An escaped pipe is still a pipe character, and only the
			// unescaped ones are cell boundaries.
			if cells := strings.ReplaceAll(line, `\|`, ""); strings.Count(cells, "|") != 4 {
				t.Errorf("row has stray pipes or newlines: %q", line)
			}
			return
		}
	}
	t.Error("no row for the 200 response")
}
