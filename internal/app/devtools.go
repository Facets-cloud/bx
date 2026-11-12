package app

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// logArgs builds the daemon arguments shared by console and network.
func logArgs(e env) (map[string]string, error) {
	args := map[string]string{"tab": e.pos[0], "since": e.a.flags["since"], "limit": e.a.flags["limit"]}
	for _, f := range []string{"since", "limit"} {
		if v := args[f]; v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				return nil, exit.New(exit.Validation, "--%s must be a whole number", f)
			}
		}
	}
	if e.a.has("errors") {
		args["errors"] = "true"
	}
	return args, nil
}

func cmdConsole(e env) error {
	if err := need(e.pos, 1, "bx console <tab> [--errors] [--since <id>] [--limit <n>]"); err != nil {
		return err
	}
	args, err := logArgs(e)
	if err != nil {
		return err
	}
	var msgs []daemon.ConsoleMessage
	if err := e.call("console", args, &msgs); err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(msgs)
		return nil
	}
	rows := make([][]string, 0, len(msgs))
	for _, m := range msgs {
		rows = append(rows, []string{strconv.Itoa(m.ID), m.Level, m.Source, clip(firstLine(m.Text), 90), clip(m.Where, 60)})
	}
	e.o.table([]string{"ID", "LEVEL", "SOURCE", "TEXT", "WHERE"}, rows)
	return nil
}

func cmdNetwork(e env) error {
	usage := "bx network <tab> [--errors] [--since <id>] [--limit <n>] | bx network <tab> --request <id>"
	if err := need(e.pos, 1, usage); err != nil {
		return err
	}
	if req := e.a.flags["request"]; req != "" {
		return networkRequest(e, req)
	}
	args, err := logArgs(e)
	if err != nil {
		return err
	}
	var reqs []daemon.NetRequest
	if err := e.call("network", args, &reqs); err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(reqs)
		return nil
	}
	rows := make([][]string, 0, len(reqs))
	for _, r := range reqs {
		status := strconv.FormatInt(r.Status, 10)
		switch {
		case r.Failed != "":
			status = "failed: " + r.Failed
		case r.Status == 0 && !r.Done:
			status = "pending"
		}
		rows = append(rows, []string{r.ID, r.Method, clip(status, 30), r.Type, fmt.Sprintf("%dms", r.Ms), clip(r.URL, 90)})
	}
	e.o.table([]string{"ID", "METHOD", "STATUS", "TYPE", "TIME", "URL"}, rows)
	return nil
}

func networkRequest(e env, id string) error {
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var d daemon.NetRequestDetail
	if err := e.call("network", map[string]string{"tab": e.pos[0], "request": id, "timeout": t}, &d); err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(d)
		return nil
	}
	fmt.Fprintf(e.o.w, "%s %s\nstatus: %d %s %s\n", d.Method, d.URL, d.Status, d.StatusText, d.Failed)
	printHeaders(e, "request headers", d.RequestHeaders)
	if d.RequestBody != "" {
		fmt.Fprintf(e.o.w, "request body:\n%s\n", d.RequestBody)
	}
	printHeaders(e, "response headers", d.ResponseHeaders)
	if d.ResponseBody != "" {
		fmt.Fprintf(e.o.w, "response body:\n%s\n", d.ResponseBody)
	}
	if d.BodyNote != "" {
		fmt.Fprintln(e.o.w, d.BodyNote)
	}
	return nil
}

func printHeaders(e env, title string, h map[string]any) {
	if len(h) == 0 {
		return
	}
	fmt.Fprintln(e.o.w, title+":")
	for _, k := range slices.Sorted(maps.Keys(h)) {
		fmt.Fprintf(e.o.w, "  %s: %v\n", k, h[k])
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
