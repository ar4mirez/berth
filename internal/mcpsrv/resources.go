package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ar4mirez/berth"
	"github.com/ar4mirez/berth/internal/ops"
)

// Resources: the orgs' state, and berth's documentation as reference.
//
//	berth://orgs           every org and its status (as orgs_list)
//	berth://orgs/{org}     one org's connection sheet (as org_info)
//	berth://docs/{page}    a page of the docs, or PARITY.md
func (s *server) resources() {
	s.mcp.AddResource(&mcp.Resource{URI: "berth://orgs", Name: "orgs", Title: "Orgs", MIMEType: "application/json",
		Description: "Every org, here and on registered hosts, with its status."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			var out, errb bytes.Buffer
			return jsonResource(req.Params.URI, ops.ListOrgs(ctx, s.opts.NewApp(&out, &errb)))
		})
	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "berth://orgs/{org}", Name: "org", Title: "An org", MIMEType: "application/json",
		Description: "An org's connection sheet: state, address, ports, git public key. Never a password."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			c := &call{s: s}
			c.app = s.opts.NewApp(&c.stdout, &c.stderr)
			defer func() {
				for _, d := range c.done {
					d()
				}
			}()
			b, o, err := c.at(ctx, strings.TrimPrefix(req.Params.URI, "berth://orgs/"))
			if err != nil {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			info, err := ops.GetInfo(ctx, b, o)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return jsonResource(req.Params.URI, info)
		})
	for _, p := range DocPages() {
		title := docTitle(p)
		s.mcp.AddResource(&mcp.Resource{URI: "berth://docs/" + p, Name: p, Title: title, MIMEType: "text/markdown",
			Description: "berth's documentation: " + title},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				b, err := berth.Docs.ReadFile(docFile(strings.TrimPrefix(req.Params.URI, "berth://docs/")))
				if err != nil {
					return nil, mcp.ResourceNotFoundError(req.Params.URI)
				}
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: string(b)}}}, nil
			})
	}
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: b.String()}}}, nil
}

// DocPages are the documentation pages served, as they are named in a berth://docs/ URI:
// "getting-started", "guides/firewall", "PARITY".
func DocPages() []string {
	var pages []string
	_ = fs.WalkDir(berth.Docs, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			pages = append(pages, strings.TrimSuffix(strings.TrimPrefix(p, "docs/"), ".md"))
		}
		return nil
	})
	return pages
}

func docFile(page string) string {
	if page == "PARITY" {
		return "PARITY.md"
	}
	return "docs/" + page + ".md"
}

// docTitle is a page's first heading, or its name.
func docTitle(page string) string {
	b, err := berth.Docs.ReadFile(docFile(page))
	if err != nil {
		return page
	}
	for _, l := range strings.Split(string(b), "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return t
		}
	}
	return page
}
