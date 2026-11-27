package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/mcpsrv"
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

// TestAPIDocumentIsDocumented: docs/json.md has a section for berth.api/v1 (#216), and it names
// every field GET /v1 answers with, so the document can't gain a field the docs don't have.
func TestAPIDocumentIsDocumented(t *testing.T) {
	srv := httptest.NewServer(Handler(Options{Version: "test", Caller: func(*http.Request) (mcpsrv.Caller, error) { return mcpsrv.Caller{}, nil }}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || doc["schema"] != "berth.api/v1" {
		t.Fatalf("GET /v1: %v %v", doc, err)
	}
	b, err := os.ReadFile("../../docs/json.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(b), "## `berth.api/v1`: `GET /v1`")
	section, _, _ = strings.Cut(section, "\n## ")
	if !ok {
		t.Fatal("docs/json.md has no section for berth.api/v1")
	}
	for k := range doc {
		if !strings.Contains(section, "`"+k+"`") && !strings.Contains(section, `"`+k+`"`) {
			t.Errorf("docs/json.md's berth.api/v1 doesn't name %q", k)
		}
	}
}
