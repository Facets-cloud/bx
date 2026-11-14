package app

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// historyText answers "what did bx do": with no ref, the recent sessions;
// with current, previous or a session id, that session as a tree from
// start to end: its tasks, the pages each task worked on, and the actions
// on each page. limit caps the sessions listed, or the tasks shown (the
// latest ones).
func historyText(ref string, limit int) (string, error) {
	list, err := daemon.ListSessions()
	if err != nil {
		return "", err
	}
	cur := daemon.CurrentSession()
	if ref == "" {
		return sessionList(list, cur, limit), nil
	}
	info, es, err := daemon.LoadSession(resolveSession(ref, list, cur))
	if err != nil {
		return "", err
	}
	if len(es) == 0 {
		return "", exit.New(exit.User, "no bx activity logged for session %q (see bx history)", ref)
	}
	return sessionTree(info, es, cur, limit), nil
}

// resolveSession turns current or previous into a session id.
func resolveSession(ref string, list []daemon.SessionInfo, cur string) string {
	switch ref {
	case "current":
		return cur
	case "previous", "last":
		for _, s := range list {
			if s.ID != cur {
				return s.ID
			}
		}
		return ""
	}
	return daemon.FindSession(list, ref)
}

func sessionList(list []daemon.SessionInfo, cur string, limit int) string {
	if len(list) == 0 {
		return "no bx activity logged yet\n"
	}
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	var b strings.Builder
	for _, s := range list {
		b.WriteString(sessionLine(s, cur) + "\n")
	}
	return b.String()
}

// sessionLine is one session in a line: id, app, project, when, title,
// counts and sites.
func sessionLine(s daemon.SessionInfo, cur string) string {
	parts := []string{s.ID}
	if s.Harness != "" {
		parts = append(parts, s.Harness)
	}
	if s.Project != "" {
		parts = append(parts, filepath.Base(s.Project))
	}
	parts = append(parts, span(s.Start, s.End))
	if s.Title != "" {
		parts = append(parts, fmt.Sprintf("%q", s.Title))
	}
	parts = append(parts, counts(s.Actions, s.Errors))
	if len(s.Sites) > 0 {
		parts = append(parts, strings.Join(s.Sites, ", "))
	}
	if s.ID == cur {
		parts = append(parts, "(this session)")
	}
	return strings.Join(parts, "  ")
}

func span(a, b time.Time) string {
	return a.Local().Format("Jan 2 15:04:05") + "-" + b.Local().Format("15:04:05")
}

func counts(actions, failed int) string {
	s := fmt.Sprintf("%d actions", actions)
	if actions == 1 {
		s = "1 action"
	}
	if failed > 0 {
		s += fmt.Sprintf(", %d failed", failed)
	}
	return s
}

// node is one line of a tree and the lines under it.
type node struct {
	text string
	kids []*node
}

func (n *node) add(text string) *node {
	k := &node{text: text}
	n.kids = append(n.kids, k)
	return k
}

func (n *node) render(b *strings.Builder, prefix string) {
	for i, k := range n.kids {
		branch, next := "├─ ", "│  "
		if i == len(n.kids)-1 {
			branch, next = "└─ ", "   "
		}
		b.WriteString(prefix + branch + k.text + "\n")
		k.render(b, prefix+next)
	}
}

// task is a stretch of a session from start to done; requests outside
// any start make untitled tasks.
type task struct {
	title, summary string
	entries        []daemon.Entry // the actions, without start and done
	start, end     time.Time
	failed         int
}

func splitTasks(es []daemon.Entry) []*task {
	var out []*task
	var cur *task
	for _, e := range es {
		if cur == nil || e.Op == "start" {
			cur = &task{start: e.T}
			out = append(out, cur)
		}
		cur.end = e.T.Add(time.Duration(e.MS) * time.Millisecond)
		switch e.Op {
		case "start":
			cur.title = e.Target
		case "done":
			cur.summary = e.Target
			cur = nil
		default:
			cur.entries = append(cur.entries, e)
			if e.Error != "" {
				cur.failed++
			}
		}
	}
	return out
}

// sessionTree draws one session: a header, then each task with the pages
// it worked on and the actions on each page.
func sessionTree(info daemon.SessionInfo, es []daemon.Entry, cur string, limit int) string {
	var b strings.Builder
	b.WriteString(sessionLine(info, cur) + "\n")
	if r := resumeCommand(info); r != "" {
		b.WriteString("resume: " + r + "\n")
	}
	b.WriteString("log: " + info.Log + "\n")
	tasks, first := splitTasks(es), 0
	if limit > 0 && len(tasks) > limit {
		fmt.Fprintf(&b, "(%d earlier tasks left out)\n", len(tasks)-limit)
		first = len(tasks) - limit
	}
	root := &node{}
	for i := first; i < len(tasks); i++ {
		addTask(root, tasks[i], i+1)
	}
	root.render(&b, "")
	return b.String()
}

// addTask adds task number n (as bx history pin --task takes it) with its
// pages and actions.
func addTask(root *node, t *task, n int) {
	head := fmt.Sprintf("%d (untitled)", n)
	if t.title != "" {
		head = fmt.Sprintf("%d %q", n, t.title)
	}
	head += "  " + span(t.start, t.end) + "  " + counts(len(t.entries), t.failed)
	if t.summary != "" {
		head += fmt.Sprintf("  done: %q", t.summary)
	}
	tn := root.add(head)
	var page *node
	var at string
	for _, e := range t.entries {
		// Pages are grouped by URL: the profile's name can read differently
		// in a browser that has only just started.
		if url := daemon.ShortURL(e.URL); page == nil || url != at {
			page, at = tn.add(pageLabel(e)), url
		}
		page.add(actionLine(e))
	}
}

// pageLabel names the page an entry acted on: profile and URL.
func pageLabel(e daemon.Entry) string {
	if where := strings.TrimSpace(e.Profile + "  " + daemon.ShortURL(e.URL)); where != "" {
		return where
	}
	return "browser"
}

func actionLine(e daemon.Entry) string {
	s := e.T.Local().Format("15:04:05") + "  " + strings.TrimSpace(e.Op+" "+e.Target)
	if e.Error != "" {
		return s + "  FAILED: " + e.Error
	}
	s += fmt.Sprintf("  ok %s", time.Duration(e.MS)*time.Millisecond)
	if e.To != "" {
		s += "  -> " + daemon.ShortURL(e.To)
	}
	return s
}

// resumeCommand reopens the agent app's session the log came from.
func resumeCommand(s daemon.SessionInfo) string {
	if s.HSess == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(s.Harness, "claude-code"):
		return "claude --resume " + s.HSess
	case strings.HasPrefix(s.Harness, "codex"):
		return "codex resume " + s.HSess
	}
	return ""
}

// cmdHistory is bx history [<session>|current|previous] [--limit n], and
// its pin, pinned and unpin subcommands.
func cmdHistory(e env) error {
	if len(e.pos) > 0 {
		switch e.pos[0] {
		case "pin":
			return cmdPin(e)
		case "pinned":
			return cmdPinned(e)
		case "unpin":
			return cmdUnpin(e)
		}
	}
	ref := strings.Join(e.pos, " ")
	if e.o.json {
		list, err := daemon.ListSessions()
		if err != nil {
			return err
		}
		if ref == "" {
			e.o.printJSON(list)
			return nil
		}
		_, es, err := daemon.LoadSession(resolveSession(ref, list, daemon.CurrentSession()))
		if err != nil {
			return err
		}
		e.o.printJSON(es)
		return nil
	}
	limit, _ := strconv.Atoi(e.a.flags["limit"])
	text, err := historyText(ref, limit)
	if err != nil {
		return err
	}
	fmt.Fprint(e.o.w, text)
	return nil
}
