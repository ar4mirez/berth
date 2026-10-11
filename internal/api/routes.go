// Package api is berth's HTTP API (#62): `berth serve`.
//
// It serves the same tools the MCP server has (internal/mcpsrv), each an operation of the catalog
// (internal/ops), which decides what a caller may do: a read always runs; a write needs a caller
// that may write; an operation that restarts a container needs one that may restart, and the org's
// name again as `confirm`. Secret values are never returned, and every write asked for is in the
// audit log.
package api

import (
	"reflect"
	"strings"
)

// Version is the API's: every path starts with it.
const Version = "v1"

// Route is one endpoint: a tool, and where its arguments come from. The ones named in braces in
// Path come from the path, the ones in Query from the query string, and the rest from a JSON body.
type Route struct {
	Method, Path string
	// Tool is the tool it serves, by its name in internal/mcpsrv.
	Tool  string
	Query []string
}

// Routes are the API's endpoints. The OpenAPI document (docs/api/openapi.json), the server and the
// Go client (internal/apiclient) are all made from this table, so they can't disagree.
var Routes = []Route{
	{Method: "GET", Path: "/v1/hosts", Tool: "hosts_list"},
	{Method: "GET", Path: "/v1/orgs", Tool: "orgs_list"},
	{Method: "GET", Path: "/v1/accounts", Tool: "org_accounts", Query: []string{"orgs"}},
	{Method: "GET", Path: "/v1/backups", Tool: "backups_list", Query: []string{"org"}},
	{Method: "POST", Path: "/v1/backups", Tool: "backup_create"},
	{Method: "GET", Path: "/v1/schedule", Tool: "schedule_status"},
	{Method: "GET", Path: "/v1/orgs/{org}", Tool: "org_info"},
	{Method: "GET", Path: "/v1/orgs/{org}/remote", Tool: "remote_status"},
	{Method: "GET", Path: "/v1/orgs/{org}/logs", Tool: "org_logs", Query: []string{"lines"}},
	{Method: "GET", Path: "/v1/orgs/{org}/firewall", Tool: "firewall_show"},
	{Method: "GET", Path: "/v1/orgs/{org}/firewall/presets", Tool: "firewall_presets"},
	{Method: "POST", Path: "/v1/orgs/{org}/firewall/test", Tool: "firewall_test"},
	{Method: "POST", Path: "/v1/orgs/{org}/firewall/allow", Tool: "firewall_allow"},
	{Method: "POST", Path: "/v1/orgs/{org}/firewall/deny", Tool: "firewall_deny"},
	{Method: "GET", Path: "/v1/orgs/{org}/repos", Tool: "repos_list"},
	{Method: "POST", Path: "/v1/orgs/{org}/repos", Tool: "repo_add"},
	{Method: "DELETE", Path: "/v1/orgs/{org}/repos/{dir}", Tool: "repo_remove"},
	{Method: "GET", Path: "/v1/orgs/{org}/env", Tool: "env_list"},
	{Method: "GET", Path: "/v1/orgs/{org}/packages", Tool: "packages_list"},
	{Method: "POST", Path: "/v1/orgs/{org}/up", Tool: "org_up"},
	{Method: "POST", Path: "/v1/orgs/{org}/restart", Tool: "org_restart"},
	{Method: "POST", Path: "/v1/orgs/{org}/down", Tool: "org_down"},
	// More of the catalog (#167): what the dashboards do beyond the first set.
	{Method: "POST", Path: "/v1/orgs", Tool: "org_create"},
	{Method: "DELETE", Path: "/v1/orgs/{org}", Tool: "org_destroy"},
	{Method: "POST", Path: "/v1/orgs/{org}/firewall/on", Tool: "firewall_on"},
	{Method: "POST", Path: "/v1/orgs/{org}/firewall/off", Tool: "firewall_off"},
	{Method: "POST", Path: "/v1/orgs/{org}/firewall/reload", Tool: "firewall_reload"},
	{Method: "POST", Path: "/v1/orgs/{org}/repos/sync", Tool: "repo_sync"},
	{Method: "GET", Path: "/v1/packages/presets", Tool: "package_presets"},
	{Method: "POST", Path: "/v1/orgs/{org}/packages", Tool: "packages_add"},
	{Method: "POST", Path: "/v1/orgs/{org}/packages/remove", Tool: "packages_remove"},
	{Method: "POST", Path: "/v1/orgs/{org}/remote/restart", Tool: "remote_restart"},
}

// PathArgs are the arguments a route takes from its path.
func (r Route) PathArgs() []string {
	var out []string
	for _, seg := range strings.Split(r.Path, "/") {
		if strings.HasPrefix(seg, "{") {
			out = append(out, strings.Trim(seg, "{}"))
		}
	}
	return out
}

// Arg is one argument of a tool, from its input type.
type Arg struct {
	Name, Description string
	Type              reflect.Type
	// Required is false for an argument the tool has a default for (omitempty).
	Required bool
	// In is where a route takes it from: "path", "query" or "body".
	In string
	// Field is its field in the input type.
	Field string
}

// Args are a route's arguments, in the input type's order.
func (r Route) Args(in reflect.Type) []Arg {
	var out []Arg
	for i := 0; i < in.NumField(); i++ {
		f := in.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		a := Arg{Name: name, Description: f.Tag.Get("jsonschema"), Type: f.Type, Required: !strings.Contains(opts, "omitempty"), In: "body", Field: f.Name}
		for _, p := range r.PathArgs() {
			if p == name {
				a.In, a.Required = "path", true
			}
		}
		for _, q := range r.Query {
			if q == name {
				a.In = "query"
			}
		}
		if name == "confirm" {
			a.Description = "the org's name again, exactly as in the path: the confirmation that stopping the work running in it was agreed to"
		}
		out = append(out, a)
	}
	return out
}
