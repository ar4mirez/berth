package mcpsrv

import (
	"reflect"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// Field is one argument of a tool, as a form shows it (#167): the dashboards make a form for a
// tool from these, as the API makes its routes and its document from the same input types.
type Field struct {
	Name string
	// Kind is "string", "bool", "int" or "list" (of strings).
	Kind     string
	Required bool
	// Secret is true for an argument that holds a secret: a form doesn't show what was typed.
	Secret bool
	// Help is the argument's description.
	Help string
}

// Fields are the tool's arguments, in its input type's order.
func (s Spec) Fields() []Field {
	var out []Field
	if s.In == nil || s.In.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < s.In.NumField(); i++ {
		f := s.In.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		kind := "string"
		switch f.Type.Kind() {
		case reflect.Bool:
			kind = "bool"
		case reflect.Int:
			kind = "int"
		case reflect.Slice:
			kind = "list"
		}
		out = append(out, Field{Name: name, Kind: kind, Required: !strings.Contains(opts, "omitempty"), Help: f.Tag.Get("jsonschema"), Secret: f.Tag.Get("secret") != ""})
	}
	return out
}

// Access is what a caller needs for the tool: "read", "write", "restart" or "admin".
func (s Spec) Access() string {
	switch {
	case s.Op.Admin:
		return "admin"
	case s.Op.Restart != ops.Never:
		return "restart"
	case s.Op.Access == ops.Write:
		return "write"
	}
	return "read"
}
