package app

import (
	"encoding/json"
)

// Output formats (`--output`).
const (
	OutputText = "text"
	OutputJSON = "json"
)

// writeJSON prints v as indented JSON: the result of an operation that returns data (internal/ops,
// docs/json.md).
func (a *App) writeJSON(v any) error {
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
