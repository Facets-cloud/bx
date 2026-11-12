package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Facets-cloud/bx/internal/exit"
)

// out holds where and how results are printed.
type out struct {
	json  bool
	quiet bool
	w     io.Writer
	errw  io.Writer
}

// newOut picks table on a terminal and JSON otherwise, unless -o says.
func newOut(a args) (out, error) {
	o := out{quiet: a.has("quiet"), w: os.Stdout, errw: os.Stderr}
	switch a.flags["output"] {
	case "":
		o.json = !isTerminal(os.Stdout)
	case "json":
		o.json = true
	case "table":
	default:
		return o, exit.New(exit.Validation, "-o must be table or json")
	}
	return o, nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (o out) printJSON(v any) {
	enc := json.NewEncoder(o.w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// table prints rows under a header, tab-aligned.
func (o out) table(header []string, rows [][]string) {
	tw := tabwriter.NewWriter(o.w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// done reports a successful action that returns no data.
func (o out) done(msg string) {
	switch {
	case o.quiet:
	case o.json:
		o.printJSON(map[string]any{"ok": true, "message": msg})
	default:
		fmt.Fprintln(o.w, msg)
	}
}

// fail prints err and returns its exit code.
func (o out) fail(err error) int {
	code := exit.CodeOf(err)
	if o.json {
		enc := json.NewEncoder(o.errw)
		_ = enc.Encode(map[string]any{"error": err.Error(), "code": code})
	} else {
		fmt.Fprintln(o.errw, "bx:", err)
	}
	return code
}

// clip shortens s to n runes for table cells.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
