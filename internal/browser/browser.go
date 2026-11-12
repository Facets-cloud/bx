// Package browser knows where each supported Chromium browser keeps its
// profiles and DevTools port, and how to open a page in a given profile.
package browser

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Browser is one entry in the table of supported browsers. Adding a
// Chromium browser means adding an entry to table.
type Browser struct {
	Name    string // what --browser takes
	Label   string // how messages name it
	DataDir string // user data dir, relative to $HOME
	App     string // executable that accepts --profile-directory
	Agent   bool   // a separate instance bx launched (see agent.go)
	Inspect string // page where the user turns remote debugging on
}

// table lists the supported browsers. Only Brave has been tested end to
// end; the others follow the same Chromium layout.
var table = []Browser{
	{
		Name:    "brave",
		Label:   "Brave",
		DataDir: "Library/Application Support/BraveSoftware/Brave-Browser",
		App:     "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		Inspect: "brave://inspect/#remote-debugging",
	},
	{
		Name:    "chrome",
		Label:   "Chrome",
		DataDir: "Library/Application Support/Google/Chrome",
		App:     "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		Inspect: "chrome://inspect/#remote-debugging",
	},
	{
		Name:    "chromium",
		Label:   "Chromium",
		DataDir: "Library/Application Support/Chromium",
		App:     "/Applications/Chromium.app/Contents/MacOS/Chromium",
		Inspect: "chrome://inspect/#remote-debugging",
	},
	{
		Name:    "edge",
		Label:   "Edge",
		DataDir: "Library/Application Support/Microsoft Edge",
		App:     "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		Inspect: "edge://inspect/#remote-debugging",
	},
	{
		Name:    "arc",
		Label:   "Arc",
		DataDir: "Library/Application Support/Arc/User Data",
		App:     "/Applications/Arc.app/Contents/MacOS/Arc",
		Inspect: "chrome://inspect/#remote-debugging",
	},
	{
		Name:    "vivaldi",
		Label:   "Vivaldi",
		DataDir: "Library/Application Support/Vivaldi",
		App:     "/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
		Inspect: "vivaldi://inspect/#remote-debugging",
	},
}

// Default is the browser used when --browser is not given and no browser
// has remote debugging on.
const Default = "brave"

// Detect returns the first browser in the table that has remote debugging
// on (its DevToolsActivePort exists), or Default when none has.
func Detect() Browser {
	for _, b := range table {
		_, err := os.Stat(filepath.Join(b.Dir(), "DevToolsActivePort"))
		// A permission error still means the browser is there; WSURL
		// reports it properly later.
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return b
		}
	}
	b, _ := Lookup(Default)
	return b
}

// Lookup returns the table entry named name (exact match).
// A name ending in "-agent", such as "brave-agent", is that browser's
// separate instance started by `bx launch`.
func Lookup(name string) (Browser, error) {
	if base, ok := strings.CutSuffix(name, AgentSuffix); ok {
		b, err := Lookup(base)
		if err != nil {
			return Browser{}, err
		}
		return b.agent(), nil
	}
	for _, b := range table {
		if b.Name == name {
			return b, nil
		}
	}
	return Browser{}, exit.New(exit.Validation, "unknown browser %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// Names lists the supported browser names.
func Names() []string {
	var out []string
	for _, b := range table {
		out = append(out, b.Name)
	}
	return out
}

// Dir is the absolute user data dir.
func (b Browser) Dir() string {
	if filepath.IsAbs(b.DataDir) {
		return b.DataDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, b.DataDir)
}

// WSURL reads DevToolsActivePort and returns the browser's WebSocket URL.
func (b Browser) WSURL() (string, error) {
	data, err := os.ReadFile(filepath.Join(b.Dir(), "DevToolsActivePort"))
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "", exit.New(exit.Permission,
			"cannot read %s's DevToolsActivePort: give your terminal app Full Disk Access (System Settings > Privacy & Security)", b.Label)
	case errors.Is(err, fs.ErrNotExist):
		return "", exit.New(exit.Upstream,
			"%s is not running with remote debugging on: run bx setup, or turn it on at %s", b.Label, b.Inspect)
	case err != nil:
		return "", exit.New(exit.Upstream, "read DevToolsActivePort: %v", err)
	}
	return parsePort(data)
}

// parsePort turns DevToolsActivePort contents ("<port>\n<path>") into a URL.
func parsePort(data []byte) (string, error) {
	f := strings.Fields(string(data))
	if len(f) < 2 || !strings.HasPrefix(f[1], "/devtools/browser/") {
		return "", exit.New(exit.Upstream, "DevToolsActivePort has unexpected contents")
	}
	return "ws://127.0.0.1:" + f[0] + f[1], nil
}

// Profile is one browser profile: its folder name and display name.
type Profile struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
}

// Profiles lists the profiles in Local State, sorted by folder name.
// A fresh agent browser has not written its list yet; it always has the
// Default profile.
func (b Browser) Profiles() ([]Profile, error) {
	ps, err := b.profiles()
	if b.Agent && (err != nil || len(ps) == 0) {
		return []Profile{{Dir: "Default", Name: "Default"}}, nil
	}
	return ps, err
}

func (b Browser) profiles() ([]Profile, error) {
	data, err := os.ReadFile(filepath.Join(b.Dir(), "Local State"))
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, exit.New(exit.Permission, "cannot read %s's Local State: give your terminal app Full Disk Access", b.Label)
		}
		return nil, exit.New(exit.Upstream, "read %s profiles: %v", b.Label, err)
	}
	return parseProfiles(data)
}

func parseProfiles(data []byte) ([]Profile, error) {
	var ls struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, exit.New(exit.Upstream, "parse Local State: %v", err)
	}
	var out []Profile
	for dir, p := range ls.Profile.InfoCache {
		out = append(out, Profile{Dir: dir, Name: p.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// FindProfile returns the profile whose display name or folder name is
// exactly ref.
func FindProfile(ps []Profile, ref string) (Profile, error) {
	for _, p := range ps {
		if p.Name == ref || p.Dir == ref {
			return p, nil
		}
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return Profile{}, exit.New(exit.User, "no profile named %q (have: %s)", ref, strings.Join(names, ", "))
}

// CheckURL accepts only URLs that cannot be read as a browser flag.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || strings.HasPrefix(raw, "-") {
		return exit.New(exit.Validation, "invalid URL %q", raw)
	}
	switch u.Scheme {
	case "http", "https", "file", "about":
		return nil
	}
	return exit.New(exit.Validation, "URL %q must start with http://, https://, file:// or about:", raw)
}

// Launch asks the running browser to open url in a new tab of profileDir.
// The launcher process hands the request to the running browser and exits.
func (b Browser) Launch(profileDir, rawURL string) error {
	if err := CheckURL(rawURL); err != nil {
		return err
	}
	return b.launch(profileDir, rawURL)
}

// ProbeMarker starts the nonce in every probe URL, so bx can hide its own
// probe tabs. It needs no escaping in a URL.
const ProbeMarker = "bxprobe-"

// ProbeURL is a page that a running browser opens from the command line
// (it ignores about: URLs there) and that carries nonce in its URL, so the
// new tab can be told apart from any other.
func ProbeURL(nonce string) string {
	return "data:text/html,<title>bx</title><!--" + ProbeMarker + nonce + "-->"
}

// LaunchProbe opens ProbeURL(nonce) in profileDir.
func (b Browser) LaunchProbe(profileDir, nonce string) error {
	return b.launch(profileDir, ProbeURL(nonce))
}

func (b Browser) launch(profileDir, rawURL string) error {
	args := []string{"--profile-directory=" + profileDir, rawURL}
	if b.Agent {
		// Route to the agent instance, not the user's everyday browser.
		args = append([]string{"--user-data-dir=" + b.Dir()}, args...)
	}
	out, err := exec.Command(b.App, args...).CombinedOutput()
	if err != nil {
		return exit.New(exit.Upstream, "launch %s: %v: %s", b.Label, err, strings.TrimSpace(string(out)))
	}
	return nil
}
