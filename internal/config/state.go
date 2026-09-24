package config

import (
	"errors"
	"fmt"
)

// ErrReadOnly is returned by every mutating operation while --read-only is set.
var ErrReadOnly = errors.New("read-only mode")

// State is what every command runs against: the resolved state root and the read-only switch.
type State struct {
	Home     Home
	ReadOnly bool
}

// Writable returns nil when mutations are allowed, else an ErrReadOnly naming op
// (e.g. "run up", "write org.env"). Anything that changes org state calls this first.
func (s State) Writable(op string) error {
	if s.ReadOnly {
		return fmt.Errorf("%w (--read-only): refusing to %s", ErrReadOnly, op)
	}
	return nil
}
