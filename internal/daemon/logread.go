package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// logVersion is the session file format. Each file starts with a Header
// line, then one Entry per line; both are JSON. Tools that ship logs
// elsewhere (Fluent Bit, Vector, the OpenTelemetry Collector) can tail the
// folder as it is.
const logVersion = 1

// Header is the first line of a session file.
type Header struct {
	V       int       `json:"v"`
	Type    string    `json:"type"` // "session"
	Session string    `json:"session"`
	Harness string    `json:"harness,omitempty"`
	HSess   string    `json:"harness_session,omitempty"`
	Project string    `json:"project,omitempty"`
	Started time.Time `json:"started"`
}

// SessionInfo is one session in the history list.
type SessionInfo struct {
	ID      string    `json:"id"`
	Harness string    `json:"harness,omitempty"`
	HSess   string    `json:"harness_session,omitempty"`
	Project string    `json:"project,omitempty"`
	Title   string    `json:"title,omitempty"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Actions int       `json:"actions"`
	Errors  int       `json:"errors"`
	Sites   []string  `json:"sites"`
	Log     string    `json:"log_file"`
}

// parseLog reads one session file: its header and entries, in order. A
// line that doesn't parse, such as one cut short by a crash, is skipped.
func parseLog(path string) (Header, []Entry, error) {
	data, err := readLog(path)
	if err != nil {
		return Header{}, nil, err
	}
	var h Header
	var es []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if bytes.Contains(line, []byte(`"type":"session"`)) && json.Unmarshal(line, &h) == nil && h.Type == "session" {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) == nil && e.Session != "" {
			e.Harness, e.HSess, e.Project = h.Harness, h.HSess, h.Project
			es = append(es, e)
		}
	}
	return h, es, nil
}

// summarize sums up entries of one session.
func summarize(id string, es []Entry, log string) SessionInfo {
	info := SessionInfo{ID: id, Sites: []string{}, Log: log}
	seen := map[string]bool{}
	for _, e := range es {
		if info.Start.IsZero() || e.T.Before(info.Start) {
			info.Start = e.T
		}
		if end := e.T.Add(time.Duration(e.MS) * time.Millisecond); end.After(info.End) {
			info.End = end
		}
		info.Harness, info.HSess, info.Project = e.Harness, e.HSess, e.Project
		if e.Title != "" {
			info.Title = e.Title
		}
		if e.Error != "" {
			info.Errors++
		}
		if e.Op != "done" && e.Op != "start" {
			info.Actions++
		}
		if h := host(e.URL); h != "" && !seen[h] {
			seen[h] = true
			info.Sites = append(info.Sites, h)
		}
	}
	return info
}

// merge adds a later file's summary of the same session to info.
func merge(info, more SessionInfo) SessionInfo {
	if more.Start.Before(info.Start) {
		info.Start = more.Start
	}
	if more.End.After(info.End) {
		info.End, info.Log = more.End, more.Log
		if more.Title != "" {
			info.Title = more.Title
		}
	}
	info.Actions += more.Actions
	info.Errors += more.Errors
	for _, s := range more.Sites {
		if !contains(info.Sites, s) {
			info.Sites = append(info.Sites, s)
		}
	}
	if info.HSess == "" {
		info.Harness, info.HSess, info.Project = more.Harness, more.HSess, more.Project
	}
	return info
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// The index keeps a summary of each session file, so listing sessions
// reads only the files that changed since the last listing, the way Loki
// indexes labels and time ranges instead of log lines.
type indexEntry struct {
	Size int64       `json:"size"`
	Mod  time.Time   `json:"mod"`
	Info SessionInfo `json:"info"`
}

func indexPath(dir string) string { return filepath.Join(dir, "index.json") }

func loadIndex(dir string) map[string]indexEntry {
	idx := map[string]indexEntry{}
	if b, err := os.ReadFile(indexPath(dir)); err == nil {
		_ = json.Unmarshal(b, &idx)
	}
	return idx
}

func saveIndex(dir string, idx map[string]indexEntry) {
	b, _ := json.Marshal(idx)
	tmp := indexPath(dir) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, indexPath(dir))
	}
}

// ListSessions sums up every logged session, archived and kept ones
// included, newest first.
func ListSessions() ([]SessionInfo, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	old, idx, changed := loadIndex(dir), map[string]indexEntry{}, false
	byID := map[string]SessionInfo{}
	for _, f := range logFiles(dir) {
		ie, ok := old[f.path]
		if !ok || ie.Size != f.size || !ie.Mod.Equal(f.mod) {
			h, es, err := parseLog(f.path)
			if err != nil || len(es) == 0 {
				continue
			}
			ie, changed = indexEntry{Size: f.size, Mod: f.mod, Info: summarize(es[0].Session, es, f.path)}, true
			if ie.Info.ID == "" {
				ie.Info.ID = h.Session
			}
		}
		idx[f.path] = ie
		if cur, ok := byID[ie.Info.ID]; ok {
			byID[ie.Info.ID] = merge(cur, ie.Info)
		} else {
			byID[ie.Info.ID] = ie.Info
		}
	}
	if changed || len(idx) != len(old) {
		saveIndex(dir, idx)
	}
	list := make([]SessionInfo, 0, len(byID))
	for _, s := range byID {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].End.After(list[j].End) })
	return list, nil
}

// LoadSession reads one session's entries in order, from its plain,
// archived and kept files, and sums it up.
func LoadSession(id string) (SessionInfo, []Entry, error) {
	dir, err := sessionsDir()
	if err != nil {
		return SessionInfo{}, nil, err
	}
	base := unsafeName.ReplaceAllString(id, "_") + ".jsonl"
	var es []Entry
	var last string
	for _, f := range logFiles(dir) {
		if name := filepath.Base(f.path); name != base && name != base+".gz" {
			continue
		}
		if _, more, err := parseLog(f.path); err == nil {
			es, last = append(es, more...), f.path
		}
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].T.Before(es[j].T) })
	return summarize(id, es, last), es, nil
}

// FindSession turns a bx session id, or the agent app's own session id,
// or the start of either, into a bx session id.
func FindSession(list []SessionInfo, ref string) string {
	for _, s := range list {
		if s.ID == ref {
			return s.ID
		}
	}
	for _, s := range list {
		if strings.HasPrefix(s.ID, ref) || (s.HSess != "" && strings.HasPrefix(s.HSess, ref)) {
			return s.ID
		}
	}
	return ref
}
