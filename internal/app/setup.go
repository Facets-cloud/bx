package app

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

//go:embed skill/SKILL.md
var skillMD []byte

// skillTargets lists where the skill goes: one path per agent harness
// whose config home exists.
func skillTargets() []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, h := range []string{".claude", ".codex"} {
		if fi, err := os.Stat(filepath.Join(home, h)); err == nil && fi.IsDir() {
			out = append(out, filepath.Join(home, h, "skills", "bx", "SKILL.md"))
		}
	}
	return out
}

func cmdSkillInstall(e env) error {
	targets := skillTargets()
	if len(targets) == 0 {
		return exit.New(exit.User, "found neither ~/.claude nor ~/.codex; install Claude Code or Codex first")
	}
	for _, t := range targets {
		if err := writeSkill(t); err != nil {
			return exit.New(exit.User, "install the skill at %s: %v", t, err)
		}
	}
	switch {
	case e.o.quiet:
	case e.o.json:
		e.o.printJSON(map[string]any{"installed": targets})
	default:
		for _, t := range targets {
			fmt.Fprintln(e.o.w, "installed", t)
		}
	}
	return nil
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	code   int
}

func cmdDoctor(e env) error {
	var cs []check
	add := func(name string, err error, okDetail string) {
		c := check{Name: name, OK: err == nil, Detail: okDetail}
		if err != nil {
			c.Detail, c.code = err.Error(), exit.CodeOf(err)
		}
		cs = append(cs, c)
	}
	_, err := os.Stat(e.b.App)
	add("browser installed", wrapNotFound(err, e.b.Label), e.b.App)
	ps, err := e.b.Profiles()
	add("profiles readable", err, fmt.Sprintf("%d profiles", len(ps)))
	_, err = e.b.WSURL()
	add("remote debugging on", err, "DevToolsActivePort found")
	add("skill installed", skillInstalled(), "up to date")
	running := "no; the next command starts it"
	if daemon.Running(e.b.Name) {
		running = "yes"
	}
	cs = append(cs, check{Name: "background connection", OK: true, Detail: running})
	cs = append(cs, versionCheck())
	return reportChecks(e.o, cs)
}

// versionCheck reports whether a newer release exists. An update is
// advice, not a failure, so the check always passes.
func versionCheck() check {
	c := check{Name: "bx version", OK: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	latest, err := latestRelease(ctx)
	switch {
	case err != nil:
		c.Detail = Version + " (could not check for updates: " + err.Error() + ")"
	case newer(latest, Version):
		c.Detail = fmt.Sprintf("%s; %s is available: run bx update", Version, latest)
	default:
		c.Detail = Version + " (latest is " + latest + ")"
	}
	return c
}

func wrapNotFound(err error, label string) error {
	if err != nil {
		return exit.New(exit.Upstream, "%s is not installed in /Applications", label)
	}
	return nil
}

func skillInstalled() error {
	for _, t := range skillTargets() {
		b, err := os.ReadFile(t)
		if err != nil || string(b) != string(skillMD) {
			return exit.New(exit.User, "%s is missing or out of date: run bx skill install", t)
		}
	}
	return nil
}

// reportChecks prints the checks and fails with the first failure's code.
func reportChecks(o out, cs []check) error {
	if o.json {
		o.printJSON(cs)
	} else {
		var rows [][]string
		for _, c := range cs {
			mark := "ok"
			if !c.OK {
				mark = "FAIL"
			}
			rows = append(rows, []string{mark, c.Name, c.Detail})
		}
		o.table([]string{"", "CHECK", "DETAIL"}, rows)
	}
	for _, c := range cs {
		if !c.OK {
			return &exit.Error{Code: c.code, Msg: "doctor found a problem: " + c.Name}
		}
	}
	return nil
}

// cmdUpdate installs the latest release when it is newer than this binary.
// With --check it only reports.
func cmdUpdate(e env) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	latest, err := latestRelease(ctx)
	if err != nil {
		return exit.New(exit.Upstream, "%v", err)
	}
	available := newer(latest, Version) || Version == "dev"
	if e.a.has("check") {
		if e.o.json {
			e.o.printJSON(map[string]any{"current": Version, "latest": latest, "update_available": available})
		} else {
			fmt.Fprintf(e.o.w, "current %s, latest %s\n", Version, latest)
		}
		return nil
	}
	if !available {
		e.o.done("bx " + Version + " is up to date")
		return nil
	}
	return install(e, latest)
}

// install replaces this binary with release tag (verified against the
// release's checksums), reinstalls the skill with the new binary, and stops
// the daemon so the next command runs the new code.
func install(e env, tag string) error {
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bin, err := fetchBinary(ctx, tag, fmt.Sprintf("bx_darwin_%s.tar.gz", runtime.GOARCH))
	if err != nil {
		return exit.New(exit.Upstream, "%v", err)
	}
	// Write next to the old binary, then rename: the swap is atomic.
	tmp, err := os.CreateTemp(filepath.Dir(self), ".bx-update-")
	if err != nil {
		return exit.New(exit.User, "cannot write next to %s: %v", self, err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(bin)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), self)
	}
	if err != nil {
		return exit.New(exit.User, "replace %s: %v", self, err)
	}
	if out, err := exec.Command(self, "skill", "update", "-q").CombinedOutput(); err != nil {
		return exit.New(exit.Upstream, "reinstall skill: %v: %s", err, out)
	}
	_ = daemon.Call(e.b.Name, daemon.Request{Op: "stop"}, nil, false)
	e.o.done("updated to " + tag)
	return nil
}
