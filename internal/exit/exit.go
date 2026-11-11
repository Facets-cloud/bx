// Package exit defines bx's exit codes and an error type that carries one.
package exit

import (
	"errors"
	"fmt"
)

// Exit codes, as documented in `bx --help`.
const (
	OK         = 0
	User       = 1 // bad reference, missing tab, unknown profile
	Validation = 2 // bad flags or arguments
	Upstream   = 3 // the browser failed or is not reachable
	Permission = 4 // Full Disk Access missing or Allow refused
)

// Error is an error with an exit code.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// New returns an *Error with a formatted message.
func New(code int, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf returns the exit code carried by err, or Upstream for an error
// that carries none.
func CodeOf(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Upstream
}
