//go:build windows

package browser

import (
	"os"
	"path/filepath"
	"strings"
)

// table lists the supported browsers on Windows. The data dirs live under
// %LOCALAPPDATA% (absolute, so Dir() returns them as-is); App is resolved to
// the first install path that exists. Paths verified on Windows 11 build
// 26300 — Edge installs under the 32-bit Program Files even though it is a
// 64-bit app, so both roots are probed.
var table = winTable()

func winTable() []Browser {
	defs := []struct {
		name, label, data, inspect string
		exes                       []string
	}{
		{"edge", "Microsoft Edge", `Microsoft\Edge\User Data`, "edge://inspect/#remote-debugging",
			[]string{`%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe`, `%ProgramFiles%\Microsoft\Edge\Application\msedge.exe`}},
		{"brave", "Brave", `BraveSoftware\Brave-Browser\User Data`, "brave://inspect/#remote-debugging",
			[]string{`%ProgramFiles%\BraveSoftware\Brave-Browser\Application\brave.exe`, `%ProgramFiles(x86)%\BraveSoftware\Brave-Browser\Application\brave.exe`}},
		{"chrome", "Chrome", `Google\Chrome\User Data`, "chrome://inspect/#remote-debugging",
			[]string{`%ProgramFiles%\Google\Chrome\Application\chrome.exe`, `%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe`, `%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe`}},
		{"vivaldi", "Vivaldi", `Vivaldi\User Data`, "vivaldi://inspect/#remote-debugging",
			[]string{`%LOCALAPPDATA%\Vivaldi\Application\vivaldi.exe`, `%ProgramFiles%\Vivaldi\Application\vivaldi.exe`}},
		{"chromium", "Chromium", `Chromium\User Data`, "chrome://inspect/#remote-debugging",
			[]string{`%LOCALAPPDATA%\Chromium\Application\chrome.exe`}},
	}
	local := os.Getenv("LOCALAPPDATA")
	out := make([]Browser, 0, len(defs))
	for _, d := range defs {
		out = append(out, Browser{
			Name:    d.name,
			Label:   d.label,
			DataDir: filepath.Join(local, d.data),
			App:     firstExisting(d.exes),
			Inspect: d.inspect,
		})
	}
	return out
}

// firstExisting expands each candidate and returns the first that is present,
// else the first candidate (so messages still name a sensible path).
func firstExisting(cands []string) string {
	var first string
	for _, c := range cands {
		e := expandEnv(c)
		if first == "" {
			first = e
		}
		if _, err := os.Stat(e); err == nil {
			return e
		}
	}
	return first
}

// expandEnv replaces %VAR% with the environment value, for the install paths.
func expandEnv(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		j := strings.IndexByte(s, '%')
		if j < 0 {
			b.WriteByte('%')
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(os.Getenv(s[:j]))
		s = s[j+1:]
	}
}
