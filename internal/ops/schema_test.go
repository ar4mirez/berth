package ops

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	schemaDir = "../../docs/schemas"
	goldenDir = "../../test/parity/testdata/json"
	jsonDoc   = "../../docs/json.md"
)

func decode(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestSchemasAreCurrent: docs/schemas has exactly one file per document, and each is what its Go
// type generates now. BERTH_UPDATE_GOLDEN=1 rewrites them after an intended change: a field that
// was added keeps the version, and one that was removed or renamed needs a new one (docs/json.md).
func TestSchemasAreCurrent(t *testing.T) {
	update := os.Getenv("BERTH_UPDATE_GOLDEN") != ""
	want := map[string]bool{}
	for _, d := range Documents {
		file := SchemaFile(d.Name)
		if want[file] {
			t.Errorf("%s: two documents share %s", d.Name, file)
		}
		want[file] = true
		got, err := d.Schema()
		if err != nil {
			t.Error(err)
			continue
		}
		p := filepath.Join(schemaDir, file)
		if update {
			if err := os.MkdirAll(schemaDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: %v (BERTH_UPDATE_GOLDEN=1 go test ./internal/ops writes it)", d.Name, err)
		} else if string(have) != string(got) {
			t.Errorf("%s is stale: %s's type changed (BERTH_UPDATE_GOLDEN=1 go test ./internal/ops rewrites it)", p, d.Name)
		}
	}
	files, err := os.ReadDir(schemaDir)
	if err != nil && !update {
		t.Fatal(err)
	}
	for _, f := range files {
		if !want[f.Name()] && strings.HasSuffix(f.Name(), ".json") {
			t.Errorf("%s/%s is no document's schema", schemaDir, f.Name())
		}
	}
}

// TestDocumentsValidate: every golden document (test/parity/testdata/json) validates against its
// schema, and so does a document of each kind that has no golden file.
func TestDocumentsValidate(t *testing.T) {
	schemas := map[string]map[string]any{}
	for _, d := range Documents {
		b, err := d.Schema()
		if err != nil {
			t.Fatal(err)
		}
		schemas[d.Name] = decode(t, b).(map[string]any)
	}
	check := func(what string, doc any) string {
		m, _ := doc.(map[string]any)
		name, _ := m["schema"].(string)
		s, ok := schemas[name]
		if !ok {
			t.Errorf("%s: schema %q isn't in ops.Documents", what, name)
			return ""
		}
		for _, p := range Validate(s, doc) {
			t.Errorf("%s (%s): %s", what, name, p)
		}
		return name
	}
	seen := map[string]bool{}
	files, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(goldenDir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		seen[check(f.Name(), decode(t, b))] = true
	}
	if len(files) < 15 {
		t.Fatalf("only %d golden documents in %s", len(files), goldenDir)
	}
	// The rest: built here, as the code builds them.
	g, _ := GetHostGuard(t.Context(), nil2{}, "box1")
	doc := fail(KindNotFound, "run: berth init acme", "unknown org 'acme'").Doc()
	var events []Event
	p := NewProgress(func(e Event) { events = append(events, e) }, "up", "acme")
	p.Step("image", "getting the image")
	_, _ = p.Writer("stdout").Write([]byte("line\n"))
	p.Done(fail(KindCommand, "", "docker exited with status 1"))
	samples := []any{g, Image{Schema: "berth.image/v1", Tag: "berth/claude-env:0123456789ab"}, doc}
	for _, e := range events {
		samples = append(samples, e)
	}
	for _, s := range samples {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		seen[check(reflect.TypeOf(s).Name(), decode(t, b))] = true
	}
	for _, d := range Documents {
		if !seen[d.Name] {
			t.Errorf("%s: no document of this kind was validated (add a golden case in test/parity, or a sample here)", d.Name)
		}
	}

	// The validator itself: a schema it wrote refuses what doesn't fit.
	bad := decode(t, []byte(`{"schema":"berth.image/v2","tag":7,"extra":true}`))
	want := "$.schema: is berth.image/v2, not berth.image/v1\n$.tag: is integer, not string\n$: has \"extra\", which the schema doesn't"
	if got := strings.Join(Validate(schemas["berth.image/v1"], bad), "\n"); got != want {
		t.Errorf("validator:\n%s\nwant:\n%s", got, want)
	}
	if got := Validate(schemas["berth.default-org/v1"], decode(t, []byte(`{"schema":"berth.default-org/v1"}`))); len(got) != 1 || got[0] != `$: lacks "org"` {
		t.Errorf("validator, missing field: %v", got)
	}
	if got := Validate(schemas["berth.default-org/v1"], decode(t, []byte(`{"schema":"berth.default-org/v1","org":null}`))); len(got) != 0 {
		t.Errorf("validator, null for a pointer: %v", got)
	}
}

// nil2 is a System with no guard installed.
type nil2 struct{ System }

func (nil2) Silent(context.Context, ...string) bool { return false }

// TestEveryDocumentIsRegistered: a type in this package with a `schema` field is in Documents, and
// docs/json.md has a section for every document.
func TestEveryDocumentIsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, d := range Documents {
		registered[reflect.TypeOf(d.Type).Name()] = true
	}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			ts, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				if fld.Tag != nil && strings.Contains(fld.Tag.Value, `json:"schema"`) {
					n++
					if !registered[ts.Name.Name] {
						t.Errorf("%s has a schema field but isn't in ops.Documents", ts.Name.Name)
					}
				}
			}
			return true
		})
	}
	if n != len(Documents) {
		t.Errorf("%d types have a schema field, and Documents has %d", n, len(Documents))
	}
	md, err := os.ReadFile(jsonDoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Documents {
		if !strings.Contains(string(md), "`"+d.Name+"`") {
			t.Errorf("docs/json.md has no section for %s", d.Name)
		}
		if !strings.Contains(string(md), "schemas/"+SchemaFile(d.Name)) {
			t.Errorf("docs/json.md doesn't link %s's schema (schemas/%s)", d.Name, SchemaFile(d.Name))
		}
	}
}
