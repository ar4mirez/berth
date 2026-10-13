package mcpsrv

import (
	"context"
	"errors"
	"strings"

	"github.com/ar4mirez/berth/internal/app"
)

// The operations that take a secret (#167, phase 3). Until these, nothing in the API took one.
// Each is an admin operation, so no MCP server offers it and a token needs the admin scope. A
// secret is taken and stored where the command stores it; it is never given back, never in the
// audit log, and never in an operation's events: the tool runs with its output kept to itself
// (call.private), and answers with what it did, not with what the command printed.

type envSetArgs struct {
	Org       string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name      string `json:"name" jsonschema:"the variable's name: capitals, digits and underscores, as in OPENROUTER_API_KEY"`
	Value     string `json:"value" secret:"true" jsonschema:"its value: one line, with no single quote. It is stored for the org, and never shown again"`
	NoRestart bool   `json:"no_restart,omitempty" jsonschema:"leave a running org as it is: it has the variable after its next restart"`
	Confirm   string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop the work running in it"`
}

func (a envSetArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a envSetArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name, "no_restart": a.NoRestart}
}

type envAcceptArgs struct {
	Org       string   `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Names     []string `json:"names,omitempty" jsonschema:"which of the waiting secrets to take, as env_pending shows them (every one when empty)"`
	NoRestart bool     `json:"no_restart,omitempty" jsonschema:"leave a running org as it is: it has them after its next restart"`
	Confirm   string   `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop the work running in it"`
}

func (a envAcceptArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a envAcceptArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "names": a.Names, "no_restart": a.NoRestart}
}

type tokenSetArgs struct {
	Org       string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Token     string `json:"token" secret:"true" jsonschema:"the org's Claude token (sk-ant-…), as claude setup-token prints it. It is stored for the org, and never shown again"`
	NoRestart bool   `json:"no_restart,omitempty" jsonschema:"leave a running org as it is: it uses the token after its next restart"`
	Confirm   string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop the work running in it"`
}

func (a tokenSetArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a tokenSetArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "no_restart": a.NoRestart}
}

type passwordRotateArgs struct {
	Org     string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Confirm string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to end the sessions in its browser terminal"`
}

func (a passwordRotateArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a passwordRotateArgs) auditArgs() map[string]any      { return map[string]any{"org": a.Org} }

type apiTokenAddArgs struct {
	Name  string `json:"name" jsonschema:"what to call it: lowercase letters, digits and dashes, as in phone or ci"`
	Scope string `json:"scope,omitempty" jsonschema:"what it may do: read (the default), write, restart or admin"`
}

func (a apiTokenAddArgs) auditArgs() map[string]any {
	return map[string]any{"name": a.Name, "scope": a.Scope}
}

type apiTokenRemoveArgs struct {
	Name string `json:"name" jsonschema:"the token's name, as api_tokens_list shows it"`
}

func (a apiTokenRemoveArgs) auditArgs() map[string]any { return map[string]any{"name": a.Name} }

// Pending is the result of env_pending: the secrets dropped inside an org that berth hasn't taken.
type Pending struct {
	Org string `json:"org"`
	// Names are their names, never their values. One berth would refuse says why after its name.
	Names []string `json:"names"`
}

// APITokens is the result of api_tokens_list.
type APITokens struct {
	Tokens []APITokenInfo `json:"tokens"`
}

// APITokenInfo is one token: what it is called and may do, never the token or its hash.
type APITokenInfo struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Created string `json:"created"`
}

// APITokenMade is the result of api_token_add: the one time the token is given.
type APITokenMade struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	// Token is what a caller presents. berth keeps only its hash: it can't be shown again.
	Token string `json:"token"`
}

func init() {
	// The API's own tokens are the operator's to see, and a secret waiting to be taken is taken by a
	// person: neither is for an agent, though neither tool needs the admin scope to read.
	noMCP["api_tokens_list"], noMCP["env_pending"] = true, true
}

// private keeps what the operation prints to this call: not to the caller's events, where a
// command that prints a secret (a rotated password, a new token) would send it.
func (c *call) private() {
	c.app.Progress = nil
	c.app.Stdout, c.app.Stderr = &c.stdout, &c.stderr
}

// forget drops what the operation printed, so a failure's message can't carry any of it: the
// server puts a failed command's output before its error.
func (c *call) forget() {
	c.stdout.Reset()
	c.stderr.Reset()
}

// secretLine refuses a secret a command couldn't read whole, and gives it as the command reads it.
func secretLine(what, v string) (string, error) {
	switch {
	case v == "":
		return "", errors.New(what + " is required")
	case strings.ContainsAny(v, "\r\n"):
		return "", errors.New(what + " is one line")
	}
	return v + "\n", nil
}

func (s *server) secretTools() {
	// --- reading -----------------------------------------------------------------------------
	addQuery(s, "env_pending", "The secrets waiting inside a running org to be taken: what someone typed there with berth-secret-drop, by name. Values are never returned.",
		func(ctx context.Context, c *call, in orgArg) (Pending, error) {
			c.private()
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Pending{}, err
			}
			if err := b.Env(ctx, []string{o, "accept", "--list"}); err != nil {
				return Pending{}, err
			}
			out := Pending{Org: in.Org, Names: []string{}}
			for _, l := range strings.Split(strings.TrimSpace(c.stdout.String()), "\n") {
				// "Nothing is waiting in acme. …" is a sentence, not a name.
				if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "Nothing is waiting") {
					out.Names = append(out.Names, l)
				}
			}
			return out, nil
		})
	add(s, "api_tokens_list", "serve", "token ls", "The tokens callers of this API present over the network: their names, scopes and dates. Never the tokens: berth keeps only their hashes.",
		func(_ context.Context, c *call, _ noArgs) (APITokens, error) {
			ts, err := c.app.ListAPITokens()
			out := APITokens{Tokens: []APITokenInfo{}}
			for _, t := range ts {
				// The hash stays where it is: it is no secret, and nobody's business either.
				out.Tokens = append(out.Tokens, APITokenInfo{Name: t.Name, Scope: t.Scope, Created: t.Created})
			}
			return out, err
		})

	// --- taking a secret (the admin scope; a restart is confirmed) --------------------------------
	add(s, "env_set", "env", "set", "Set one of an org's custom variables, a secret included: its value is stored for the org and never shown again. A running org is restarted to apply it, unless no_restart.",
		func(ctx context.Context, c *call, in envSetArgs) (Changed, error) {
			c.private()
			value, err := secretLine("value", in.Value)
			if err != nil {
				return Changed{}, err
			}
			if in.Name == "" {
				return Changed{}, errors.New("name is required")
			}
			if err := flagLike("name", in.Name); err != nil {
				return Changed{}, err
			}
			c.app.Stdin = strings.NewReader(value)
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			args := []string{o, "set", in.Name}
			if in.NoRestart {
				args = append(args, "--no-restart")
			}
			if err := b.Env(ctx, args); err != nil {
				c.forget()
				return Changed{}, scrub(err, in.Value)
			}
			return Changed{Org: in.Org, Output: "Set " + in.Name + " for " + in.Org + "."}, nil
		})
	add(s, "env_accept", "env", "accept", "Take the secrets waiting inside a running org (typed there with berth-secret-drop) as its variables. Their values never leave the machine the org is on. A running org is restarted to apply them, unless no_restart.",
		func(ctx context.Context, c *call, in envAcceptArgs) (Changed, error) {
			c.private()
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			args := []string{o, "accept"}
			for _, n := range in.Names {
				if err := flagLike("a name", n); err != nil {
					return Changed{}, err
				}
				args = append(args, n)
			}
			if in.NoRestart {
				args = append(args, "--no-restart")
			}
			err = b.Env(ctx, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "token_set", "token", "", "Give an org its Claude token, as claude setup-token prints it: it is stored for the org and never shown again. A running org is restarted to apply it, unless no_restart.",
		func(ctx context.Context, c *call, in tokenSetArgs) (Changed, error) {
			c.private()
			token, err := secretLine("token", in.Token)
			if err != nil {
				return Changed{}, err
			}
			c.app.Stdin = strings.NewReader(token)
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			args := []string{o, "--paste"}
			if in.NoRestart {
				args = append(args, "--no-restart")
			}
			if err := b.Token(ctx, args); err != nil {
				c.forget()
				return Changed{}, scrub(err, in.Token)
			}
			return Changed{Org: in.Org, Output: "Token saved for " + in.Org + "."}, nil
		})
	add(s, "password_rotate", "password", "rotate", "Give an org's browser terminal a new password, which ends the sessions open in it. The password is not returned: berth org password show prints it, on the machine the org is on.",
		func(ctx context.Context, c *call, in passwordRotateArgs) (Changed, error) {
			c.private()
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if err := b.Password(ctx, o, "rotate"); err != nil {
				c.forget()
				return Changed{}, errors.New("the password wasn't rotated: " + firstLine(err.Error()))
			}
			// What the command printed holds the password: say what was done instead.
			return Changed{Org: in.Org, Output: "Rotated; the browser terminal restarts with it in ~3s. Read it on that machine with: berth org password show " + o}, nil
		})
	add(s, "backup_keygen", "keygen", "", "Make this machine's backup key, so backups are encrypted without a passphrase. The key stays in a file on this machine: only its public half is returned.",
		func(ctx context.Context, c *call, _ nothing) (Done, error) {
			c.private()
			err := c.app.Keygen(ctx)
			return Done{Output: c.output()}, err
		})
	add(s, "api_token_add", "serve", "token add", "Make a token for a caller of this API over the network. It is returned once, here: berth keeps only its hash.",
		func(_ context.Context, c *call, in apiTokenAddArgs) (APITokenMade, error) {
			c.private()
			scope := in.Scope
			if scope == "" {
				scope = app.ScopeRead
			}
			for what, v := range map[string]string{"name": in.Name, "scope": scope} {
				if err := flagLike(what, v); err != nil {
					return APITokenMade{}, err
				}
			}
			if err := c.app.APIToken([]string{"add", in.Name, "--scope", scope}); err != nil {
				return APITokenMade{}, err
			}
			// The command prints the token alone on stdout, and the rest on stderr.
			return APITokenMade{Name: in.Name, Scope: scope, Token: strings.TrimSpace(c.stdout.String())}, nil
		})
	add(s, "api_token_remove", "serve", "token rm", "Remove one of this API's tokens. A running server refuses it from then on.",
		func(_ context.Context, c *call, in apiTokenRemoveArgs) (Done, error) {
			if err := flagLike("name", in.Name); err != nil {
				return Done{}, err
			}
			err := c.app.APIToken([]string{"rm", in.Name})
			return Done{Output: c.output()}, err
		})
}

// scrub is an error that doesn't say the secret a command was given, should the command's own
// message ever quote it.
func scrub(err error, secret string) error {
	if secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "…"))
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// PasswordShown is the result of password_show: the browser terminal's sign-in.
type PasswordShown struct {
	Org string `json:"org"`
	// User and Password are what the org's browser terminal asks for.
	User     string `json:"user"`
	Password string `json:"password"`
}

// passwordTools is the one tool that gives a secret back (#167): the browser terminal's password,
// for a caller that may do everything, when it asks. A dashboard links to that terminal, and the
// link is no use without what to type into it. It is an admin operation, so no MCP server offers
// it; its output is kept to the call; and the audit log has that it was shown, and to which
// interface.
func (s *server) passwordTools() {
	add(s, "password_show", "password", "show", "The user and password an org's browser terminal asks for. It is the one secret this API gives back, to a caller with the admin scope, and each time it is shown is in the audit log.",
		func(ctx context.Context, c *call, in orgWrite) (PasswordShown, error) {
			c.private()
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return PasswordShown{}, err
			}
			if err := b.Password(ctx, o, "show"); err != nil {
				c.forget()
				return PasswordShown{}, err
			}
			// The command prints "user: node" and "pass: …", each on a line.
			out := PasswordShown{Org: in.Org}
			for _, l := range strings.Split(c.stdout.String(), "\n") {
				switch k, v, _ := strings.Cut(l, ": "); k {
				case "user":
					out.User = v
				case "pass":
					out.Password = v
				}
			}
			c.forget()
			if out.Password == "" {
				return PasswordShown{}, errors.New("the password couldn't be read")
			}
			return out, nil
		})
}
