package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"

	"github.com/ar4mirez/berth/internal/ops"
)

func argSchema(a Arg) map[string]any {
	s := map[string]any{}
	switch a.Type.Kind() {
	case reflect.Slice:
		s["type"], s["items"] = "array", map[string]any{"type": "string"}
	case reflect.Int:
		s["type"] = "integer"
	case reflect.Bool:
		s["type"] = "boolean"
	default:
		s["type"] = "string"
	}
	return s
}

// access is what a caller needs for an operation: "read", "write" or "restart".
func access(op ops.Op) string {
	switch {
	case op.Restart != ops.Never:
		return "restart"
	case op.Access == ops.Write:
		return "write"
	}
	return "read"
}

var (
	specOnce sync.Once
	specJSON []byte
)

// OpenAPI is the API's OpenAPI 3.1 document, made from Routes and the tools they serve. It is
// committed as docs/api/openapi.json, which a test keeps current.
func OpenAPI() []byte {
	specOnce.Do(func() {
		b, err := json.MarshalIndent(openAPI(), "", "  ")
		if err != nil {
			panic(err)
		}
		b = append(b, '\n')
		specJSON = b
	})
	return specJSON
}

func openAPI() map[string]any {
	specs := Specs()
	errRef := map[string]any{"$ref": "#/components/schemas/Error"}
	errResp := func(what string) map[string]any {
		return map[string]any{"description": what, "content": map[string]any{"application/json": map[string]any{"schema": errRef}}}
	}
	paths := map[string]any{}
	add := func(path, method string, op map[string]any) {
		p, _ := paths[path].(map[string]any)
		if p == nil {
			p = map[string]any{}
			paths[path] = p
		}
		p[strings.ToLower(method)] = op
	}
	for _, r := range Routes {
		spec := specs[r.Tool]
		summary, _, _ := strings.Cut(spec.Description, ". ")
		op := map[string]any{
			"operationId": r.Tool, "summary": strings.TrimSuffix(summary, "."), "description": spec.Description,
			"x-berth-operation": strings.TrimSpace(spec.Cmd + " " + spec.Sub), "x-berth-access": access(spec.Op),
		}
		var params []any
		body, required := map[string]any{}, []any{}
		for _, a := range r.Args(spec.In) {
			s := argSchema(a)
			if a.In == "body" {
				s["description"] = a.Description
				body[a.Name] = s
				if a.Required {
					required = append(required, a.Name)
				}
				continue
			}
			params = append(params, map[string]any{"name": a.Name, "in": a.In, "required": a.Required, "description": a.Description, "schema": s})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if len(body) > 0 {
			schema := map[string]any{"type": "object", "properties": body, "additionalProperties": false}
			if len(required) > 0 {
				schema["required"] = required
			}
			op["requestBody"] = map[string]any{"required": len(required) > 0, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
		}
		ok := map[string]any{"application/json": map[string]any{"schema": ops.SchemaOf(spec.Out)}}
		if spec.Op.Access == ops.Write {
			ok["text/event-stream"] = map[string]any{"schema": map[string]any{"type": "string",
				"description": "With Accept: text/event-stream: the operation's progress as berth.event/v1 events (start, step, output, then done or failed), then a `result` event with the document."}}
		}
		responses := map[string]any{
			"200": map[string]any{"description": "The result.", "content": ok},
			"400": errResp("The arguments don't fit."),
			"401": errResp("No token, or a wrong one (TCP only)."),
			"404": errResp("No such org, host or endpoint."),
			"409": errResp("The org isn't in a state for this (not running, say)."),
			"500": errResp("The operation failed."),
		}
		if spec.Op.Access == ops.Write {
			responses["403"] = errResp("This caller may not do this: read-only, a token without the scope, or a restart without `confirm`.")
		}
		op["responses"] = responses
		add(r.Path, r.Method, op)
	}
	add("/v1", "GET", map[string]any{"operationId": "api_info", "summary": "What is serving, and what this caller may do",
		"x-berth-access": "read", "responses": map[string]any{"200": map[string]any{"description": "The server.",
			"content": map[string]any{"application/json": map[string]any{"schema": ops.SchemaOf(reflect.TypeFor[Info]())}}}}})
	add("/v1/openapi.json", "GET", map[string]any{"operationId": "api_openapi", "summary": "This document", "x-berth-access": "read",
		"responses": map[string]any{"200": map[string]any{"description": "The OpenAPI document.", "content": map[string]any{"application/json": map[string]any{}}}}})
	add("/v1/orgs/{org}/logs/follow", "GET", map[string]any{"operationId": "org_logs_follow", "summary": "Follow an org's container log",
		"description":       "The log as server-sent events (berth.event/v1): one `output` event per line, until the client closes the connection.",
		"x-berth-operation": "logs", "x-berth-access": "read",
		"parameters": []any{map[string]any{"name": "org", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}},
		"responses":  map[string]any{"200": map[string]any{"description": "The events.", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}}}})
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "berth", "version": Version, "license": map[string]any{"name": "MIT"},
			"description": "berth's API, served by `berth serve`. Each endpoint is one of berth's operations, with the rules the command line has: " +
				"an operation that changes something needs a caller that may write; one that restarts a container needs a caller that may restart, " +
				"and the org's name again as `confirm`. Secret values are never returned. Errors are berth.error/v1 documents."},
		"paths": paths,
		"components": map[string]any{
			"schemas":         map[string]any{"Error": ops.SchemaOf(reflect.TypeFor[ops.ErrorDoc]())},
			"securitySchemes": map[string]any{"token": map[string]any{"type": "http", "scheme": "bearer", "description": "For TCP. The Unix socket needs none: its file permissions are the access control."}},
		},
		"security": []any{map[string]any{}, map[string]any{"token": []any{}}},
	}
}
