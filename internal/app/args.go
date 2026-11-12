package app

import (
	"strings"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

// valueFlags take a value; boolFlags do not. Each maps an accepted
// spelling to its canonical name.
var valueFlags = map[string]string{
	"-o": "output", "--output": "output",
	"--browser": "browser", "--profile": "profile",
	"--selector": "selector", "--format": "format",
	"--out": "out", "--tab": "tab", "--timeout": "timeout",
	"--text": "text", "--submit": "submit", "--file": "file",
	"--since": "since", "--limit": "limit", "--request": "request",
	"--viewport": "viewport", "--network": "network", "--cpu": "cpu", "--geo": "geo", "--color": "color",
}

var boolFlags = map[string]string{
	"-q": "quiet", "--quiet": "quiet",
	"--yes": "yes", "--full": "full", "--double": "double", "--gone": "gone", "--check": "check", "--off": "off",
	"--errors": "errors", "--reset": "reset", "--reload": "reload",
	"-h": "help", "--help": "help",
}

// args is a parsed command line: positional words and flags, which may
// appear in any order. "--" ends flag parsing.
type args struct {
	pos   []string
	flags map[string]string
}

func parseArgs(in []string) (args, error) {
	a := args{flags: map[string]string{}}
	for i := 0; i < len(in); i++ {
		w := in[i]
		if w == "--" {
			a.pos = append(a.pos, in[i+1:]...)
			break
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			a.pos = append(a.pos, w)
			continue
		}
		name, val, hasVal := strings.Cut(w, "=")
		if canon, ok := boolFlags[name]; ok && !hasVal {
			a.flags[canon] = "true"
			continue
		}
		canon, ok := valueFlags[name]
		if !ok {
			return a, exit.New(exit.Validation, "unknown flag %s", name)
		}
		if !hasVal {
			if i+1 >= len(in) {
				return a, exit.New(exit.Validation, "flag %s needs a value", name)
			}
			i++
			val = in[i]
		}
		a.flags[canon] = val
	}
	return a, nil
}

func (a args) has(name string) bool { return a.flags[name] == "true" }

// timeout validates --timeout and returns it as a string for the daemon.
func (a args) timeout() (string, error) {
	t := a.flags["timeout"]
	if t == "" {
		return "30s", nil
	}
	if d, err := time.ParseDuration(t); err != nil || d <= 0 {
		return "", exit.New(exit.Validation, "--timeout must be a duration such as 10s or 2m")
	}
	return t, nil
}

// need checks the number of positional words after the command words.
func need(pos []string, n int, usage string) error {
	if len(pos) != n {
		return exit.New(exit.Validation, "usage: %s", usage)
	}
	return nil
}
