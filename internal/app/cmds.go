package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

func cmdListProfiles(e env) error {
	if err := need(e.pos, 0, "bx list profiles"); err != nil {
		return err
	}
	ps, err := e.b.Profiles()
	if err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(ps)
		return nil
	}
	var rows [][]string
	for _, p := range ps {
		rows = append(rows, []string{p.Name, p.Dir})
	}
	e.o.table([]string{"NAME", "FOLDER"}, rows)
	return nil
}

// printTab prints one tab: an object in JSON, a one-row table otherwise.
func printTab(o out, t daemon.Tab) {
	if o.json {
		o.printJSON(t)
		return
	}
	printTabs(o, []daemon.Tab{t})
}

func printTabs(o out, tabs []daemon.Tab) {
	if o.json {
		o.printJSON(tabs)
		return
	}
	var rows [][]string
	for _, t := range tabs {
		p := t.Profile
		if p == "" {
			p = "?"
		}
		rows = append(rows, []string{t.ID, p, clip(t.Title, 40), clip(t.URL, 60)})
	}
	o.table([]string{"ID", "PROFILE", "TITLE", "URL"}, rows)
}

func cmdListTabs(e env) error {
	if err := need(e.pos, 0, "bx list tabs [--profile <name>]"); err != nil {
		return err
	}
	var tabs []daemon.Tab
	if err := e.call("tabs", map[string]string{"profile": e.a.flags["profile"]}, &tabs); err != nil {
		return err
	}
	printTabs(e.o, tabs)
	return nil
}

func cmdShowTab(e env) error {
	if err := need(e.pos, 1, "bx show tab <id>"); err != nil {
		return err
	}
	var t daemon.Tab
	if err := e.call("show", map[string]string{"tab": e.pos[0]}, &t); err != nil {
		return err
	}
	printTab(e.o, t)
	return nil
}

func cmdOpen(e env) error {
	if err := need(e.pos, 1, "bx open <url> --profile <name>"); err != nil {
		return err
	}
	if err := browser.CheckURL(e.pos[0]); err != nil {
		return err
	}
	if e.a.flags["profile"] == "" {
		return exit.New(exit.Validation, "--profile is required (see bx list profiles)")
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var tab daemon.Tab
	args := map[string]string{"url": e.pos[0], "profile": e.a.flags["profile"], "timeout": t}
	if err := e.call("open", args, &tab); err != nil {
		return err
	}
	if !e.o.quiet {
		printTab(e.o, tab)
	}
	return nil
}

func cmdRead(e env) error {
	if err := need(e.pos, 1, "bx read <tab> [--selector <css>] [--format text|html]"); err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var c daemon.Content
	args := map[string]string{"tab": e.pos[0], "selector": e.a.flags["selector"], "format": e.a.flags["format"], "timeout": t}
	if err := e.call("read", args, &c); err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(c)
	} else {
		fmt.Fprintln(e.o.w, c.Content)
	}
	return nil
}

func cmdCloseTab(e env) error {
	if err := need(e.pos, 1, "bx close tab <id> --yes"); err != nil {
		return err
	}
	if !e.a.has("yes") {
		return exit.New(exit.Validation, "closing a tab needs --yes")
	}
	if err := e.call("close", map[string]string{"tab": e.pos[0]}, nil); err != nil {
		return err
	}
	e.o.done("closed " + e.pos[0])
	return nil
}

func cmdScreenshot(e env) error {
	if err := need(e.pos, 1, "bx screenshot <tab> [--selector <css>] [--full] [--out <file.png>|-]"); err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var img daemon.Image
	args := map[string]string{"tab": e.pos[0], "selector": e.a.flags["selector"], "timeout": t}
	if e.a.has("full") {
		args["full"] = "true"
	}
	if err := e.call("screenshot", args, &img); err != nil {
		return err
	}
	path := e.a.flags["out"]
	if path == "-" {
		_, err := os.Stdout.Write(img.PNG)
		return err
	}
	if path == "" {
		path = fmt.Sprintf("bx-%s.png", time.Now().Format("20060102-150405"))
	}
	if err := os.WriteFile(path, img.PNG, 0o644); err != nil {
		return exit.New(exit.User, "write %s: %v", path, err)
	}
	switch {
	case e.o.quiet:
	case e.o.json:
		e.o.printJSON(map[string]any{"path": path, "bytes": len(img.PNG)})
	default:
		fmt.Fprintln(e.o.w, path)
	}
	return nil
}

func cmdCDP(e env) error {
	if len(e.pos) < 1 || len(e.pos) > 2 {
		return exit.New(exit.Validation, "usage: bx cdp <Method> [<json-params>] [--tab <id>]")
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	args := map[string]string{"method": e.pos[0], "tab": e.a.flags["tab"], "timeout": t}
	if len(e.pos) == 2 {
		args["params"] = e.pos[1]
	}
	var res json.RawMessage
	if err := e.call("cdp", args, &res); err != nil {
		return err
	}
	if len(res) == 0 {
		res = json.RawMessage("{}")
	}
	e.o.printJSON(res)
	return nil
}

func cmdStatus(e env) error {
	var st daemon.Status
	err := daemon.Call(e.b.Name, daemon.Request{Op: "status"}, &st, false)
	if exit.CodeOf(err) == exit.User {
		// Not running is a normal answer to "status", not a failure.
		if e.o.json {
			e.o.printJSON(map[string]any{"browser": e.b.Name, "running": false})
		} else {
			fmt.Fprintf(e.o.w, "no bx connection to %s\n", e.b.Label)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(map[string]any{"browser": st.Browser, "running": true, "version": st.Version, "pid": st.PID, "since": st.Since, "known_profiles": st.KnownProfiles})
		return nil
	}
	fmt.Fprintf(e.o.w, "connected to %s (bx %s, pid %d) since %s; profiles mapped: %d\n",
		e.b.Label, st.Version, st.PID, st.Since.Format(time.Kitchen), len(st.KnownProfiles))
	return nil
}

func cmdStop(e env) error {
	err := daemon.Call(e.b.Name, daemon.Request{Op: "stop"}, nil, false)
	if err != nil && exit.CodeOf(err) != exit.User {
		return err
	}
	msg := "stopped"
	if err != nil {
		msg = "not running"
	}
	// An agent browser exists only for bx, so stopping also quits it.
	quit, qerr := e.b.QuitAgent()
	if qerr != nil {
		return qerr
	}
	if quit {
		msg += "; quit " + e.b.Label
	}
	e.o.done(msg)
	return nil
}

// cmdRecordStart starts recording the tabs bx works in, as video frames.
func cmdRecordStart(e env) error {
	dir := e.a.flags["out"]
	if dir == "" {
		dir = "bx-recording-" + time.Now().Format("20060102-150405")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return exit.New(exit.User, "%v", err)
	}
	if err := daemon.Call(e.b.Name, daemon.Request{Op: "record-start", Args: map[string]string{"dir": abs, "blur": e.a.flags["blur"]}}, nil, true); err != nil {
		return err
	}
	e.o.done("recording into " + abs + ": bx record stop makes the video")
	return nil
}

// cmdRecordStop stops the recording and, when ffmpeg is installed, makes
// an MP4 with the session's real timing.
func cmdRecordStop(e env) error {
	var r daemon.Recording
	if err := daemon.Call(e.b.Name, daemon.Request{Op: "record-stop"}, &r, false); err != nil {
		return err
	}
	out := e.a.flags["out"]
	if out == "" {
		out = r.Dir + ".mp4"
	}
	video := ""
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		cmd := exec.Command(ff, "-y", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", r.List,
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=30", "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)
		if b, err := cmd.CombinedOutput(); err != nil {
			return exit.New(exit.Upstream, "ffmpeg: %v: %s", err, strings.TrimSpace(string(b)))
		}
		video = out
	}
	if e.o.json {
		e.o.printJSON(map[string]any{"video": video, "frames_dir": r.Dir, "frames": r.Frames, "seconds": r.Seconds})
		return nil
	}
	if video == "" {
		fmt.Fprintf(e.o.w, "%d frames in %s (install ffmpeg to get a video; %s lists them with their timing)\n", r.Frames, r.Dir, r.List)
		return nil
	}
	fmt.Fprintf(e.o.w, "%s (%.0fs, %d frames)\n", video, r.Seconds, r.Frames)
	return nil
}

// cmdDone tells the browser's pages that the agent finished its browser
// work: the on-page pointer shows "Done" and the summary, then goes away.
// Without a running connection there is nothing to tidy, which is not an
// error.
func cmdDone(e env) error {
	summary := strings.Join(e.pos, " ")
	var res json.RawMessage
	err := daemon.Call(e.b.Name, daemon.Request{Op: "done", Args: map[string]string{"summary": summary}}, &res, false)
	if err != nil && exit.CodeOf(err) != exit.User {
		return err
	}
	if len(res) > 0 && string(res) != "null" {
		e.o.printJSON(res) // the session's title, time, actions and the tabs it opened
		return nil
	}
	e.o.done("done")
	return nil
}

// cmdStart names the CLI's agent session and sets its mode and typing,
// which the on-page pointer and pacing follow until bx done.
func cmdStart(e env) error {
	if len(e.pos) == 0 {
		return exit.New(exit.Validation, `usage: bx start "<title>" [--mode fast|watch] [--typing paste|keys]`)
	}
	args := map[string]string{"title": strings.Join(e.pos, " "), "task_mode": e.a.flags["mode"], "task_typing": e.a.flags["typing"]}
	if err := daemon.Call(e.b.Name, daemon.Request{Op: "start", Args: args}, nil, true); err != nil {
		return err
	}
	e.o.done("started: " + args["title"])
	return nil
}
