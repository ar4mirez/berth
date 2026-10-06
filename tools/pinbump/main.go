// Command pinbump bumps the image's pinned tools (gh, ttyd, mise, Claude Code) together with their
// checksums (#69), for the pinbump workflow: GitHub's own Actions and token, no third-party apps.
//
// Each `# pin: datasource=… depName=…` block in image/Dockerfile is resolved to its latest release.
// A checksum comes from the project's own published file (GitHub releases) or the registry's signed
// integrity (npm), never from downloading the tool and hashing it here, and it's cross-checked:
// against GitHub's own asset digest, or npm's registry signature. A mismatch is refused.
//
//	pinbump -list                         the pinned deps, one per line
//	pinbump [-only dep] [-write] [-out f] what's newer; -write updates the Dockerfile, -out writes
//	                                      the bump (branch, title, PR body) as JSON, or nothing
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func main() {
	dockerfile := flag.String("dockerfile", "image/Dockerfile", "the Dockerfile with the pins")
	list := flag.Bool("list", false, "print the pinned deps")
	only := flag.String("only", "", "only this dep (depName)")
	write := flag.Bool("write", false, "update the Dockerfile")
	out := flag.String("out", "", "write the bump as JSON here (empty file: nothing to bump); needs -only")
	flag.Parse()
	if err := run(*dockerfile, *list, *only, *write, *out); err != nil {
		fmt.Fprintln(os.Stderr, "pinbump:", err)
		os.Exit(1)
	}
}

func run(dockerfile string, list bool, only string, write bool, out string) error {
	src, err := os.ReadFile(dockerfile) // #nosec G304 -- a CI tool: reads the Dockerfile it is given
	if err != nil {
		return err
	}
	pins, err := ParsePins(src)
	if err != nil {
		return err
	}
	if list {
		for _, p := range pins {
			fmt.Println(p.Dep)
		}
		return nil
	}
	if out != "" && only == "" {
		return errors.New("-out needs -only: one PR per tool")
	}
	if out != "" {
		if err := os.WriteFile(out, nil, 0o600); err != nil {
			return err
		}
	}
	ctx, f := context.Background(), httpFetcher{token: os.Getenv("GITHUB_TOKEN")}
	found := false
	for _, p := range pins {
		if only != "" && p.Dep != only {
			continue
		}
		found = true
		b, err := Resolve(ctx, f, p, time.Now())
		if err != nil {
			return err
		}
		if len(b.Set) == 0 {
			fmt.Printf("%s: %s is the latest\n", p.Dep, b.From)
			continue
		}
		fmt.Printf("%s: %s -> %s\n", p.Dep, b.From, b.To)
		if write {
			if src, err = Apply(src, b.Set); err != nil {
				return err
			}
			if err := os.WriteFile(dockerfile, src, 0o644); err != nil { // #nosec G306 G703 -- a CI tool: the tracked Dockerfile it is given
				return err
			}
		}
		if out != "" {
			j, err := json.MarshalIndent(Proposal(b), "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, j, 0o600); err != nil { // #nosec G703 -- a CI tool: the file it is given
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("no pin for %q in %s", only, dockerfile)
	}
	return nil
}

// PR is a bump as the workflow opens it.
type PR struct {
	Dep    string `json:"dep"`
	To     string `json:"to"`
	Branch string `json:"branch"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// Proposal is the PR for a bump: one branch per tool, so a newer version replaces an unmerged one.
func Proposal(b Bump) PR {
	short := b.Dep[strings.LastIndexAny(b.Dep, "/")+1:]
	var args []string
	for a := range b.Set {
		args = append(args, a)
	}
	sort.Strings(args)
	var s strings.Builder
	fmt.Fprintf(&s, "Bumps the image's pinned **%s** from `%s` to `%s` ([release](%s)), with its checksums (#69).\n\n", b.Dep, b.From, b.To, b.Release)
	s.WriteString("| ARG | New value |\n|---|---|\n")
	for _, a := range args {
		fmt.Fprintf(&s, "| `%s` | `%s` |\n", a, b.Set[a])
	}
	s.WriteString("\n**How the checksums were verified:**\n\n")
	for _, c := range b.Checks {
		fmt.Fprintf(&s, "- %s;\n", c)
	}
	s.WriteString("- the `image` workflow builds the image from this branch: each download must match its checksum, and the installed version must be the pinned one.\n\n")
	s.WriteString("**Live orgs:** this changes the container image. A live org gets it only at its next `berth restart`, which is announced per org; merging this restarts nothing.\n\n")
	s.WriteString("Opened by the `pinbump` workflow (`tools/pinbump`). A newer release replaces this PR's branch.\n")
	return PR{Dep: b.Dep, To: b.To, Branch: "pinbump/" + short,
		Title: fmt.Sprintf("image: bump %s to %s", short, b.To), Body: s.String()}
}

// httpFetcher: GitHub's API with the workflow's token (for the rate limit), anything else as is.
// Go drops the Authorization header when a download redirects to another host.
type httpFetcher struct{ token string }

func (h httpFetcher) Get(ctx context.Context, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(u, githubAPI+"/") {
		req.Header.Set("Accept", "application/vnd.github+json")
		if h.token != "" {
			req.Header.Set("Authorization", "Bearer "+h.token)
		}
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G704 -- fixed upstream hosts, from the specs above
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return body, nil
}
