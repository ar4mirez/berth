package api

import (
	"os"
	"testing"
)

const specFile = "../../docs/api/openapi.json"

// TestOpenAPIIsCurrent: the committed document is what the routes and tools make (the handlers are
// made from the same table, so they can't differ from it).
func TestOpenAPIIsCurrent(t *testing.T) {
	if os.Getenv("BERTH_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(specFile, OpenAPI(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(OpenAPI()) {
		t.Errorf("%s is stale (BERTH_UPDATE_GOLDEN=1 go test ./internal/api rewrites it)", specFile)
	}
}
