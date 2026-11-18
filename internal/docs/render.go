package docs

import (
	"fmt"
	"sort"
	"strings"
)

// Markdown renders the reference. It is deliberately plain: the output is read
// on GitHub, so it carries no assets and nothing has to be served.
func (s *Spec) Markdown() string {
	var b strings.Builder

	info, _ := s.Document["info"].(map[string]any)
	fmt.Fprintf(&b, "# %s API\n\n", str(info["title"]))
	b.WriteString("<!-- Generated from docs/api/openapi.yaml by scripts/docs. Do not edit. -->\n\n")

	if summary := str(info["summary"]); summary != "" {
		fmt.Fprintf(&b, "%s\n\n", summary)
	}
	fmt.Fprintf(&b, "Version `%s`.\n\n", str(info["version"]))

	if description := str(info["description"]); description != "" {
		b.WriteString(strings.TrimRight(description, "\n") + "\n\n")
	}

	s.writeServers(&b)
	s.writeContents(&b)
	s.writeOperations(&b)
	s.writeSchemas(&b)

	return b.String()
}

func (s *Spec) writeServers(b *strings.Builder) {
	servers, _ := s.Document["servers"].([]any)
	if len(servers) == 0 {
		return
	}

	b.WriteString("## Servers\n\n")
	for _, entry := range servers {
		server, _ := entry.(map[string]any)
		fmt.Fprintf(b, "- `%s`", str(server["url"]))
		if description := str(server["description"]); description != "" {
			fmt.Fprintf(b, " — %s", description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func (s *Spec) writeContents(b *strings.Builder) {
	operations := s.Operations()
	if len(operations) == 0 {
		return
	}

	b.WriteString("## Endpoints\n\n")
	for _, op := range operations {
		fmt.Fprintf(b, "- [`%s %s`](#%s) — %s\n",
			op.Method, op.Path, anchor(op), str(op.Detail["summary"]))
	}
	b.WriteString("\n")
}

func (s *Spec) writeOperations(b *strings.Builder) {
	for _, op := range s.Operations() {
		fmt.Fprintf(b, "## `%s %s`\n\n", op.Method, op.Path)

		if summary := str(op.Detail["summary"]); summary != "" {
			fmt.Fprintf(b, "**%s**\n\n", summary)
		}
		if description := str(op.Detail["description"]); description != "" {
			b.WriteString(strings.TrimRight(description, "\n") + "\n\n")
		}

		// An operation-level `security: []` is how the spec says "no token
		// needed", and that is worth stating rather than leaving to inference.
		if security, ok := op.Detail["security"].([]any); ok && len(security) == 0 {
			b.WriteString("No authentication required.\n\n")
		}

		writeParameters(b, op)
		writeRequestBody(b, op)
		writeResponses(b, op)
	}
}

func writeParameters(b *strings.Builder, op Operation) {
	params, _ := op.Detail["parameters"].([]any)
	if len(params) == 0 {
		return
	}

	b.WriteString("| Parameter | In | Required | Description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, entry := range params {
		param, _ := entry.(map[string]any)
		required := ""
		if yes, _ := param["required"].(bool); yes {
			required = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n",
			str(param["name"]), str(param["in"]), required, oneLine(str(param["description"])))
	}
	b.WriteString("\n")
}

func writeRequestBody(b *strings.Builder, op Operation) {
	body, ok := op.Detail["requestBody"].(map[string]any)
	if !ok {
		return
	}

	b.WriteString("### Request body\n\n")
	if name := schemaName(body); name != "" {
		fmt.Fprintf(b, "[`%s`](#%s)", name, strings.ToLower(name))
		if required, _ := body["required"].(bool); required {
			b.WriteString(", required")
		}
		b.WriteString("\n\n")
	}
}

func writeResponses(b *strings.Builder, op Operation) {
	responses, ok := op.Detail["responses"].(map[string]any)
	if !ok {
		return
	}

	codes := make([]string, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	b.WriteString("### Responses\n\n")
	b.WriteString("| Status | Body | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, code := range codes {
		response, _ := responses[code].(map[string]any)

		body := "—"
		if name := schemaName(response); name != "" {
			body = fmt.Sprintf("[`%s`](#%s)", name, strings.ToLower(name))
		}
		fmt.Fprintf(b, "| `%s` | %s | %s |\n",
			code, body, oneLine(str(response["description"])))
	}
	b.WriteString("\n")
}

func (s *Spec) writeSchemas(b *strings.Builder) {
	schemas := s.Schemas()
	if len(schemas) == 0 {
		return
	}

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	b.WriteString("## Schemas\n\n")
	for _, name := range names {
		schema, _ := schemas[name].(map[string]any)
		fmt.Fprintf(b, "### %s\n\n", name)

		if description := str(schema["description"]); description != "" {
			b.WriteString(strings.TrimRight(description, "\n") + "\n\n")
		}
		writeProperties(b, schema)
	}
}

func writeProperties(b *strings.Builder, schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return
	}

	required := map[string]bool{}
	if list, ok := schema["required"].([]any); ok {
		for _, name := range list {
			required[str(name)] = true
		}
	}

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	b.WriteString("| Field | Type | Required | Description |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, name := range names {
		property, _ := properties[name].(map[string]any)

		yes := ""
		if required[name] {
			yes = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n",
			name, typeOf(property), yes, oneLine(str(property["description"])))
	}
	b.WriteString("\n")
}

// typeOf renders a property's type, following a `$ref` to its schema name so
// the table links rather than saying "object".
func typeOf(property map[string]any) string {
	if ref := str(property["$ref"]); ref != "" {
		name := ref[strings.LastIndex(ref, "/")+1:]
		return fmt.Sprintf("[`%s`](#%s)", name, strings.ToLower(name))
	}

	kind := str(property["type"])
	if kind == "array" {
		if items, ok := property["items"].(map[string]any); ok {
			return typeOf(items) + "[]"
		}
		return "array"
	}
	if format := str(property["format"]); format != "" && kind != "" {
		return fmt.Sprintf("%s (%s)", kind, format)
	}
	if kind == "" {
		return "any"
	}
	return kind
}

// schemaName reads the component name out of a request or response body,
// which is the only form the reference links to.
func schemaName(node map[string]any) string {
	content, _ := node["content"].(map[string]any)
	for _, media := range content {
		entry, _ := media.(map[string]any)
		schema, _ := entry["schema"].(map[string]any)
		if ref := str(schema["$ref"]); ref != "" {
			return ref[strings.LastIndex(ref, "/")+1:]
		}
	}
	return ""
}

func anchor(op Operation) string {
	slug := strings.ToLower(op.Method + " " + op.Path)
	slug = strings.NewReplacer("/", "", "{", "", "}", "", " ", "-").Replace(slug)
	return slug
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|"))
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
