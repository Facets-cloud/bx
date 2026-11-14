package daemon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
)

// Every request an agent sends is written to a local audit log, one JSON
// line per request, in one file per session:
//
//	<Dir>/sessions/<session>.jsonl
//
// Inside Claude Code or Codex the session is the agent app's own session
// (see harnessSession), so one conversation is one file.
//
// It records where bx acted (browser, profile, tab, URL) and what it did,
// but not what it typed: fill values, typed text and eval scripts appear
// only as their length, unless the user's config says log.values full;
// log.values off writes nothing. The setting lives only in the config
// file, so an agent can't switch logging off for its own commands.
// retention.go keeps the folder within its limits, and logread.go reads it.

// Entry is one line of a session's audit log.
type Entry struct {
	T       time.Time `json:"t"`
	Session string    `json:"session"`
	// From the file's header line, not repeated on each line.
	Harness string `json:"-"` // the agent app, such as claude-code
	HSess   string `json:"-"` // the app's own session id, when it tells us
	Project string `json:"-"` // the folder the agent works in
	Title   string `json:"title,omitempty"`
	Browser string `json:"browser"`
	Profile string `json:"profile,omitempty"`
	Tab     string `json:"tab,omitempty"`
	URL     string `json:"url,omitempty"` // the page it acted on
	To      string `json:"to,omitempty"`  // the page it led to, when that changed
	Op      string `json:"op"`
	Target  string `json:"target,omitempty"`
	Error   string `json:"error,omitempty"`
	MS      int64  `json:"ms"`
}

// unlogged ops say nothing about what an agent did to a page.
var unlogged = map[string]bool{"status": true, "stop": true, "tabs": true, "show": true}

// sessionsDir is where the audit logs live.
func sessionsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// logPath is the file a session's entries go to.
func logPath(session string) (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, unsafeName.ReplaceAllString(session, "_")+".jsonl"), nil
}

// audit appends one request to its session's log. It runs after the
// response has gone back, so it never slows the agent down.
func (s *server) audit(req Request, title string, res any, err error, took time.Duration) {
	a := req.Args
	values := LoadConfig().Log.values()
	if unlogged[req.Op] || values == "off" || a["session"] == "" {
		return
	}
	e := Entry{T: time.Now().Add(-took), Session: a["session"], Harness: a["harness"], HSess: a["harness_session"], Project: a["project"], Title: title, Browser: s.b.Name,
		Tab: a["tab"], Op: req.Op, Target: describe(req.Op, a, values == "full", req.names), MS: took.Milliseconds()}
	if err != nil {
		e.Error = errorResponse(err).Error
	}
	if tab, ok := res.(Tab); ok { // open
		e.Tab = string(tab.ID)
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if e.Tab != "" {
		s.locate(&e, req.Op)
	}
	if values != "full" {
		e.URL, e.To = cleanURL(e.URL), cleanURL(e.To)
	}
	writeEntry(e)
}

// locate fills in the entry's profile and pages: the URL the tab had
// before the request (as of the last logged one), and where it is now.
func (s *server) locate(e *Entry, op string) {
	info, err := s.findTab(e.Tab)
	if err != nil {
		e.URL = s.lastURL[e.Tab]
		return
	}
	t := s.tabInfo(info, s.profileNames())
	e.Profile = t.Profile
	before := s.lastURL[e.Tab]
	if before == "" || op == "open" {
		e.URL = t.URL
	} else {
		e.URL = before
		if t.URL != before {
			e.To = t.URL
		}
	}
	s.lastURL[e.Tab] = t.URL
}

func writeEntry(e Entry) {
	p, err := logPath(e.Session)
	if err != nil || os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	// A new file starts with a header line naming the session, its agent
	// app and project.
	var out []byte
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		h, _ := json.Marshal(Header{V: logVersion, Type: "session", Session: e.Session, Harness: e.Harness,
			HSess: e.HSess, Project: e.Project, Started: e.T})
		out = append(h, '\n')
	} else if f, err = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(e)
	_, _ = f.Write(append(append(out, line...), '\n'))
}

// refNames is the tab's refs as role and name, as of its latest snapshot.
func (s *server) refNames(tab string) map[string]string {
	s.mu.Lock()
	sess := s.sessions[target.ID(tab)]
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	m, _ := sess.named.Load().(map[string]string)
	return m
}

// named turns a ref such as e12 into the element it stood for, such as
// button "Next", so the log reads the same tomorrow, when refs mean
// nothing; anything else (a role and name, a CSS selector) stays as given.
func named(el string, names map[string]string) string {
	if n, ok := names[el]; ok {
		return n
	}
	return el
}

// describe says what a request acted on, leaving out what was typed
// unless full is set. Elements are logged by role and name (see named).
func describe(op string, a map[string]string, full bool, names map[string]string) string {
	secret := func(label, v string) string {
		if full {
			return fmt.Sprintf("%s %q", label, v)
		}
		return fmt.Sprintf("%s (%d chars)", label, len(v))
	}
	switch op {
	case "open", "goto":
		if full {
			return a["url"]
		}
		return cleanURL(a["url"])
	case "click", "hover", "read", "screenshot":
		return named(a["selector"], names)
	case "wait":
		if a["text"] != "" {
			return fmt.Sprintf("text %q", a["text"])
		}
		return named(a["selector"], names)
	case "type":
		return strings.TrimSpace(named(a["selector"], names) + " " + secret("text", a["text"]))
	case "fill":
		return describeFill(a["pairs"], full, names)
	case "batch":
		return describeBatch(a["steps"], full, names)
	case "eval":
		return secret("script", a["script"])
	case "key":
		return a["keys"]
	case "drag":
		return named(a["from"], names) + " -> " + named(a["to"], names)
	case "upload":
		var files []string
		_ = json.Unmarshal([]byte(a["files"]), &files)
		return fmt.Sprintf("%s, %d files", named(a["selector"], names), len(files))
	case "dialog":
		return a["action"]
	case "start":
		return a["title"]
	case "done":
		return a["summary"]
	case "cdp":
		return a["method"]
	case "network":
		return a["request"]
	case "record-start":
		return a["dir"]
	}
	return ""
}

func describeFill(pairs string, full bool, names map[string]string) string {
	var ps [][2]string
	_ = json.Unmarshal([]byte(pairs), &ps)
	var out []string
	for _, p := range ps {
		el := named(p[0], names)
		if full {
			out = append(out, fmt.Sprintf("%s = %q", el, p[1]))
		} else {
			out = append(out, el)
		}
	}
	return strings.Join(out, ", ")
}

func describeBatch(steps string, full bool, names map[string]string) string {
	var ss []Step
	_ = json.Unmarshal([]byte(steps), &ss)
	out := []string{fmt.Sprintf("%d steps:", len(ss))}
	for _, st := range ss {
		d := st.Action
		switch st.Action {
		case "goto":
			if full {
				d += " " + st.URL
			} else {
				d += " " + cleanURL(st.URL)
			}
		case "key":
			d += " " + st.Key
		default:
			if st.Element != "" {
				d += " " + named(st.Element, names)
			}
			if full && (st.Value != "" || st.Text != "") {
				d += fmt.Sprintf(" = %q", st.Value+st.Text)
			}
		}
		out = append(out, d+";")
	}
	return strings.TrimSuffix(strings.Join(out, " "), ";")
}

// host is a URL's host, without "www.".
func host(u string) string {
	_, rest, ok := strings.Cut(u, "://")
	if !ok {
		return ""
	}
	h, _, _ := strings.Cut(rest, "/")
	return strings.TrimPrefix(h, "www.")
}

// cleanURL drops what a URL can carry besides the page: the query string
// (sign-in codes, reset tokens, session ids), any user:password, and a
// fragment holding key=value pairs (#access_token=...). A fragment without
// one is the page in an app that routes by it (#/settings, #inbox), so it
// stays. Logs may be kept for years, so they keep only the page.
func cleanURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		raw, _, _ = strings.Cut(raw, "?")
		raw, _, _ = strings.Cut(raw, "#")
		return raw
	}
	u.User, u.RawQuery, u.ForceQuery = nil, "", false
	if strings.Contains(u.Fragment, "=") {
		u.Fragment, u.RawFragment = "", ""
	}
	return u.String()
}

// ShortURL is a URL without its scheme, query or www., cut to 60 characters.
func ShortURL(u string) string {
	_, rest, ok := strings.Cut(u, "://")
	if !ok {
		return u
	}
	rest, _, _ = strings.Cut(rest, "?")
	if len(rest) > 60 {
		rest = rest[:60] + "…"
	}
	return strings.TrimPrefix(rest, "www.")
}
