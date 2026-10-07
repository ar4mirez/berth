package app

import (
	"context"
	"encoding/json"
	"path"
	"time"
)

// AuditEntry is one line of the audit log: a change asked for through an interface other than the
// command line (the MCP server, #61). Arguments are names and settings, never secret values.
type AuditEntry struct {
	Time string `json:"time"` // RFC 3339, UTC
	// Via is the interface ("mcp"), Tool what was called and Args its arguments.
	Via  string         `json:"via"`
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	// Outcome is "ok", "failed" or "refused" (the server wasn't started with the flag it needs, or
	// the confirmation was missing), and Error why, for the last two.
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

// AuditFile is the audit log: one JSON object per line, in the state root.
func (a *App) AuditFile() string { return path.Join(a.State.Home.Path, "audit.log") }

// Audit appends e to the audit log, on the operator's machine. Under --read-only nothing is
// written (and nothing was changed).
func (a *App) Audit(ctx context.Context, e AuditEntry) error {
	if a.State.ReadOnly {
		return nil
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := a.Operator.FS.MkdirAll(a.State.Home.Path, 0o755); err != nil {
		return err
	}
	b, _ := a.Operator.FS.ReadFile(a.AuditFile())
	return a.Operator.FS.WriteFile(a.AuditFile(), append(b, append(line, '\n')...), 0o600)
}
