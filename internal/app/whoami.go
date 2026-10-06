package app

import (
	"context"
	"fmt"

	"github.com/ar4mirez/berth/internal/ops"
)

// Whoami is `ccenv whoami [org...]`: which Claude account (and GitHub account) each org uses.
func (a *App) Whoami(ctx context.Context, orgs []string) error {
	// An unknown org ends it after the orgs before it are printed, as in ccenv's loop.
	who, err := ops.GetWhoami(ctx, a, orgs)
	if a.Output == OutputJSON {
		if err != nil {
			return err
		}
		return a.writeJSON(who)
	}
	fmt.Fprintf(a.Stdout, "%-12s %-7s %-14s %s\n", "ORG", "TOKEN", "GITHUB (gh)", "REMOTE-CONTROL LOGIN (account)")
	for _, r := range who.Orgs {
		token := "MISSING"
		if r.Token {
			token = "set"
		}
		fmt.Fprintf(a.Stdout, "%-12s %-7s %-14s %s\n", r.Org, token, r.GitHubText, r.ClaudeText)
	}
	return err
}
