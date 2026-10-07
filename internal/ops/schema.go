package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Document is one kind of JSON document berth prints: its schema name, what prints it, and its Go
// type.
type Document struct {
	// Name is the document's "schema" value: "berth.orgs/v1".
	Name string
	// From is the command that prints it.
	From string
	// Type is a value of its Go type.
	Type any
}

// Documents is every document `--output json` prints (docs/json.md). Each has a JSON Schema in
// docs/schemas, generated from its type; the tests fail when a file is stale, when a golden
// document doesn't validate, and when a type with a schema name is missing here.
var Documents = []Document{
	{"berth.orgs/v1", "ls", Orgs{}},
	{"berth.info/v1", "info <org>", Info{}},
	{"berth.whoami/v1", "whoami [org...]", Whoami{}},
	{"berth.remote/v1", "remote <org> status", RemoteStatus{}},
	{"berth.env/v1", "env <org> ls", Env{}},
	{"berth.firewall/v1", "fw <org> show", Firewall{}},
	{"berth.firewall-presets/v1", "fw <org> presets", Presets{}},
	{"berth.firewall-test/v1", "fw <org> test [host...]", FirewallTest{}},
	{"berth.repos/v1", "repo ls <org>", Repos{}},
	{"berth.repo-audit/v1", "repo audit <org>", RepoAuditDoc{}},
	{"berth.repo-policy/v1", "repo policy <org>", RepoPolicy{}},
	{"berth.schedule/v1", "schedule status", Schedule{}},
	{"berth.hosts/v1", "host ls", Hosts{}},
	{"berth.host-guard/v1", "host guard <name> status", HostGuard{}},
	{"berth.default-org/v1", "use", DefaultOrg{}},
	{"berth.image/v1", "image-tag", Image{}},
	{"berth.error/v1", "any command that fails (on stderr)", ErrorDoc{}},
	{"berth.event/v1", "up, restart, build, pull, backup, restore, logs, remote <org> logs (one per line)", Event{}},
}

// SchemaFile is a document's schema file name: "berth.orgs/v1" is "orgs.v1.json".
func SchemaFile(name string) string {
	return strings.NewReplacer("berth.", "", "/", ".").Replace(name) + ".json"
}

// SchemaBase is where the schemas are published.
const SchemaBase = "https://ar4mirez.github.io/berth/schemas/"

// Schema is d's JSON Schema (draft 2020-12), from its Go type: every field is required, nothing
// else is allowed, a pointer may be null, and "schema" is the document's name. Adding a field
// changes the file and keeps the version; removing or renaming one needs a new version.
func (d Document) Schema() ([]byte, error) {
	s := schemaOf(reflect.TypeOf(d.Type))
	props := s["properties"].(map[string]any)
	if _, ok := props["schema"]; !ok {
		return nil, fmt.Errorf("%s: its type has no schema field", d.Name)
	}
	props["schema"] = map[string]any{"const": d.Name}
	out := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         SchemaBase + SchemaFile(d.Name),
		"title":       d.Name,
		"description": "berth " + d.From + " --output json",
	}
	for k, v := range s {
		out[k] = v
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// fields are a struct's JSON fields, embedded structs flattened, `json:"-"` left out.
func fields(t reflect.Type) []reflect.StructField {
	var out []reflect.StructField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case f.Anonymous && f.Type.Kind() == reflect.Struct && name == "":
			out = append(out, fields(f.Type)...)
		case name == "-" || !f.IsExported():
		default:
			out = append(out, f)
		}
	}
	return out
}

func schemaOf(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.Pointer:
		s := schemaOf(t.Elem())
		if typ, ok := s["type"].(string); ok {
			s["type"] = []any{typ, "null"}
		}
		return s
	case reflect.Struct:
		props, required := map[string]any{}, []any{}
		for _, f := range fields(t) {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" {
				name = f.Name
			}
			props[name] = schemaOf(f.Type)
			required = append(required, name)
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	}
	panic("ops: no JSON Schema for " + t.String())
}

// Validate checks a decoded JSON value against a schema Schema wrote (the keywords it uses: type,
// properties, required, additionalProperties, items, const). It returns every problem, by path.
func Validate(schema map[string]any, v any) []string {
	var problems []string
	validate(schema, v, "$", &problems)
	sort.Strings(problems)
	return problems
}

func validate(s map[string]any, v any, at string, problems *[]string) {
	bad := func(format string, args ...any) { *problems = append(*problems, at+": "+fmt.Sprintf(format, args...)) }
	if c, ok := s["const"]; ok {
		if v != c {
			bad("is %v, not %v", v, c)
		}
		return
	}
	types := []any{s["type"]}
	if l, ok := s["type"].([]any); ok {
		types = l
	}
	is := ""
	switch x := v.(type) {
	case nil:
		is = "null"
	case string:
		is = "string"
	case bool:
		is = "boolean"
	case float64:
		is = "number"
		if x == float64(int64(x)) {
			is = "integer"
		}
	case []any:
		is = "array"
	case map[string]any:
		is = "object"
	}
	ok := false
	for _, t := range types {
		ok = ok || t == is
	}
	if !ok {
		bad("is %s, not %v", is, s["type"])
		return
	}
	switch x := v.(type) {
	case []any:
		items, _ := s["items"].(map[string]any)
		for i, e := range x {
			validate(items, e, fmt.Sprintf("%s[%d]", at, i), problems)
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		for _, r := range s["required"].([]any) {
			if _, ok := x[r.(string)]; !ok {
				bad("lacks %q", r)
			}
		}
		for k, e := range x {
			p, known := props[k].(map[string]any)
			if !known {
				bad("has %q, which the schema doesn't", k)
				continue
			}
			validate(p, e, at+"."+k, problems)
		}
	}
}
