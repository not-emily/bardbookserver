package docs

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const metaSchemaPath = "testdata/openapi-3.1-schema.json"

func load(t *testing.T) *Spec {
	t.Helper()
	spec, err := Load()
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	return spec
}

// asJSON is what a consumer actually fetches: the YAML round-tripped through
// JSON, which is also the only form a JSON Schema validator will accept.
func asJSON(t *testing.T, spec *Spec) any {
	t.Helper()

	encoded, err := json.Marshal(spec.Document)
	if err != nil {
		t.Fatalf("spec is not representable as JSON: %v", err)
	}

	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func metaSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()

	file, err := os.Open(metaSchemaPath)
	if err != nil {
		t.Fatalf("open meta-schema: %v", err)
	}
	defer file.Close()

	doc, err := jsonschema.UnmarshalJSON(file)
	if err != nil {
		t.Fatalf("parse meta-schema: %v", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(metaSchemaPath, doc); err != nil {
		t.Fatalf("add meta-schema: %v", err)
	}
	schema, err := compiler.Compile(metaSchemaPath)
	if err != nil {
		t.Fatalf("compile meta-schema: %v", err)
	}
	return schema
}

func TestSpecIsValidOpenAPI31(t *testing.T) {
	if err := metaSchema(t).Validate(asJSON(t, load(t))); err != nil {
		t.Errorf("openapi.yaml is not valid OpenAPI 3.1:\n%v", err)
	}
}

// Proving the validator bites, rather than trusting that a passing spec means
// it was checked at all.
func TestMetaSchemaRejectsAMalformedSpec(t *testing.T) {
	broken := map[string]any{"openapi": 31, "info": "not an object"}

	if err := metaSchema(t).Validate(broken); err == nil {
		t.Fatal("the meta-schema accepted a spec with the wrong types")
	}
}

func TestOperationsAreFoundAndSorted(t *testing.T) {
	spec, err := Parse([]byte(`
openapi: 3.1.0
info: {title: T, version: "1"}
paths:
  /b:
    get: {summary: second}
  /a:
    post: {summary: first}
    get: {summary: also first}
    parameters: []
`))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, op := range spec.Operations() {
		got = append(got, op.String())
	}

	want := []string{"GET /a", "POST /a", "GET /b"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("operations = %v, want %v", got, want)
	}
}

// `parameters` sits beside the verbs under a path item and is not an
// operation; counting it would make route coverage compare nonsense.
func TestNonMethodKeysAreNotOperations(t *testing.T) {
	spec, err := Parse([]byte(`
openapi: 3.1.0
info: {title: T, version: "1"}
paths:
  /a:
    summary: a path, not an operation
    description: nor this
    parameters: []
    servers: []
`))
	if err != nil {
		t.Fatal(err)
	}

	if ops := spec.Operations(); len(ops) != 0 {
		t.Errorf("found %v, want none", ops)
	}
}

func TestOperationLookup(t *testing.T) {
	spec := load(t)

	if _, ok := spec.Operation("POST", "/api/v1/auth/login"); !ok {
		t.Error("login is not findable in the spec")
	}
	if _, ok := spec.Operation("GET", "/api/v1/invented"); ok {
		t.Error("an operation the spec does not contain was found")
	}
}
