package ops

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ar4mirez/berth/internal/config"
)

// Kind says what sort of failure an Error is, for a caller that acts on it rather than shows it.
type Kind string

const (
	// KindUsage: the command line is wrong.
	KindUsage Kind = "usage"
	// KindNotFound: the org (or host, repo, file) named doesn't exist.
	KindNotFound Kind = "not-found"
	// KindNotRunning: the org's container isn't running, and the operation needs it.
	KindNotRunning Kind = "not-running"
	// KindRefused: berth won't do it: --read-only, an org another tool manages, a failed check.
	KindRefused Kind = "refused"
	// KindState: the org's own state is incomplete or inconsistent.
	KindState Kind = "state"
	// KindCommand: a command the operation ran (docker, systemctl, …) failed.
	KindCommand Kind = "command"
	// KindFailed: anything else.
	KindFailed Kind = "failed"
)

// Error is a failure with what an interface needs to show it or act on it (#54): an exit code, a
// message, and a hint saying what to do about it.
type Error struct {
	Kind Kind
	// Code is the process exit code.
	Code int
	// Msg is what went wrong. Hint is what to do about it ("" for none).
	Msg  string
	Hint string
	// Quiet is true where ccenv ends with Code and prints nothing (set -e stopped it): the text
	// interface stays silent too, and the others still have Msg.
	Quiet bool
	// Err is the cause, if there is one.
	Err error
}

// Error is the message as berth prints it: "msg (hint)".
func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Msg
	}
	return e.Msg + " (" + e.Hint + ")"
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode is the exit code. Silent reports whether the text interface prints nothing.
func (e *Error) ExitCode() int { return e.Code }
func (e *Error) Silent() bool  { return e.Quiet }

// fail is an Error with exit code 1.
func fail(kind Kind, hint, format string, args ...any) *Error {
	return &Error{Kind: kind, Code: 1, Msg: fmt.Sprintf(format, args...), Hint: hint}
}

// quiet is an Error where ccenv exits with code and no message.
func quiet(kind Kind, code int, hint, format string, args ...any) *Error {
	return &Error{Kind: kind, Code: code, Msg: fmt.Sprintf(format, args...), Hint: hint, Quiet: true}
}

// AsError is err as an Error, whatever returned it: an Error as it is; an exit code with no message
// (Exit, a passthrough command's own exit) as a quiet one; --read-only as a refusal; ccenv's
// "usage: …" messages as usage errors; anything else as a plain failure with exit code 1.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var x *Exit
	if errors.As(err, &x) {
		return &Error{Kind: KindFailed, Code: x.Code, Msg: fmt.Sprintf("the command ended with exit status %d and no message", x.Code), Quiet: true, Err: err}
	}
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return &Error{Kind: KindCommand, Code: ec.ExitCode(), Msg: err.Error(), Quiet: true, Err: err}
	}
	out := &Error{Kind: KindFailed, Code: 1, Msg: err.Error(), Err: err}
	switch {
	case errors.Is(err, config.ErrReadOnly):
		out.Kind = KindRefused
	case strings.HasPrefix(out.Msg, "usage: "):
		out.Kind = KindUsage
	}
	return out
}

// ErrorDoc is a failure under `--output json`: one document on stderr (docs/json.md).
type ErrorDoc struct {
	Schema string `json:"schema"` // "berth.error/v1"
	Kind   Kind   `json:"kind"`
	Code   int    `json:"code"`
	// Message is what went wrong, and Hint what to do about it ("" for none).
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// Doc is the error as a document.
func (e *Error) Doc() ErrorDoc {
	// The commands a message names, as this command line spells them (spell.go).
	return ErrorDoc{Schema: "berth.error/v1", Kind: e.Kind, Code: e.Code, Message: Respell(e.Msg), Hint: Respell(e.Hint)}
}
