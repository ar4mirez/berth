// Package integration holds tests that need a real Docker daemon. They are behind the
// "integration" build tag and run in GitHub Actions (integration.yml):
//
//	go test -tags integration ./test/integration/...
//
// The dev container has no Docker on purpose (docs/plan.md, "Dogfooding setup"), so a plain
// `go test ./...` there compiles nothing in this package.
package integration
