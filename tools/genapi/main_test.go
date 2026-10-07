package main

import (
	"os"
	"testing"
)

// TestClientIsCurrent: the committed client is what the route table makes.
func TestClientIsCurrent(t *testing.T) {
	want, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../internal/apiclient/client_gen.go")
	if err != nil || string(got) != string(want) {
		t.Errorf("internal/apiclient/client_gen.go is stale (run: go run ./tools/genapi): %v", err)
	}
}
